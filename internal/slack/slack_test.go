package slack

// Slack client tests (stage 27d): the whole flow against a fake
// endpoint - query params, JSON post body, auth header, and the
// Slack-specific ok:false-on-200 quirk surfacing as an error.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListChannels(t *testing.T) {
	var gotAuth, gotLimit string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotLimit = r.URL.Query().Get("limit")
		io.WriteString(w, `{"ok":true,"channels":[{"id":"C1","name":"general"},{"id":"C2","name":"random"}]}`)
	}))
	defer srv.Close()
	c := &Client{HTTP: &http.Client{Transport: fixedAuth{srv.Client().Transport}}, Root: srv.URL}
	chs, err := c.ListChannels(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(chs) != 2 || chs[0].Name != "general" {
		t.Fatalf("unexpected channels: %+v", chs)
	}
	if gotAuth != "Bearer xoxb-test" {
		t.Fatalf("missing auth header: %q", gotAuth)
	}
	if gotLimit != "50" {
		t.Fatalf("default limit: %q", gotLimit)
	}
}

func TestHistory(t *testing.T) {
	var gotChannel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotChannel = r.URL.Query().Get("channel")
		io.WriteString(w, `{"ok":true,"messages":[{"user":"U1","text":"hello","ts":"1727.001"}]}`)
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), Root: srv.URL}
	msgs, err := c.History(context.Background(), "C9", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Text != "hello" {
		t.Fatalf("unexpected messages: %+v", msgs)
	}
	if gotChannel != "C9" {
		t.Fatalf("channel param: %q", gotChannel)
	}
}

func TestPostMessage(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content type: %q", ct)
		}
		io.WriteString(w, `{"ok":true,"ts":"1727.002"}`)
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), Root: srv.URL}
	ts, err := c.PostMessage(context.Background(), "C1", "hi there")
	if err != nil {
		t.Fatal(err)
	}
	if ts != "1727.002" {
		t.Fatalf("ts: %q", ts)
	}
	if !strings.Contains(gotBody, `"channel":"C1"`) || !strings.Contains(gotBody, `"text":"hi there"`) {
		t.Fatalf("post body: %s", gotBody)
	}
}

// The Slack quirk: HTTP 200 with ok:false must surface as an error.
func TestOkFalseSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"ok":false,"error":"channel_not_found"}`)
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), Root: srv.URL}
	if _, err := c.ListChannels(context.Background(), 5); err == nil || !strings.Contains(err.Error(), "channel_not_found") {
		t.Fatalf("list should surface ok:false: %v", err)
	}
	if _, err := c.History(context.Background(), "Cx", 5); err == nil || !strings.Contains(err.Error(), "channel_not_found") {
		t.Fatalf("history should surface ok:false: %v", err)
	}
	if _, err := c.PostMessage(context.Background(), "Cx", "t"); err == nil || !strings.Contains(err.Error(), "channel_not_found") {
		t.Fatalf("post should surface ok:false: %v", err)
	}
}

func TestHTTPErrorsSurface(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), Root: srv.URL}
	if _, err := c.ListChannels(context.Background(), 5); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("http error should surface: %v", err)
	}
}

type fixedAuth struct{ base http.RoundTripper }

func (f fixedAuth) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set("Authorization", "Bearer xoxb-test")
	return f.base.RoundTrip(r)
}
