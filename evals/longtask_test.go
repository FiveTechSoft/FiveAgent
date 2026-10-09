package evals

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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

// Long-task A/B. Arm A is the agent as it ships; arm B adds the two opt-in
// aids (progress file injected each turn, one corrective attempt per tool
// failure per turn). The model is a script, so these cases measure the
// MECHANISM: how many failing calls run, and whether the state needed to
// resume reaches the model. They say nothing about how a real model uses
// either aid; that needs the live battery.

type flakyTool struct {
	failTimes int32 // fail this many calls, then succeed (huge = always)
	calls     atomic.Int32
}

func (*flakyTool) Name() string                { return "flaky" }
func (*flakyTool) Description() string         { return "fake tool for the long-task A/B" }
func (*flakyTool) Parameters() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (f *flakyTool) Execute(context.Context, json.RawMessage) (string, error) {
	n := f.calls.Add(1)
	if n <= f.failTimes {
		return "", fmt.Errorf("simulated failure %d", n)
	}
	return "ok", nil
}

// stubborn keeps calling flaky until it sees a success or the stop notice.
func stubbornServer(t *testing.T, sawProgress *atomic.Bool, progressMark string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body := string(b)
		if progressMark != "" && strings.Contains(body, progressMark) {
			sawProgress.Store(true)
		}
		w.Header().Set("Content-Type", "application/json")
		last := body
		if i := strings.LastIndex(body, `"role":"tool"`); i >= 0 {
			last = body[i:]
		}
		switch {
		case strings.Contains(last, "fix_limit_reached"):
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"no pude, te cuento que fallo"}}]}`)
		case strings.Contains(last, `"role":"tool"`) && strings.Contains(last, "ok"):
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"hecho"}}]}`)
		default:
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"c","type":"function","function":{"name":"flaky","arguments":"{}"}}]}}]}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newLongTaskAgent(t *testing.T, srv *httptest.Server, reg *tools.Registry, hist string) *agent.Agent {
	t.Helper()
	cfg := config.Config{}
	cfg.Model = config.Model{BaseURL: srv.URL, Name: "mock"}
	store, err := memory.OpenJSON(filepath.Join(t.TempDir(), hist))
	if err != nil {
		t.Fatal(err)
	}
	return agent.New(model.NewOpenAICompat(cfg.Model), store, reg, agent.SystemPrompt(&cfg))
}

func TestLongTaskFixAttemptLimit(t *testing.T) {
	const trials = 10
	type result struct{ always, once int32 }
	run := func(limit int) result {
		var res result
		for i := 0; i < trials; i++ {
			for _, failTimes := range []int32{1 << 20, 1} {
				fl := &flakyTool{failTimes: failTimes}
				srv := stubbornServer(t, &atomic.Bool{}, "")
				a := newLongTaskAgent(t, srv, tools.NewRegistry(fl), "h.json")
				if limit > 0 {
					a.WithFixAttemptLimit(limit)
				}
				if _, err := a.Handle(context.Background(), "wa", "u", "haz la tarea"); err != nil {
					t.Fatal(err)
				}
				if failTimes > 1 {
					res.always += fl.calls.Load()
				} else {
					res.once += fl.calls.Load()
				}
			}
		}
		return res
	}
	a, b := run(0), run(1)
	t.Logf("A/B fix limit over %d trials: tool always failing -> executed calls per trial A=%.1f B=%.1f; tool failing once -> A=%.1f B=%.1f",
		trials, float64(a.always)/trials, float64(b.always)/trials, float64(a.once)/trials, float64(b.once)/trials)
	if b.always/trials != 2 {
		t.Fatalf("limit 1 must run the failing tool exactly twice (initial + one fix), got %.1f", float64(b.always)/trials)
	}
	if a.always/trials <= b.always/trials {
		t.Fatalf("baseline should retry more than the limited arm: A=%d B=%d", a.always, b.always)
	}
	if a.once != b.once || b.once/trials != 2 {
		t.Fatalf("a recoverable failure must behave the same in both arms: A=%d B=%d", a.once, b.once)
	}
}

func TestLongTaskResumeAfterHistoryLoss(t *testing.T) {
	const mark = "PASO-6-HECHO"
	resumed := func(withProgress bool) (bool, int) {
		var saw atomic.Bool
		srv := stubbornServer(t, &saw, mark)
		ws := tools.Workspace{Root: t.TempDir()}
		ctx := tools.WithRequestInfo(context.Background(), "wa", "u")
		// The model's own earlier notes, as write_file would have left them.
		dir := filepath.Join(ws.Root, "wa-u")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		notes := "pasos 1-5 hechos\n" + mark + "\nsiguiente: paso 7\n"
		if err := os.WriteFile(filepath.Join(dir, "PROGRESS.md"), []byte(notes), 0o644); err != nil {
			t.Fatal(err)
		}
		// Fresh history file = process restart or history pruned away.
		a := newLongTaskAgent(t, srv, tools.NewRegistry(&flakyTool{}), "fresh.json")
		if withProgress {
			a.WithProgressFile(ws, "PROGRESS.md")
		}
		if _, err := a.Handle(ctx, "wa", "u", "continua con la tarea"); err != nil {
			t.Fatal(err)
		}
		return saw.Load(), len(notes)
	}
	aSaw, _ := resumed(false)
	bSaw, added := resumed(true)
	t.Logf("A/B resume after history loss: state reaches the model A=%v B=%v; extra context in B = %d chars per turn (cap 2000)", aSaw, bSaw, added)
	if aSaw {
		t.Fatal("baseline should not see the progress notes")
	}
	if !bSaw {
		t.Fatal("progress file content did not reach the model")
	}
}

func TestLongTaskProgressStaysInsideUserFolder(t *testing.T) {
	var saw atomic.Bool
	srv := stubbornServer(t, &saw, "SECRETO-AJENO")
	ws := tools.Workspace{Root: t.TempDir()}
	other := filepath.Join(ws.Root, "wa-other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(other, "PROGRESS.md"), []byte("SECRETO-AJENO"), 0o644)
	a := newLongTaskAgent(t, srv, tools.NewRegistry(&flakyTool{}), "h.json")
	a.WithProgressFile(ws, "PROGRESS.md")
	if _, err := a.Handle(context.Background(), "wa", "me", "continua"); err != nil {
		t.Fatal(err)
	}
	if saw.Load() {
		t.Fatal("another user's progress notes reached the model")
	}
}
