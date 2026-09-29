package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/google"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

func fakeGmailServer(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/gmail/v1/users/me/messages":
			json.NewEncoder(rw).Encode(map[string]any{"messages": []map[string]string{{"id": "m1"}}})
		case r.Method == http.MethodGet:
			json.NewEncoder(rw).Encode(map[string]any{
				"id": "m1", "threadId": "t1", "snippet": "te paso la factura",
				"payload": map[string]any{"headers": []map[string]string{
					{"name": "Subject", "value": "factura"},
					{"name": "From", "value": "proveedor@example.com"},
					{"name": "Date", "value": "ayer"},
				}},
			})
		default:
			json.NewEncoder(rw).Encode(map[string]string{"id": "sent-1"})
		}
	}))
}

func TestGmailSearchTool(t *testing.T) {
	srv := fakeGmailServer(t)
	t.Cleanup(srv.Close)
	tool := tools.GmailSearch{Client: func(context.Context) (*google.Gmail, error) {
		return &google.Gmail{HTTP: http.DefaultClient, Base: srv.URL}, nil
	}}
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"query": "factura"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "factura") || !strings.Contains(out, "proveedor@example.com") || !strings.Contains(out, "id:m1") {
		t.Fatalf("output %q", out)
	}
}

func TestGmailSendTool(t *testing.T) {
	srv := fakeGmailServer(t)
	t.Cleanup(srv.Close)
	tool := tools.GmailSend{Client: func(context.Context) (*google.Gmail, error) {
		return &google.Gmail{HTTP: http.DefaultClient, Base: srv.URL}, nil
	}}
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"to": "ana@example.com", "subject": "hola", "body": "texto"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "sent") {
		t.Fatalf("output %q", out)
	}
}

// Without a connected account the tool must say so, never return an
// empty list that reads as "no mail".
func TestGmailToolNotConnectedHonest(t *testing.T) {
	notConnected := func(context.Context) (*google.Gmail, error) {
		return nil, errors.New("oauth: gmail not connected - open the OAuth link")
	}
	if _, err := (tools.GmailSearch{Client: notConnected}).Execute(context.Background(), json.RawMessage(`{"query": "x"}`)); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("search: %v", err)
	}
	if _, err := (tools.GmailSend{Client: notConnected}).Execute(context.Background(), json.RawMessage(`{"to":"a@b.c","subject":"s","body":"b"}`)); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("send: %v", err)
	}
}
