package model

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
)

// The classification table: every failure shape the client can meet,
// mapped to its kind at the source.
func TestClassifyStatus(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   FailureKind
	}{
		{429, `{"error":"rate limit reached"}`, FailureRateLimit},
		{401, `{"error":"invalid api key"}`, FailureAuth},
		{403, `{"error":"forbidden"}`, FailureAuth},
		{400, `{"error":"request exceeds the context size of the model"}`, FailureOverflow},
		{413, `{"error":"payload too large, too many tokens"}`, FailureOverflow},
		{500, `{"error":"context length exceeded"}`, FailureOverflow},
		{500, `{"error":"internal server error"}`, FailureUnavailable},
		{502, ``, FailureUnavailable},
		{503, ``, FailureUnavailable},
		{504, ``, FailureUnavailable},
		{400, `{"error":"bad request: unknown tool"}`, FailureUnknown},
		{418, ``, FailureUnknown},
	}
	for _, c := range cases {
		if got := classifyStatus(c.status, []byte(c.body)); got != c.want {
			t.Errorf("classifyStatus(%d, %q) = %s, want %s", c.status, c.body, got, c.want)
		}
	}
}

func TestClassifyNetError(t *testing.T) {
	if got := classifyNetError(context.DeadlineExceeded); got != FailureTimeout {
		t.Errorf("deadline = %s, want timeout", got)
	}
	if got := classifyNetError(&net.DNSError{IsTimeout: true}); got != FailureTimeout {
		t.Errorf("dns timeout = %s, want timeout", got)
	}
	if got := classifyNetError(&net.OpError{Op: "dial", Err: fmt.Errorf("connection refused")}); got != FailureUnavailable {
		t.Errorf("refused = %s, want unavailable", got)
	}
}

// A Failure must survive errors.As through wrapping - the recovery
// pipeline depends on it.
func TestFailureSurvivesWrapping(t *testing.T) {
	base := &Failure{Kind: FailureRateLimit, Status: 429, Err: fmt.Errorf("slow down")}
	wrapped := fmt.Errorf("agent: turn failed: %w", base)
	var f *Failure
	if !errors.As(wrapped, &f) || f.Kind != FailureRateLimit || f.Status != 429 {
		t.Fatalf("errors.As lost the failure: %v", f)
	}
}

// Every kind has a stable name - logs, audits and the both-models-failed
// error message all print it.
func TestFailureKindStrings(t *testing.T) {
	names := map[FailureKind]string{
		FailureUnknown:     "unknown",
		FailureTimeout:     "timeout",
		FailureRateLimit:   "rate-limit",
		FailureAuth:        "auth",
		FailureOverflow:    "context-overflow",
		FailureMalformed:   "malformed-reply",
		FailureUnavailable: "unavailable",
		FailureEmpty:       "empty-reply",
	}
	for k, want := range names {
		if got := k.String(); got != want {
			t.Errorf("FailureKind(%d).String() = %q, want %q", int(k), got, want)
		}
	}
}
