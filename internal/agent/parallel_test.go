package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/memory"
	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

// parallelRig builds an agent whose fake model sleeps per subturn
// and counts concurrent calls, so the tests prove real overlap and a
// respected cap instead of trusting the pool's construction.
func parallelRig(t *testing.T, sleep time.Duration, failOn string) (*Agent, *int32, *int32) {
	t.Helper()
	var inFlight, maxSeen int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if failOn != "" && strings.Contains(string(body), failOn) {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":"boom"}`)
			return
		}
		cur := atomic.AddInt32(&inFlight, 1)
		for {
			m := atomic.LoadInt32(&maxSeen)
			if cur <= m || atomic.CompareAndSwapInt32(&maxSeen, m, cur) {
				break
			}
		}
		time.Sleep(sleep)
		atomic.AddInt32(&inFlight, -1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%q}}]}`, "done")
	}))
	t.Cleanup(srv.Close)
	cfg := &config.Config{Model: config.Model{BaseURL: srv.URL, Name: "eval"}}
	store, err := memory.OpenJSON(filepath.Join(t.TempDir(), "history.json"))
	if err != nil {
		t.Fatal(err)
	}
	a := New(model.NewOpenAICompat(cfg.Model), store, tools.NewRegistry(), "sys")
	return a, &inFlight, &maxSeen
}

func TestParallelOverlapAndCap(t *testing.T) {
	a, _, maxSeen := parallelRig(t, 150*time.Millisecond, "")
	tasks := []string{"t1", "t2", "t3", "t4", "t5", "t6", "t7", "t8"}
	start := time.Now()
	results := a.runSubTurns(context.Background(), tasks)
	elapsed := time.Since(start)

	for i, r := range results {
		if r.err != nil || r.reply != "done" {
			t.Fatalf("slot %d broken: %+v", i, r)
		}
		if r.task != tasks[i] {
			t.Fatalf("slot %d out of order: %q", i, r.task)
		}
	}
	if m := atomic.LoadInt32(maxSeen); m > maxParallelSubtasks {
		t.Fatalf("concurrency cap broken: saw %d workers", m)
	} else if m < 2 {
		t.Fatalf("no real overlap: max concurrency %d", m)
	}
	// Sequential would take 8 x 150ms = 1.2s; two waves take ~300ms.
	if elapsed > 900*time.Millisecond {
		t.Fatalf("no latency win: %s for 8 x 150ms tasks", elapsed)
	}
}

func TestParallelFailureIsolation(t *testing.T) {
	a, _, _ := parallelRig(t, 10*time.Millisecond, "explota")
	results := a.runSubTurns(context.Background(), []string{"bien uno", "explota siempre", "bien dos"})
	if results[0].err != nil || results[2].err != nil {
		t.Fatalf("healthy subtasks failed: %+v", results)
	}
	if results[1].err == nil {
		t.Fatal("failing subtask came back clean")
	}
	if !strings.Contains(results[1].err.Error(), "subtask 2") {
		t.Fatalf("error not attributed to its slot: %v", results[1].err)
	}
	if results[0].reply != "done" || results[2].reply != "done" {
		t.Fatalf("one failure tainted the others: %+v", results)
	}
}

func TestRunSubtasksToolValidation(t *testing.T) {
	a, _, _ := parallelRig(t, time.Millisecond, "")
	tool := runSubtasksTool{a: a}
	ctx := context.Background()
	if _, err := tool.Execute(ctx, json.RawMessage(`{"tasks":[]}`)); err == nil {
		t.Fatal("empty list accepted")
	}
	if _, err := tool.Execute(ctx, json.RawMessage(`{"tasks":["ok", "  "]}`)); err == nil {
		t.Fatal("empty task accepted")
	}
	var many strings.Builder
	many.WriteString(`{"tasks":[`)
	for i := 0; i < maxSubtasksPerCall+1; i++ {
		if i > 0 {
			many.WriteString(",")
		}
		fmt.Fprintf(&many, `"t%d"`, i)
	}
	many.WriteString(`]}`)
	if _, err := tool.Execute(ctx, json.RawMessage(many.String())); err == nil {
		t.Fatal("over-cap fan-out accepted")
	}
	// A healthy fan-out aggregates every slot, errors honestly.
	out, err := tool.Execute(ctx, json.RawMessage(`{"tasks":["alpha", "beta"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "=== Subtask 1: alpha") || !strings.Contains(out, "=== Subtask 2: beta") {
		t.Fatalf("aggregation wrong:\n%s", out)
	}
}
