// Package mcp exposes FiveAgent as a local, token-authenticated MCP
// server (stage 33 of docs/ROADMAP.md), so an external agent - the
// owner's OpenCode is the design consumer - can run commands inside
// the owner's sandbox and read files from the workspace folder.
//
// One POST endpoint, /mcp, speaking JSON-RPC 2.0 with the MCP
// streamable-HTTP shape: initialize, ping, notifications (202, no
// body), tools/list and tools/call. Every call needs the bearer
// token from fiveagent.yml (mcp.token); a missing or wrong token
// gets an honest 401, and tool failures come back as isError
// results with the real stderr and exit code - never a fake
// success.
//
// Confinement is by construction: the only execution path is
// sandbox.Sandbox.Run and the only file path is the workspace's
// resolve, which refuses ".." escapes. This package never touches
// os/exec or the filesystem directly.
package mcp

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/FiveTechSoft/FiveAgent/internal/sandbox"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

// protocolVersion is the MCP revision this server speaks.
const protocolVersion = "2025-06-18"

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Server is the MCP endpoint. It is an http.Handler; the caller owns
// the listener (see app/fiveagent).
type Server struct {
	token   string
	sb      sandbox.Sandbox
	ws      tools.Workspace
	userKey string
	mux     *http.ServeMux
}

// New builds the server. The token is required; the sandbox is
// required because fiveagent_run_command is the point of the server.
func New(token string, sb sandbox.Sandbox, ws tools.Workspace, userKey string) (*Server, error) {
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("mcp: a bearer token is required (set mcp.token in fiveagent.yml)")
	}
	if sb == nil {
		return nil, fmt.Errorf("mcp: the sandbox is required (set sandbox.enabled: true in fiveagent.yml)")
	}
	if userKey == "" {
		userKey = "mcp"
	}
	s := &Server{token: token, sb: sb, ws: ws, userKey: userKey, mux: http.NewServeMux()}
	s.mux.HandleFunc("/mcp", s.handle)
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func writeRPC(w http.ResponseWriter, resp rpcResponse) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "the MCP endpoint takes POST only", http.StatusMethodNotAllowed)
		return
	}
	auth := r.Header.Get("Authorization")
	tok := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	if tok == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(s.token)) != 1 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0",
			Error: &rpcError{Code: -32001, Message: "missing or wrong bearer token"}})
		return
	}
	var req rpcRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeRPC(w, rpcResponse{JSONRPC: "2.0",
			Error: &rpcError{Code: -32700, Message: "parse error: " + err.Error()}})
		return
	}
	// A notification (no id) gets 202 and no body, per the MCP
	// streamable-HTTP shape.
	if len(req.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	switch req.Method {
	case "initialize":
		writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "fiveagent", "version": "dev"},
		}})
	case "ping":
		writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}})
	case "tools/list":
		writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"tools": toolList(),
		}})
	case "tools/call":
		s.callTool(w, r, req)
	default:
		writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: req.ID,
			Error: &rpcError{Code: -32601, Message: fmt.Sprintf("unknown method %q", req.Method)}})
	}
}

func toolList() []map[string]any {
	return []map[string]any{
		{
			"name": "fiveagent_run_command",
			"description": "Run a command inside the owner's sandboxed folder. " +
				"The command and its arguments go to the sandbox as argv (no shell); " +
				"stdout, stderr and the exit code come back, and a non-zero exit or a " +
				"timeout is reported as an honest error.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"argv": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "command and arguments, e.g. [\"go\", \"test\", \"./...\"]",
					},
				},
				"required": []string{"argv"},
			},
		},
		{
			"name": "fiveagent_read_file",
			"description": "Read a file from the owner's workspace folder. Paths are " +
				"confined to that folder; \"..\" escapes are refused with an error.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "path relative to the workspace folder",
					},
				},
				"required": []string{"path"},
			},
		},
	}
}

// toolResult is the MCP tools/call payload: content blocks plus an
// honest isError flag.
func toolResult(text string, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	}
}

func (s *Server) callTool(w http.ResponseWriter, r *http.Request, req rpcRequest) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: req.ID,
			Error: &rpcError{Code: -32602, Message: "tools/call params: " + err.Error()}})
		return
	}
	switch p.Name {
	case "fiveagent_run_command":
		var args struct {
			Argv []string `json:"argv"`
		}
		if err := json.Unmarshal(p.Arguments, &args); err != nil || len(args.Argv) == 0 {
			writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: toolResult(
				"fiveagent_run_command needs a non-empty argv array", true)})
			return
		}
		res, err := s.sb.Run(r.Context(), s.userKey, args.Argv)
		if err != nil {
			writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: toolResult(
				"sandbox refused the command: "+err.Error(), true)})
			return
		}
		text := res.Stdout
		if res.Stderr != "" {
			text += "\n[stderr]\n" + res.Stderr
		}
		text += fmt.Sprintf("\n[exit code: %d]", res.ExitCode)
		if res.TimedOut {
			text += "\n[timed out]"
		}
		writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: toolResult(
			strings.TrimSpace(text), res.ExitCode != 0 || res.TimedOut)})
	case "fiveagent_read_file":
		var args struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(p.Arguments, &args); err != nil || strings.TrimSpace(args.Path) == "" {
			writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: toolResult(
				"fiveagent_read_file needs a path", true)})
			return
		}
		// The workspace confines the path to the MCP user's folder and
		// refuses ".." escapes.
		ctx := tools.WithRequestInfo(r.Context(), "mcp", "server")
		out, err := (tools.ReadFile{WS: s.ws}).Execute(ctx, p.Arguments)
		if err != nil {
			writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: toolResult(err.Error(), true)})
			return
		}
		writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: toolResult(out, false)})
	default:
		writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: req.ID,
			Error: &rpcError{Code: -32602, Message: fmt.Sprintf("unknown tool %q", p.Name)}})
	}
}
