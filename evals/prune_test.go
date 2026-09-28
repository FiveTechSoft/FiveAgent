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

// Stage 15 battery case: a 61-turn conversation with verbose turns
// (1KB of user text each) and periodic verbose tool calls stays inside
// the context budget and still recalls the facts from its first turns.
// The agent runs for real (store, tools, pruning, model over
// httptest); the model is scripted.
func TestContextPruningStaysInBudgetAndRecalls(t *testing.T) {
	fact := "mi perro se llama Toby"
	probe := &probeTool{}
	reg := tools.NewRegistry(probe)

	// The scripted server answers a probe call when the last message
	// is exactly a tool-turn prompt, the final answer once the tool
	// result is in, and a plain answer otherwise (this also covers the
	// pruner's summarizer pass). It records the last request body so
	// the test can measure what the model was actually sent.
	var lastBody atomic.Value // []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		lastBody.Store(body)
		w.Header().Set("Content-Type", "application/json")
		var req struct {
			Messages []model.Message `json:"messages"`
		}
		if err := json.Unmarshal(body, &req); err != nil || len(req.Messages) == 0 {
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"entendido"}}]}`)
			return
		}
		last := req.Messages[len(req.Messages)-1]
		if last.Role == "user" && strings.HasPrefix(last.Content, "turno herramienta") {
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"",`+
				`"tool_calls":[{"id":"c1","type":"function","function":{"name":"probe",`+
				`"arguments":"{\"count\":7}"}}]}}]}`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"entendido"}}]}`)
	}))
	t.Cleanup(srv.Close)

	store, err := memory.OpenJSON(filepath.Join(t.TempDir(), "history.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Model: config.Model{BaseURL: srv.URL, Name: "eval"}}
	budget := 12000
	a := agent.New(model.NewOpenAICompat(cfg.Model), store, reg, agent.SystemPrompt(cfg)).
		WithPruning(agent.PruneConfig{MaxChars: budget, ToolOutputKeep: 200})

	ctx := context.Background()
	if _, err := a.Handle(ctx, "whatsapp", "u1", "hola, recuerda esto: "+fact); err != nil {
		t.Fatal(err)
	}
	// 61 turns total; the last one is deliberately a normal turn so the
	// measured request is a plain history+prompt call, and every 6th
	// turn is a tool turn (60 is, 61 is not).
	for turn := 2; turn <= 61; turn++ {
		text := "turno normal de charla " + strings.Repeat("relleno ", 120)
		if turn%6 == 0 {
			text = "turno herramienta: sonda el sistema " + strings.Repeat("relleno ", 120)
		}
		if _, err := a.Handle(ctx, "whatsapp", "u1", text); err != nil {
			t.Fatalf("turn %d: %v", turn, err)
		}
	}

	// What the model was actually sent on the last turn: the history
	// within budget (system prompt rides outside it), the compaction
	// marker present (proof the pruning fired), and the turn-1 fact
	// verbatim (head protection).
	body := lastBody.Load().([]byte)
	var sent struct {
		Messages []model.Message `json:"messages"`
	}
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatal(err)
	}
	histChars := 0
	foundFact, foundMarker := false, false
	for _, m := range sent.Messages {
		if m.Role == "system" && strings.HasPrefix(m.Content, "You are FiveAgent") {
			continue // the system prompt rides outside the history budget
		}
		histChars += len(m.Content)
		if strings.Contains(m.Content, fact) {
			foundFact = true
		}
		if strings.Contains(m.Content, "earlier turns") {
			foundMarker = true
		}
	}
	if histChars > budget {
		t.Fatalf("history over budget: %d > %d", histChars, budget)
	}
	if !foundMarker {
		t.Fatal("no pruning marker in the sent history: pruning never fired, the test proves nothing")
	}
	if !foundFact {
		t.Fatalf("turn-1 fact %q did not survive pruning", fact)
	}
}
