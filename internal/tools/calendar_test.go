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

func fakeCalendarServer(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			json.NewEncoder(rw).Encode(map[string]any{"items": []map[string]any{{
				"id": "e1", "summary": "comida con Ana", "location": "Casa Pepe",
				"start": map[string]string{"dateTime": "2026-09-30T13:00:00+02:00"},
				"end":   map[string]string{"dateTime": "2026-09-30T14:30:00+02:00"},
			}}})
			return
		}
		json.NewEncoder(rw).Encode(map[string]string{"id": "new-1"})
	}))
}

func TestCalendarListTool(t *testing.T) {
	srv := fakeCalendarServer(t)
	t.Cleanup(srv.Close)
	tool := tools.CalendarList{Client: func(context.Context) (*google.Calendar, error) {
		return &google.Calendar{HTTP: http.DefaultClient, Base: srv.URL}, nil
	}}
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"from": "2026-09-30", "to": "2026-09-30"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "comida con Ana") || !strings.Contains(out, "Casa Pepe") || !strings.Contains(out, "id:e1") {
		t.Fatalf("output %q", out)
	}
}

func TestCalendarCreateTool(t *testing.T) {
	srv := fakeCalendarServer(t)
	t.Cleanup(srv.Close)
	tool := tools.CalendarCreate{Client: func(context.Context) (*google.Calendar, error) {
		return &google.Calendar{HTTP: http.DefaultClient, Base: srv.URL}, nil
	}}
	out, err := tool.Execute(context.Background(), json.RawMessage(`{
		"summary": "tenis", "start": "2026-10-01T17:00:00+02:00", "end": "2026-10-01T18:00:00+02:00"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "created") {
		t.Fatalf("output %q", out)
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"summary": "x", "start": "ayer", "end": "2026-10-01T18:00:00+02:00"}`)); err == nil {
		t.Fatal("expected RFC3339 validation error")
	}
}

func TestCalendarToolNotConnectedHonest(t *testing.T) {
	notConnected := func(context.Context) (*google.Calendar, error) {
		return nil, errors.New("oauth: calendar not connected - open the OAuth link")
	}
	if _, err := (tools.CalendarList{Client: notConnected}).Execute(context.Background(), json.RawMessage(`{"from": "2026-09-30", "to": "2026-10-01"}`)); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("list: %v", err)
	}
}
