package evals

// Hardening cases for the MCP endpoint, found in an audit: scheme-less
// tokens were accepted, browser origins and rebinding hosts were not
// refused, weak tokens and the unisolated backend were allowed, and
// command concurrency was unbounded. All with a fake sandbox.

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/mcp"
	"github.com/FiveTechSoft/FiveAgent/internal/sandbox"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

const mcpTestToken = "token-secreto-0123"

func mcpTestServer(t *testing.T, sb sandbox.Sandbox) (*mcp.Server, *httptest.Server) {
	t.Helper()
	srv, err := mcp.New(mcpTestToken, sb, tools.Workspace{Root: t.TempDir()}, "")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return srv, ts
}

func rawPost(t *testing.T, url string, hdr map[string]string, host string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url+"/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if host != "" {
		req.Host = host
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestMCPTokenNeedsBearerScheme(t *testing.T) {
	_, ts := mcpTestServer(t, &fakeSandbox{})
	if s := rawPost(t, ts.URL, map[string]string{"Authorization": mcpTestToken}, ""); s != http.StatusUnauthorized {
		t.Fatalf("token without the Bearer scheme: want 401, got %d", s)
	}
	if s := rawPost(t, ts.URL, map[string]string{"Authorization": "Basic " + mcpTestToken}, ""); s != http.StatusUnauthorized {
		t.Fatalf("other scheme: want 401, got %d", s)
	}
	if s := rawPost(t, ts.URL, map[string]string{"Authorization": "bearer " + mcpTestToken}, ""); s != http.StatusOK {
		t.Fatalf("lowercase scheme with the right token: want 200, got %d", s)
	}
}

func TestMCPRefusesBrowserOriginEvenWithToken(t *testing.T) {
	_, ts := mcpTestServer(t, &fakeSandbox{})
	h := map[string]string{"Authorization": "Bearer " + mcpTestToken, "Origin": "https://evil.example"}
	if s := rawPost(t, ts.URL, h, ""); s != http.StatusForbidden {
		t.Fatalf("browser origin: want 403, got %d", s)
	}
}

func TestMCPLoopbackHostRestriction(t *testing.T) {
	srv, ts := mcpTestServer(t, &fakeSandbox{})
	h := map[string]string{"Authorization": "Bearer " + mcpTestToken}
	// Off by default: any Host passes (tunnels, other binds keep working).
	if s := rawPost(t, ts.URL, h, "rebind.example:8090"); s != http.StatusOK {
		t.Fatalf("unrestricted server: want 200, got %d", s)
	}
	srv.RestrictToLoopback()
	if s := rawPost(t, ts.URL, h, "rebind.example:8090"); s != http.StatusForbidden {
		t.Fatalf("rebinding host: want 403, got %d", s)
	}
	for _, host := range []string{"127.0.0.1:8090", "localhost:8090", "[::1]:8090"} {
		if s := rawPost(t, ts.URL, h, host); s != http.StatusOK {
			t.Fatalf("loopback host %s: want 200, got %d", host, s)
		}
	}
}

type namedSandbox struct {
	fakeSandbox
	name string
}

func (n *namedSandbox) Name() string { return n.name }

func TestMCPRefusesWeakSetups(t *testing.T) {
	if _, err := mcp.New("corto", &fakeSandbox{}, tools.Workspace{Root: t.TempDir()}, ""); err == nil {
		t.Fatal("a short token must be refused")
	}
	if _, err := mcp.New(mcpTestToken, &namedSandbox{name: "jobobject"}, tools.Workspace{Root: t.TempDir()}, ""); err == nil {
		t.Fatal("the unisolated jobobject backend must be refused")
	}
	for _, ok := range []string{"bubblewrap", "docker", "appcontainer"} {
		if _, err := mcp.New(mcpTestToken, &namedSandbox{name: ok}, tools.Workspace{Root: t.TempDir()}, ""); err != nil {
			t.Fatalf("backend %s must be accepted: %v", ok, err)
		}
	}
}

// blockingSandbox holds every command until released.
type blockingSandbox struct {
	fakeSandbox
	mu      sync.Mutex
	running int
	max     int
	release chan struct{}
}

func (b *blockingSandbox) Run(ctx context.Context, k string, a []string) (sandbox.Result, error) {
	b.mu.Lock()
	b.running++
	if b.running > b.max {
		b.max = b.running
	}
	b.mu.Unlock()
	<-b.release
	b.mu.Lock()
	b.running--
	b.mu.Unlock()
	return sandbox.Result{Stdout: "ok"}, nil
}

func TestMCPBoundsConcurrentCommands(t *testing.T) {
	bs := &blockingSandbox{release: make(chan struct{})}
	_, ts := mcpTestServer(t, bs)
	c := mcpClient{t: t, url: ts.URL + "/mcp"}
	call := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"fiveagent_run_command","arguments":{"argv":["x"]}}}`
	var wg sync.WaitGroup
	var busy, done int
	var mu sync.Mutex
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, resp := c.call(mcpTestToken, call)
			txt, isErr := resultText(t, resp)
			mu.Lock()
			defer mu.Unlock()
			if isErr && strings.Contains(txt, "too many commands") {
				busy++
			} else {
				done++
			}
		}()
	}
	// Wait until the busy replies have come back, then free the runners.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		b := busy
		mu.Unlock()
		if b >= 8 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(bs.release)
	wg.Wait()
	if busy < 8 {
		t.Fatalf("expected 8 busy refusals, got %d", busy)
	}
	if bs.max > 4 {
		t.Fatalf("up to %d commands ran at once, cap is 4", bs.max)
	}
	t.Logf("12 simultaneous calls: %d ran (max concurrent %d), %d refused as busy", done, bs.max, busy)
}

// The folder the commands write to is the folder read_file reads, and a
// symlink left there by a command cannot be used to read outside it.
func TestMCPReadFileSeesCommandFolderAndRefusesSymlinks(t *testing.T) {
	root := t.TempDir()
	srv, err := mcp.New(mcpTestToken, &fakeSandbox{}, tools.Workspace{Root: root}, "")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	c := mcpClient{t: t, url: ts.URL + "/mcp"}
	folder := filepath.Join(root, "mcp") // where the sandbox runs for user key "mcp"
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "fuera.txt")
	if err := os.WriteFile(outside, []byte("FAKE-SECRET-fuera"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "salida.txt"), []byte("lo que escribio un comando"), 0o600); err != nil {
		t.Fatal(err)
	}
	read := func(path string) (string, bool) {
		_, resp := c.call(mcpTestToken, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"fiveagent_read_file","arguments":{"path":"`+path+`"}}}`)
		return resultText(t, resp)
	}
	if txt, isErr := read("salida.txt"); isErr || !strings.Contains(txt, "lo que escribio") {
		t.Fatalf("a file in the command folder must be readable: %q (isError %v)", txt, isErr)
	}
	if err := os.Symlink(outside, filepath.Join(folder, "enlace.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if txt, _ := read("enlace.txt"); strings.Contains(txt, "FAKE-SECRET-fuera") {
		t.Fatal("a symlink in the command folder leaked a file outside it")
	}
}
