package model

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/config"
)

func TestNativeContextRouteAndRequestedTelemetry(t *testing.T) {
	for _, n := range []int{0, 8192} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				if r.URL.Path != "/v1/chat/completions" {
					t.Errorf("unset changed route: %s", r.URL.Path)
				}
				if _, ok := req["options"]; ok {
					t.Error("unset native options sent")
				}
				io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"fixture"}}]}`)
				return
			}
			if r.URL.Path != "/api/chat" {
				t.Errorf("ctx not native: %s", r.URL.Path)
			}
			opts := req["options"].(map[string]any)
			if opts["num_ctx"] != float64(n) {
				t.Error("capacity not sent")
			}
			if _, ok := opts["num_thread"]; ok {
				t.Error("invented threads")
			}
			io.WriteString(w, `{"model":"fixture","done":true,"done_reason":"stop","message":{"role":"assistant","content":"fixture"}}`)
		}))
		c := NewOpenAICompat(config.Model{Name: "fixture", BaseURL: srv.URL + "/v1", NumCtx: n})
		var o NativeObservation
		_, err := c.Chat(WithNativeObserver(context.Background(), func(x NativeObservation) { o = x }), nil, nil)
		srv.Close()
		if err != nil {
			t.Fatal(err)
		}
		if n > 0 && (o.RequestedNumCtx == nil || *o.RequestedNumCtx != n || o.RequestedNumPredict != nil) {
			t.Fatalf("requested option lost: %+v", o)
		}
	}
}
