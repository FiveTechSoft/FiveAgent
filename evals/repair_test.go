package evals

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/agent"
	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/memory"
	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

// probeTool records the decoded arguments it receives, so the test can
// see what survived the trip: mistyped on the wire, coerced on arrival.
type probeTool struct{ got map[string]any }

func (p *probeTool) Name() string        { return "probe" }
func (p *probeTool) Description() string { return "test probe" }
func (p *probeTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
		"count":{"type":"integer"},"force":{"type":"boolean"},"args":{"type":"array"}}}`)
}
func (p *probeTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	json.Unmarshal(args, &p.got)
	return "probe ok", nil
}

// scriptedModel answers chat completions from a script: one response
// per request, in order.
func scriptedModel(t *testing.T, responses ...string) (*httptest.Server, *int64) {
	t.Helper()
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		n := atomic.AddInt64(&calls, 1)
		if int(n) > len(responses) {
			t.Errorf("model called more than %d times", len(responses))
			n = int64(len(responses))
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, responses[n-1])
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func newTurn(t *testing.T, srv *httptest.Server, reg *tools.Registry) *agent.Agent {
	t.Helper()
	store, err := memory.OpenJSON(filepath.Join(t.TempDir(), "history.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Model: config.Model{BaseURL: srv.URL, Name: "eval"}}
	return agent.New(model.NewOpenAICompat(cfg.Model), store, reg, agent.SystemPrompt(cfg))
}

// Stage 14 battery case 1: a tool call with mistyped arguments ("7" as
// string, "true" as string, a scalar where an array goes) succeeds
// after conservative schema-guided coercion, and the turn completes.
func TestToolCallRepairRescuesMistypedArgs(t *testing.T) {
	probe := &probeTool{}
	srv, _ := scriptedModel(t,
		`{"choices":[{"message":{"role":"assistant","content":"",`+
			`"tool_calls":[{"id":"c1","type":"function","function":{"name":"probe",`+
			`"arguments":"{\"count\":\"7\",\"force\":\"true\",\"args\":\"ls\"}"}}]}}]}`,
		`{"choices":[{"message":{"role":"assistant","content":"listo, siete y verdadero"}}]}`,
	)
	a := newTurn(t, srv, tools.NewRegistry(probe))
	reply, err := a.Handle(context.Background(), "whatsapp", "u1", "prueba la sonda")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply, "siete") {
		t.Errorf("turn did not complete after repair: %q", reply)
	}
	// What the tool received must be the coerced types: json numbers
	// decode as float64, so a real string would stay a string here.
	if probe.got["count"] != float64(7) {
		t.Errorf("count arrived uncoerced: %#v", probe.got["count"])
	}
	if probe.got["force"] != true {
		t.Errorf("force arrived uncoerced: %#v", probe.got["force"])
	}
	arr, ok := probe.got["args"].([]any)
	if !ok || len(arr) != 1 || arr[0] != "ls" {
		t.Errorf("args arrived unwrapped: %#v", probe.got["args"])
	}
	t.Log("METRIC stage14 rescued calls: 1 (string->int, string->bool, scalar->array)")
}

// Stage 14 battery case 2: a degenerate reply dominated by one long
// repeated fragment is aborted by the repetition guard, never
// delivered, and never stored.
func TestRepetitionGuardAbortsDegenerateReply(t *testing.T) {
	frag := "La respuesta es siempre la misma, una y otra vez. "
	srv, _ := scriptedModel(t,
		`{"choices":[{"message":{"role":"assistant","content":`+
			jsonString(strings.Repeat(frag, 6))+`}}]}`,
	)
	a := newTurn(t, srv, tools.NewRegistry())
	_, err := a.Handle(context.Background(), "whatsapp", "u1", "hola")
	if err == nil || !strings.Contains(err.Error(), "repetition guard") {
		t.Fatalf("degenerate reply should abort with the guard error, got: %v", err)
	}
	t.Log("METRIC stage14 degenerate replies aborted before delivery: 1")
}

// jsonString quotes s as a JSON string literal.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
