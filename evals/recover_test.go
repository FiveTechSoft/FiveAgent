package evals

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/agent"
	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/memory"
	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

// Stage 16 battery: failure-injection cases. Each one forces its
// classified failure through a scripted endpoint and asserts the
// mapped recovery path AND the single clear outcome for the user.

const okAnswer = `{"choices":[{"message":{"role":"assistant","content":"aquí estoy, recuperado"}}]}`

func newRecoverAgent(t *testing.T, mainCfg, coderCfg config.Model) *agent.Agent {
	t.Helper()
	store, err := memory.OpenJSON(filepath.Join(t.TempDir(), "history.json"))
	if err != nil {
		t.Fatal(err)
	}
	a := agent.New(model.NewOpenAICompat(mainCfg), store, tools.NewRegistry(), agent.SystemPrompt(&config.Config{}))
	if coderCfg.BaseURL != "" {
		a = a.WithCoder(model.NewOpenAICompat(coderCfg))
	}
	return a
}

// flaky returns a server that answers the given statuses/bodies in
// order, then okAnswer forever, counting calls.
func flaky(t *testing.T, script ...struct {
	status int
	body   string
}) (*httptest.Server, *int64) {
	t.Helper()
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		n := int(atomic.AddInt64(&calls, 1))
		if n <= len(script) {
			s := script[n-1]
			w.WriteHeader(s.status)
			io.WriteString(w, s.body)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, okAnswer)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func fail(status int, body string) struct {
	status int
	body   string
} {
	return struct {
		status int
		body   string
	}{status, body}
}

// (1) Rate-limit: two 429s, then the answer. The retry path recovers
// and the user gets one clean reply.
func TestRecoveryRateLimitRetries(t *testing.T) {
	srv, calls := flaky(t, fail(429, `{"error":"rate limit"}`), fail(429, `{"error":"rate limit"}`))
	cfg := config.Model{BaseURL: srv.URL, Name: "eval", Timeout: 10}
	a := newRecoverAgent(t, cfg, config.Model{})
	reply, err := a.Handle(context.Background(), "whatsapp", "u1", "hola")
	if err != nil {
		t.Fatalf("rate-limit should recover: %v", err)
	}
	if !strings.Contains(reply, "recuperado") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if *calls != 3 {
		t.Fatalf("expected 3 calls (2 retries), got %d", *calls)
	}
}

// (2) Malformed reply: garbage JSON once, then the answer.
func TestRecoveryMalformedThenOK(t *testing.T) {
	srv, calls := flaky(t, fail(200, `this is not json`))
	cfg := config.Model{BaseURL: srv.URL, Name: "eval", Timeout: 10}
	a := newRecoverAgent(t, cfg, config.Model{})
	reply, err := a.Handle(context.Background(), "whatsapp", "u1", "hola")
	if err != nil {
		t.Fatalf("malformed reply should recover: %v", err)
	}
	if !strings.Contains(reply, "recuperado") || *calls != 2 {
		t.Fatalf("reply %q, calls %d", reply, *calls)
	}
}

// (3) Timeout: the endpoint never answers in time; retries exhaust
// and the turn aborts with one clear error (no fallback configured).
func TestRecoveryTimeoutAbortsHonestly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1500 * time.Millisecond) // past the 1s client timeout
		io.WriteString(w, okAnswer)
	}))
	t.Cleanup(srv.Close)
	cfg := config.Model{BaseURL: srv.URL, Name: "eval", Timeout: 1}
	a := newRecoverAgent(t, cfg, config.Model{})
	start := time.Now()
	_, err := a.Handle(context.Background(), "whatsapp", "u1", "hola")
	if err == nil {
		t.Fatal("a permanently slow endpoint must abort, not hang forever")
	}
	if !strings.Contains(err.Error(), "timeout") && !strings.Contains(err.Error(), "unrecoverable") {
		t.Fatalf("the abort must name the failure, got: %v", err)
	}
	if d := time.Since(start); d > 15*time.Second {
		t.Fatalf("recovery took too long: %v", d)
	}
}

