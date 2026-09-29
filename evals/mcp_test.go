package evals

// mcp_test.go - stage 33 done-when: a fake MCP client drives the
// endpoint through the protocol - auth rejected without the token,
// initialize/tools-list/tools-call happy path, commands going through
// the sandbox (never around it), workspace reads confined, and every
// failure surfaced honestly.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/mcp"
	"github.com/FiveTechSoft/FiveAgent/internal/sandbox"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

// fakeSandbox records what the endpoint asked it to run. The MCP
// package's only execution path is Sandbox.Run, so this record is the
// proof that commands run confined to the sandbox.
type fakeSandbox struct {
	userKeys []string
	argvs    [][]string
	result   sandbox.Result
	err      error
}

func (f *fakeSandbox) Run(_ context.Context, userKey string, argv []string) (sandbox.Result, error) {
	f.userKeys = append(f.userKeys, userKey)
	f.argvs = append(f.argvs, argv)
	return f.result, f.err
}
func (f *fakeSandbox) Name() string { return "fake" }

type mcpClient struct {
	t   *testing.T
	url string
}

func (c mcpClient) call(token, body string) (int, map[string]any) {
	c.t.Helper()
	req, err := http.NewRequest(http.MethodPost, c.url, bytes.NewBufferString(body))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(bytes.TrimSpace(b)) > 0 {
		if err := json.Unmarshal(b, &out); err != nil {
			c.t.Fatalf("non-JSON response to %s: %q", body, b)
		}
	}
	return resp.StatusCode, out
}

func resultText(t *testing.T, resp map[string]any) (string, bool) {
	t.Helper()
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in %v", resp)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("no content blocks in %v", result)
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	isErr, _ := result["isError"].(bool)
	return text, isErr
}

func TestMCPProtocolBattery(t *testing.T) {
	fs := &fakeSandbox{result: sandbox.Result{Stdout: "hola desde el sandbox\n", ExitCode: 0}}
	wsRoot := t.TempDir()
	srv, err := mcp.New("token-secreto", fs, tools.Workspace{Root: wsRoot}, "")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	c := mcpClient{t: t, url: ts.URL + "/mcp"}

	// Auth: no token and wrong token are both refused, honestly.
	for _, tok := range []string{"", "token-mal"} {
		status, resp := c.call(tok, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
		if status != http.StatusUnauthorized {
			t.Errorf("token %q: want 401, got %d", tok, status)
		}
		if resp["error"] == nil {
			t.Errorf("token %q: the refusal must carry an error object", tok)
		}
	}

	// initialize.
	_, resp := c.call("token-secreto", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	init, ok := resp["result"].(map[string]any)
	if !ok || init["protocolVersion"] == "" {
		t.Fatalf("initialize must answer a protocolVersion: %v", resp)
	}
	if info, _ := init["serverInfo"].(map[string]any); info["name"] != "fiveagent" {
		t.Fatalf("serverInfo must name fiveagent: %v", init)
	}

	// A notification gets 202 and no body.
	status, resp := c.call("token-secreto", `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if status != http.StatusAccepted || resp != nil {
		t.Errorf("a notification must get 202 with no body, got %d %v", status, resp)
	}

	// tools/list: both tools with honest schemas.
	_, resp = c.call("token-secreto", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	tl, _ := resp["result"].(map[string]any)["tools"].([]any)
	names := map[string]bool{}
	for _, it := range tl {
		m, _ := it.(map[string]any)
		names[fmt.Sprint(m["name"])] = true
		if m["inputSchema"] == nil || m["description"] == "" {
			t.Errorf("every tool needs a schema and a description: %v", m)
		}
	}
	if !names["fiveagent_run_command"] || !names["fiveagent_read_file"] {
		t.Fatalf("tools/list must offer both tools: %v", names)
	}

	// tools/call run_command: goes through the sandbox, userKey mcp.
	_, resp = c.call("token-secreto",
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"fiveagent_run_command","arguments":{"argv":["echo","hola"]}}}`)
	text, isErr := resultText(t, resp)
	if isErr || !strings.Contains(text, "hola desde el sandbox") || !strings.Contains(text, "exit code: 0") {
		t.Errorf("run_command must return stdout and the exit code: %q (isError %v)", text, isErr)
	}
	if len(fs.argvs) != 1 || fs.argvs[0][0] != "echo" || fs.userKeys[0] != "mcp" {
		t.Fatalf("the command must reach the sandbox as argv under the mcp user key: %v %v", fs.argvs, fs.userKeys)
	}

	// A failing command is an honest error, never a fake success.
	fs.result = sandbox.Result{Stdout: "", Stderr: "boom", ExitCode: 3}
	_, resp = c.call("token-secreto",
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"fiveagent_run_command","arguments":{"argv":["false"]}}}`)
	text, isErr = resultText(t, resp)
	if !isErr || !strings.Contains(text, "boom") || !strings.Contains(text, "exit code: 3") {
		t.Errorf("a non-zero exit must surface stderr and isError: %q (isError %v)", text, isErr)
	}

	// Missing argv is an honest usage error.
	_, resp = c.call("token-secreto",
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"fiveagent_run_command","arguments":{}}}`)
	if _, isErr = resultText(t, resp); !isErr {
		t.Error("empty argv must be refused")
	}

	// read_file inside the workspace folder.
	folder := filepath.Join(wsRoot, "mcp-server")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "nota.txt"), []byte("contenido de la nota"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, resp = c.call("token-secreto",
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"fiveagent_read_file","arguments":{"path":"nota.txt"}}}`)
	text, isErr = resultText(t, resp)
	if isErr || !strings.Contains(text, "contenido de la nota") {
		t.Errorf("read_file must return the file content: %q (isError %v)", text, isErr)
	}

	// read_file escaping the folder is refused.
	_, resp = c.call("token-secreto",
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"fiveagent_read_file","arguments":{"path":"../../etc/passwd"}}}`)
	if _, isErr = resultText(t, resp); !isErr {
		t.Error("a .. escape must be refused")
	}

	// Unknown method and unknown tool: honest protocol errors.
	_, resp = c.call("token-secreto", `{"jsonrpc":"2.0","id":8,"method":"resources/list"}`)
	if e, _ := resp["error"].(map[string]any); e["code"] != -32601.0 {
		t.Errorf("unknown method must be -32601: %v", resp)
	}
	_, resp = c.call("token-secreto",
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"delete_everything","arguments":{}}}`)
	if e, _ := resp["error"].(map[string]any); e["code"] != -32602.0 {
		t.Errorf("unknown tool must be -32602: %v", resp)
	}
}
