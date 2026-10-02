package wasetup

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fake(t *testing.T, subscribed *bool, calls *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls = append(*calls, r.Method+" "+r.URL.Path+" auth="+r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/111" && r.Method == "GET":
			if r.Header.Get("Authorization") != "Bearer good" {
				w.WriteHeader(401)
				w.Write([]byte(`{"error":{"message":"Invalid OAuth access token"}}`))
				return
			}
			w.Write([]byte(`{"display_phone_number":"+1 555 000","verified_name":"Test"}`))
		case r.URL.Path == "/222/subscribed_apps" && r.Method == "POST":
			*subscribed = true
			w.Write([]byte(`{"success":true}`))
		case r.URL.Path == "/222/subscribed_apps" && r.Method == "GET":
			if *subscribed {
				w.Write([]byte(`{"data":[{"id":"app"}]}`))
				return
			}
			w.Write([]byte(`{"data":[]}`))
		case r.URL.Path == "/999/subscriptions" && r.Method == "POST":
			r.ParseForm()
			if r.Form.Get("object") != "whatsapp_business_account" || r.Form.Get("fields") != "messages" ||
				r.Form.Get("callback_url") != "https://example.test/webhook/whatsapp" || r.Form.Get("verify_token") != "vt" {
				w.WriteHeader(400)
				w.Write([]byte(`{"error":{"message":"bad form"}}`))
				return
			}
			w.Write([]byte(`{"success":true}`))
		default:
			w.WriteHeader(404)
		}
	}))
}

func TestWizardSteps(t *testing.T) {
	var subscribed bool
	var calls []string
	srv := fake(t, &subscribed, &calls)
	defer srv.Close()
	c := &Client{Base: srv.URL, Token: "good"}
	ctx := context.Background()

	if s := c.CheckPhone(ctx, "111"); !s.OK || !strings.Contains(s.Detail, "+1 555 000") {
		t.Fatalf("check: %+v", s)
	}
	if s := (&Client{Base: srv.URL, Token: "bad"}).CheckPhone(ctx, "111"); s.OK || !strings.Contains(s.Detail, "Invalid OAuth") {
		t.Fatalf("bad token must fail with the API message: %+v", s)
	}
	if s := c.SubscribeWABA(ctx, "222"); !s.OK || !subscribed {
		t.Fatalf("subscribe: %+v", s)
	}
	if s := c.RegisterWebhook(ctx, "999", "sec", "https://example.test/webhook/whatsapp", "vt"); !s.OK {
		t.Fatalf("webhook: %+v", s)
	}
	if got := calls[len(calls)-1]; !strings.Contains(got, "auth=Bearer 999|sec") {
		t.Fatalf("webhook must use the app access token, got %s", got)
	}
}

func TestSubscribeNeedsReadBack(t *testing.T) {
	// A POST that says success while the read-back lists nothing must not
	// be reported as done.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":true,"data":[]}`))
	}))
	defer srv.Close()
	if s := (&Client{Base: srv.URL, Token: "t"}).SubscribeWABA(context.Background(), "222"); s.OK {
		t.Fatalf("empty read-back reported as OK: %+v", s)
	}
}

func TestWebhookRejectsPlainHTTPAndMissingFields(t *testing.T) {
	c := &Client{Base: "http://127.0.0.1:1"}
	if s := c.RegisterWebhook(context.Background(), "a", "b", "http://x/y", "v"); s.OK || !strings.Contains(s.Detail, "https") {
		t.Fatalf("%+v", s)
	}
	if s := c.RegisterWebhook(context.Background(), "", "b", "https://x", "v"); s.OK {
		t.Fatalf("%+v", s)
	}
	if s := c.CheckPhone(context.Background(), ""); s.OK {
		t.Fatalf("%+v", s)
	}
}