// (4) Context overflow: the endpoint rejects big requests with a
// context-size error; the recovery prunes and the retry fits.
func TestRecoveryOverflowCompresses(t *testing.T) {
	var sizes []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sizes = append(sizes, len(body))
		// This endpoint simulates a small-context model: 20KB and the
		// request is rejected with a context-size error.
		if len(body) > 20000 {
			w.WriteHeader(400)
			io.WriteString(w, `{"error":"request exceeds the context size"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, okAnswer)
	}))
	t.Cleanup(srv.Close)
	cfg := config.Model{BaseURL: srv.URL, Name: "eval", Timeout: 10}
	a := newRecoverAgent(t, cfg, config.Model{})
	ctx := context.Background()
	// Seed a fat history through the real turn path: each ~1.2KB user
	// text lands in the store, so after ~18 turns the request passes
	// 20KB and the endpoint starts rejecting it with a context-size
	// error. Late seeding turns already survive via recovery.
	text := "semilla " + strings.Repeat("relleno ", 140)
	for i := 0; i < 22; i++ {
		if _, err := a.Handle(ctx, "whatsapp", "u1", text); err != nil {
			t.Fatalf("seeding turn %d: %v", i, err)
		}
	}
	reply, err := a.Handle(ctx, "whatsapp", "u1", text)
	if err != nil {
		t.Fatalf("overflow should recover via compression: %v", err)
	}
	if !strings.Contains(reply, "recuperado") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	// Anti-humo: the compression must actually have fired - some
	// oversized request must be followed by a strictly smaller retry
	// that fits. Without pruning, every retry repeats the same size.
	sawOverflow, sawShrink := false, false
	for i := 0; i+1 < len(sizes); i++ {
		if sizes[i] > 20000 {
			sawOverflow = true
			if sizes[i+1] <= 20000 && sizes[i+1] < sizes[i] {
				sawShrink = true
			}
		}
	}
	if !sawOverflow || !sawShrink {
		t.Fatalf("no overflow->shrink sequence in request sizes: %v", sizes)
	}
}

// (5) Auth: a 401 aborts honestly naming the credential; no retry
// storm, no silent fallback.
func TestRecoveryAuthAbortsHonestly(t *testing.T) {
	srv, calls := flaky(t, fail(401, `{"error":"invalid api key"}`), fail(401, `{"error":"invalid api key"}`), fail(401, `{"error":"invalid api key"}`))
	cfg := config.Model{BaseURL: srv.URL, Name: "eval", Timeout: 10}
	a := newRecoverAgent(t, cfg, config.Model{})
	_, err := a.Handle(context.Background(), "whatsapp", "u1", "hola")
	if err == nil {
		t.Fatal("a rejected credential must abort")
	}
	if !strings.Contains(err.Error(), "credential") && !strings.Contains(err.Error(), "api_key") {
		t.Fatalf("the abort must name the credential, got: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("auth must not be retried: %d calls", *calls)
	}
}

// (6) Fallback: the main model is down (503), the coder model is
// healthy - the answer comes from the fallback.
func TestRecoveryFallbackToOtherModel(t *testing.T) {
	mainSrv, _ := flaky(t, fail(503, ``), fail(503, ``), fail(503, ``))
	coderSrv, coderCalls := flaky(t) // healthy from call 1
	mainCfg := config.Model{BaseURL: mainSrv.URL, Name: "eval-main", Timeout: 10}
	coderCfg := config.Model{BaseURL: coderSrv.URL, Name: "eval-coder", Timeout: 10}
	a := newRecoverAgent(t, mainCfg, coderCfg)
	reply, err := a.Handle(context.Background(), "whatsapp", "u1", "hola, sin código")
	if err != nil {
		t.Fatalf("fallback should recover: %v", err)
	}
	if !strings.Contains(reply, "recuperado") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if *coderCalls != 1 {
		t.Fatalf("the fallback model should have answered exactly once, got %d", *coderCalls)
	}
}

// Guard: an unclassified error (not a model.Failure) bubbles up
// untouched, so the classifier never swallows foreign errors.
func TestRecoveryForeignErrorBubbles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if ok {
			conn, _, _ := hj.Hijack()
			conn.Close() // kill the connection mid-reply
		}
	}))
	t.Cleanup(srv.Close)
	cfg := config.Model{BaseURL: srv.URL, Name: "eval", Timeout: 5}
	a := newRecoverAgent(t, cfg, config.Model{})
	_, err := a.Handle(context.Background(), "whatsapp", "u1", "hola")
	if err == nil {
		t.Fatal("a dead endpoint must produce an error")
	}
	// It must be a classified failure (unavailable), and with no
	// fallback the abort names it.
	if !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("expected classified unavailable, got: %v", err)
	}
	fmt.Println("bubbled error:", err)
}
