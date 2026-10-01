package model

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/config"
)

func TestNativeGenerationLimitAndTelemetry(t *testing.T) {
	for _, tc := range []struct {
		name, content, thinking, reason string
		calls                           bool
		limited                         bool
	}{
		{"empty length", "", "private trace", "length", false, true},
		{"partial length", "En", "trace", "length", false, true},
		{"tool length", "", "trace", "length", true, true},
		{"empty stop", "", "trace", "stop", false, false},
		{"tool stop", "", "", "stop", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/chat" {
					t.Errorf("route %s", r.URL.Path)
				}
				body, _ := io.ReadAll(r.Body)
				if strings.Contains(string(body), "private trace") {
					t.Error("thinking entered prompt")
				}
				tools := `[]`
				if tc.calls {
					tools = `[{"function":{"name":"probe","arguments":{}}}]`
				}
				fmt.Fprintf(w, `{"model":"fixture","done":true,"done_reason":%q,"message":{"role":"assistant","content":%q,"thinking":%q,"tool_calls":%s},"prompt_eval_count":3000,"eval_count":1096,"total_duration":10,"load_duration":1,"prompt_eval_duration":2,"eval_duration":7}`, tc.reason, tc.content, tc.thinking, tools)
			}))
			defer srv.Close()
			c := NewOpenAICompat(config.Model{Name: "fixture", BaseURL: srv.URL + "/v1", NumThread: 4})
			var observations []NativeObservation
			ctx := WithNativeObserver(WithModelPurpose(context.Background(), "fixture-attempt"), func(o NativeObservation) { observations = append(observations, o) })
			for i := 0; i < 2; i++ {
				ans, err := c.Chat(ctx, []Message{{Role: "user", Content: "fixture"}}, nil)
				var f *Failure
				if tc.limited {
					if !errors.As(err, &f) || f.Kind != FailureGenerationLimit {
						t.Fatalf("length not classified: %v", err)
					}
					if ans.Content != "" || len(ans.ToolCalls) > 0 {
						t.Fatal("truncated response escaped")
					}
				} else if err != nil {
					t.Fatal(err)
				}
			}
			if len(observations) != 2 {
				t.Fatalf("want both attempts, got %d", len(observations))
			}
			o := observations[0]
			if o.Model != "fixture" || o.Purpose != "fixture-attempt" || !o.Done || o.DoneReason != tc.reason || o.Thinking != tc.thinking || o.Content != tc.content || o.HTTPStatus != 200 || o.PromptEvalCount != 3000 || o.EvalCount != 1096 || o.TotalDuration != 10 || o.LoadDuration != 1 || o.PromptEvalDuration != 2 || o.EvalDuration != 7 {
				t.Fatalf("lost telemetry: %+v", o)
			}
			if o.RequestedNumCtx != nil || o.RequestedNumPredict != nil {
				t.Fatal("invented unset options")
			}
		})
	}
}

func TestNativeObservationErrorStatus(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		kind   FailureKind
	}{{429, `busy`, FailureRateLimit}, {413, `context size exceeded`, FailureOverflow}, {200, `not JSON`, FailureMalformed}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); io.WriteString(w, tc.body) }))
		c := NewOpenAICompat(config.Model{Name: "fixture", BaseURL: srv.URL + "/v1", NumThread: 4})
		var o NativeObservation
		_, err := c.Chat(WithNativeObserver(context.Background(), func(x NativeObservation) { o = x }), nil, nil)
		srv.Close()
		var f *Failure
		if !errors.As(err, &f) || f.Kind != tc.kind || o.HTTPStatus != tc.status || o.Error == "" {
			t.Fatalf("error telemetry: %+v %v", o, err)
		}
	}
}
