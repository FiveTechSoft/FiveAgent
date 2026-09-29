package google

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeCalendar struct {
	lastBody     []byte
	lastEndpoint string
	authFailures int
}

func (f *fakeCalendar) handler() http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			f.authFailures++
		}
		rw.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/calendars/primary/events":
			json.NewEncoder(rw).Encode(map[string]any{"items": []map[string]any{{
				"id": "e1", "summary": "comida con Ana", "location": "Casa Pepe",
				"start":     map[string]string{"dateTime": "2026-09-30T13:00:00+02:00"},
				"end":       map[string]string{"dateTime": "2026-09-30T14:30:00+02:00"},
				"attendees": []map[string]string{{"email": "ana@example.com"}},
			}}})
		case r.Method == http.MethodPost && r.URL.Path == "/calendars/primary/events":
			f.lastEndpoint = r.URL.Path
			f.lastBody, _ = io.ReadAll(r.Body)
			json.NewEncoder(rw).Encode(map[string]string{"id": "new-1"})
		case r.Method == http.MethodPost && r.URL.Path == "/freeBusy":
			json.NewEncoder(rw).Encode(map[string]any{"calendars": map[string]any{
				"primary": map[string]any{"busy": []map[string]string{
					{"start": "2026-09-30T13:00:00Z", "end": "2026-09-30T14:30:00Z"},
				}},
			}})
		default:
			rw.Write([]byte(`{}`))
		}
	})
}

func TestCalendarListEvents(t *testing.T) {
	fc := &fakeCalendar{}
	srv := httptest.NewServer(fc.handler())
	t.Cleanup(srv.Close)
	c := &Calendar{HTTP: bearerClient("test-token"), Base: srv.URL}
	from := time.Date(2026, 9, 30, 0, 0, 0, 0, time.Local)
	to := from.Add(24 * time.Hour)
	events, err := c.ListEvents(context.Background(), from, to, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events: %d", len(events))
	}
	ev := events[0]
	if ev.Summary != "comida con Ana" || ev.Location != "Casa Pepe" || ev.Start == "" || len(ev.Attendees) != 1 {
		t.Fatalf("event %+v", ev)
	}
	if fc.authFailures > 0 {
		t.Fatalf("unauthenticated calls: %d", fc.authFailures)
	}
}

func TestCalendarCreateEvent(t *testing.T) {
	fc := &fakeCalendar{}
	srv := httptest.NewServer(fc.handler())
	t.Cleanup(srv.Close)
	c := &Calendar{HTTP: bearerClient("test-token"), Base: srv.URL}
	id, err := c.CreateEvent(context.Background(), Event{
		Summary: "tenis", Location: "Buena Vista",
		Start: "2026-10-01T17:00:00+02:00", End: "2026-10-01T18:00:00+02:00",
		Attendees: []string{"sam@example.com"},
	}, "partido semanal")
	if err != nil {
		t.Fatal(err)
	}
	if id != "new-1" {
		t.Fatalf("id %q", id)
	}
	var body map[string]any
	if err := json.Unmarshal(fc.lastBody, &body); err != nil {
		t.Fatal(err)
	}
	if body["summary"] != "tenis" || body["location"] != "Buena Vista" || body["description"] != "partido semanal" {
		t.Fatalf("body %v", body)
	}
	start, _ := body["start"].(map[string]any)
	if start["dateTime"] != "2026-10-01T17:00:00+02:00" {
		t.Fatalf("start %v", start)
	}
	ats, _ := body["attendees"].([]any)
	if len(ats) != 1 || ats[0].(map[string]any)["email"] != "sam@example.com" {
		t.Fatalf("attendees %v", ats)
	}
}

func TestCalendarFreeBusy(t *testing.T) {
	fc := &fakeCalendar{}
	srv := httptest.NewServer(fc.handler())
	t.Cleanup(srv.Close)
	c := &Calendar{HTTP: bearerClient("test-token"), Base: srv.URL}
	from := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	busy, err := c.FreeBusy(context.Background(), from, from.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	slots := busy["primary"]
	if len(slots) != 1 || !strings.HasPrefix(slots[0][0], "2026-09-30T13:00") {
		t.Fatalf("busy %v", busy)
	}
}
