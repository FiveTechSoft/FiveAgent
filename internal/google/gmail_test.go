package google

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeGmail serves the Gmail API endpoints the client touches and
// records the last send body so tests can decode the RFC822 payload.
type fakeGmail struct {
	lastSendBody []byte
	lastEndpoint string
	authFailures int
}

func (f *fakeGmail) handler() http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			f.authFailures++
		}
		rw.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/gmail/v1/users/me/messages":
			json.NewEncoder(rw).Encode(map[string]any{
				"messages": []map[string]string{{"id": "m1"}},
			})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/gmail/v1/users/me/messages/"):
			json.NewEncoder(rw).Encode(map[string]any{
				"id": "m1", "threadId": "t1", "snippet": "quedamos manana?",
				"payload": map[string]any{"headers": []map[string]string{
					{"name": "Subject", "value": "comida"},
					{"name": "From", "value": "ana@example.com"},
					{"name": "Date", "value": "Tue, 29 Sep 2026 08:00:00 +0200"},
				}},
			})
		case r.Method == http.MethodPost:
			f.lastEndpoint = r.URL.Path
			f.lastSendBody, _ = io.ReadAll(r.Body)
			json.NewEncoder(rw).Encode(map[string]string{"id": "sent-1"})
		default:
			rw.Write([]byte(`{}`))
		}
	})
}

func TestGmailSearch(t *testing.T) {
	fg := &fakeGmail{}
	srv := httptest.NewServer(fg.handler())
	t.Cleanup(srv.Close)
	g := &Gmail{HTTP: bearerClient("test-token"), Base: srv.URL}
	msgs, err := g.Search(context.Background(), "from:ana", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("messages: %d", len(msgs))
	}
	m := msgs[0]
	if m.Subject != "comida" || m.From != "ana@example.com" || m.ThreadID != "t1" || m.Snippet == "" {
		t.Fatalf("message %+v", m)
	}
	if fg.authFailures > 0 {
		t.Fatalf("unauthenticated calls: %d", fg.authFailures)
	}
}

func TestGmailSend(t *testing.T) {
	fg := &fakeGmail{}
	srv := httptest.NewServer(fg.handler())
	t.Cleanup(srv.Close)
	g := &Gmail{HTTP: bearerClient("test-token"), Base: srv.URL}
	id, err := g.Send(context.Background(), "me", "ana@example.com", "manana", "a las 12 nos vemos")
	if err != nil {
		t.Fatal(err)
	}
	if id != "sent-1" {
		t.Fatalf("id %q", id)
	}
	if fg.lastEndpoint != "/gmail/v1/users/me/messages/send" {
		t.Fatalf("endpoint %s", fg.lastEndpoint)
	}
	var payload struct {
		Raw string `json:"raw"`
	}
	if err := json.Unmarshal(fg.lastSendBody, &payload); err != nil {
		t.Fatal(err)
	}
	raw, err := base64.URLEncoding.DecodeString(payload.Raw)
	if err != nil {
		t.Fatal(err)
	}
	msg := string(raw)
	for _, want := range []string{"To: ana@example.com", "Subject: manana", "a las 12 nos vemos"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("RFC822 missing %q: %q", want, msg)
		}
	}
}

func TestGmailCreateDraft(t *testing.T) {
	fg := &fakeGmail{}
	srv := httptest.NewServer(fg.handler())
	t.Cleanup(srv.Close)
	g := &Gmail{HTTP: bearerClient("test-token"), Base: srv.URL}
	if _, err := g.CreateDraft(context.Background(), "me", "a@b.c", "s", "b"); err != nil {
		t.Fatal(err)
	}
	if fg.lastEndpoint != "/gmail/v1/users/me/drafts" {
		t.Fatalf("endpoint %s", fg.lastEndpoint)
	}
	var payload struct {
		Message struct {
			Raw string `json:"raw"`
		} `json:"message"`
	}
	if err := json.Unmarshal(fg.lastSendBody, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Message.Raw == "" {
		t.Fatal("draft missing message.raw")
	}
}

func bearerClient(token string) *http.Client {
	return &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.Header.Set("Authorization", "Bearer "+token)
		return http.DefaultTransport.RoundTrip(r)
	})}
}

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
