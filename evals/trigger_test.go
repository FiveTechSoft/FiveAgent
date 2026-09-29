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

// Stage 17 battery cases: keyword-triggered context. The scripted
// endpoint captures the exact request the model receives, so the test
// measures the injected skill bytes per turn - the done-when metric.
func captureOneTurn(t *testing.T, prompt string) string {
	t.Helper()
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"vale"}}]}`)
	}))
	t.Cleanup(srv.Close)
	store, err := memory.OpenJSON(filepath.Join(t.TempDir(), "history.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Model{BaseURL: srv.URL, Name: "eval", Timeout: 10}
	a := agent.New(model.NewOpenAICompat(cfg), store, tools.NewRegistry(), agent.SystemPrompt(&config.Config{}))
	a.WithSkills(agent.DomainSkill())
	if _, err := a.Handle(context.Background(), "whatsapp", "u1", prompt); err != nil {
		t.Fatal(err)
	}
	return body
}

// An unrelated turn carries zero skill text.
func TestTriggerUnrelatedTurnInjectsZero(t *testing.T) {
	body := captureOneTurn(t, "¿cuál es la capital de Portugal?")
	if strings.Contains(body, "AUTHORITATIVE") || strings.Contains(body, "FiveWin for Harbour") {
		t.Fatal("an unrelated turn must carry zero domain skill text")
	}
	t.Logf("METRIC trigger unrelated: 0 skill bytes injected")
}

// A matching turn carries exactly one injection of the skill text.
func TestTriggerMatchingTurnInjectsOnce(t *testing.T) {
	body := captureOneTurn(t, "¿qué es FWH?")
	n := strings.Count(body, "AUTHORITATIVE")
	if n != 1 {
		t.Fatalf("a matching turn must inject the domain skill exactly once, got %d", n)
	}
	block := agent.DomainSkill().Load()
	t.Logf("METRIC trigger fwh: %d skill bytes injected (%d chars of source)", len(block), len(block))
}

// The trigger list covers every FiveTech prompt of the live battery's
// harbour_fivewin category - otherwise the triggered design would
// silently un-ground those answers.
func TestTriggerCoversBatteryFiveTechPrompts(t *testing.T) {
	bf := loadBattery(t)
	for i, p := range bf.Categories["harbour_fivewin"] {
		body := captureOneTurn(t, p.Prompt)
		if !strings.Contains(body, "AUTHORITATIVE") {
			t.Errorf("harbour_fivewin[%d] %q does NOT trigger the domain skill", i, p.Prompt)
		}
	}
}
