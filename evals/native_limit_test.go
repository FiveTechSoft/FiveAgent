package evals

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/trajectory"
)

func TestNativeLengthDoesNotRetryOrDeliver(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		io.WriteString(w, `{"model":"fixture","done":true,"done_reason":"length","prompt_eval_count":3000,"eval_count":1096,"message":{"role":"assistant","content":"En","thinking":"token: fixture-secret"}}`)
	}))
	defer srv.Close()
	a := newRecoverAgent(t, config.Model{Name: "fixture", BaseURL: srv.URL + "/v1", NumThread: 4}, config.Model{})
	var rec trajectory.Record
	a.WithTrajectory(func(r trajectory.Record) { rec = r })
	reply, err := a.Handle(context.Background(), "whatsapp", "fixture-user", "fixture question")
	if err == nil || !strings.Contains(err.Error(), "generation limit") || reply != "" {
		t.Fatalf("truncation escaped: %q %v", reply, err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatal("identical over-limit request retried")
	}
	if len(rec.ModelAttempts) != 1 || rec.ModelAttempts[0].DoneReason != "length" || rec.ModelAttempts[0].Purpose != "tool-round" || rec.ModelAttempts[0].HTTPStatus != 200 {
		t.Fatalf("missing attempts: %+v", rec)
	}
	if strings.Contains(rec.ModelAttempts[0].Thinking, "fixture-secret") {
		t.Fatal("thinking leaked")
	}
}

func TestNativeEmptyRecoveryAndForcedAttemptsRecorded(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		io.WriteString(w, `{"model":"fixture","done":true,"done_reason":"stop","message":{"role":"assistant","content":"","thinking":"fixture"}}`)
	}))
	defer srv.Close()
	a := newRecoverAgent(t, config.Model{Name: "fixture", BaseURL: srv.URL + "/v1", NumThread: 4}, config.Model{})
	var rec trajectory.Record
	a.WithTrajectory(func(r trajectory.Record) { rec = r })
	reply, err := a.Handle(context.Background(), "whatsapp", "fixture-user", "fixture question")
	if err != nil || !strings.Contains(reply, "Lo siento") {
		t.Fatalf("plain empty changed: %q %v", reply, err)
	}
	if calls != 6 || len(rec.ModelAttempts) != 6 {
		t.Fatalf("lost failed/forced attempts: %d %+v", calls, rec)
	}
	want := []string{"tool-round", "empty-retry", "empty-retry", "forced-answer", "forced-answer", "forced-answer"}
	for i, p := range want {
		if rec.ModelAttempts[i].Purpose != p || rec.ModelAttempts[i].Sequence != i+1 {
			t.Fatal("attempt order/purpose lost")
		}
	}
	if rec.Outcome.ToolRounds != 0 {
		t.Fatal("HTTP attempts changed tool-round counter")
	}
}
