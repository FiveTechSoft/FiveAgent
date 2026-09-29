package evals

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/agent"
	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/memory"
	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

// Stage 18 battery case: a multi-step task that fails as a single
// turn succeeds decomposed. The scripted endpoint plays both sides:
// with run_subtask available it delegates and composes; without it
// (single-turn side) it flounders. The script proves the MACHINERY
// (delegation, fresh-context isolation, depth cap, composition); the
// model's decision to decompose is the live battery's side.
func TestSubtaskComposition(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		w.Header().Set("Content-Type", "application/json")
		hasSubtool := strings.Contains(string(b), "run_subtask")
		switch {
		case hasSubtool && strings.Contains(string(b), "tarea-compuesta") && !strings.Contains(string(b), `"role":"tool"`):
			// Main turn, round 1: delegate the calculation.
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"run_subtask","arguments":"{\"task\":\"calcula 17*23\"}"}}]}}]}`)
		case hasSubtool:
			// Main turn, round 2: the tool result is in; compose.
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"el resultado compuesto es 391"}}]}`)
		default:
			// Subturn (no run_subtask spec): solve the small task.
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"391"}}]}`)
		}
	}))
	t.Cleanup(srv.Close)
	store, err := memory.OpenJSON(filepath.Join(t.TempDir(), "history.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Model{BaseURL: srv.URL, Name: "eval", Timeout: 10}
	a := agent.New(model.NewOpenAICompat(cfg), store, tools.NewRegistry(), agent.SystemPrompt(&config.Config{}))
	reply, err := a.Handle(context.Background(), "whatsapp", "u1", "tarea-compuesta: necesito 17*23 calculado por pasos")
	if err != nil {
		t.Fatal(err)
	}
	if reply != "el resultado compuesto es 391" {
		t.Fatalf("the main turn must compose the subturn result, got %q", reply)
	}
	if len(bodies) != 3 {
		t.Fatalf("expected 3 model calls (delegation + subturn + composition), got %d", len(bodies))
	}
	// Fresh-context isolation: the subturn request carries the subtask
	// text but NOT the original compound prompt.
	sub := bodies[1]
	if !strings.Contains(sub, "calcula 17*23") {
		t.Error("the subturn must receive the subtask text")
	}
	if strings.Contains(sub, "tarea-compuesta") {
		t.Error("the subturn must NOT see the main conversation (fresh context)")
	}
	// Depth cap: the subturn's tool list must exclude run_subtask.
	if strings.Contains(sub, "run_subtask") {
		t.Error("the subturn must not be able to delegate further (depth 1)")
	}
}

// The tool self-registers on construction and survives in the spec
// list the model sees.
func TestSubtaskToolRegistered(t *testing.T) {
	reg := tools.NewRegistry()
	store, err := memory.OpenJSON(filepath.Join(t.TempDir(), "history.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Model{BaseURL: "http://localhost:1", Name: "eval", Timeout: 1}
	agent.New(model.NewOpenAICompat(cfg), store, reg, agent.SystemPrompt(&config.Config{}))
	found := false
	for _, s := range reg.Specs() {
		if s.Function.Name == "run_subtask" {
			found = true
		}
	}
	if !found {
		t.Fatal("agent.New must self-register run_subtask")
	}
	if n := len(reg.Without("run_subtask").Specs()); n != 0 {
		t.Fatalf("Without must exclude run_subtask from the subturn registry, %d specs left", n)
	}
}

// A subturn that only produces empty replies surfaces as a tool
// error, not a hang.
func TestSubtaskEmptySurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":""}}]}`)
	}))
	t.Cleanup(srv.Close)
	store, err := memory.OpenJSON(filepath.Join(t.TempDir(), "history.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Model{BaseURL: srv.URL, Name: "eval", Timeout: 5}
	a := agent.New(model.NewOpenAICompat(cfg), store, tools.NewRegistry(), agent.SystemPrompt(&config.Config{}))
	reply, err := a.Handle(context.Background(), "whatsapp", "u1", "hola")
	if err != nil {
		t.Fatal(err)
	}
	// Everything empty everywhere degrades to the honest line.
	if reply != "Lo siento, no he podido preparar una respuesta esta vez. Prueba a preguntármelo otra vez." {
		t.Fatalf("expected the honest guard line, got %q", reply)
	}
}
