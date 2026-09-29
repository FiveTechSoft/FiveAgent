package google

// Calendar client (stage 27b): same shape as the Gmail client -
// overridable base URL, OAuth-authorized HTTP client from the shared
// oauth package, read (events, free/busy) and write (create).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CalendarBase is the Calendar API root, overridable in tests.
var CalendarBase = "https://www.googleapis.com/calendar/v3"

// Calendar reads and writes calendar events.
type Calendar struct {
	HTTP *http.Client
	Base string // empty uses CalendarBase
}

func (c *Calendar) base() string {
	if c.Base != "" {
		return c.Base
	}
	return CalendarBase
}

// Event is one calendar entry.
type Event struct {
	ID        string   `json:"id"`
	Summary   string   `json:"summary"`
	Location  string   `json:"location,omitempty"`
	Start     string   `json:"start"`
	End       string   `json:"end"`
	Attendees []string `json:"attendees,omitempty"`
}

// ListEvents returns the user's events overlapping [from, to].
func (c *Calendar) ListEvents(ctx context.Context, from, to time.Time, maxResults int) ([]Event, error) {
	if maxResults <= 0 || maxResults > 50 {
		maxResults = 10
	}
	u := c.base() + "/calendars/primary/events?singleEvents=true&orderBy=startTime" +
		"&timeMin=" + url.QueryEscape(from.UTC().Format(time.RFC3339)) +
		"&timeMax=" + url.QueryEscape(to.UTC().Format(time.RFC3339)) +
		fmt.Sprintf("&maxResults=%d", maxResults)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("calendar: %s", resp.Status)
	}
	var parsed struct {
		Items []struct {
			ID       string `json:"id"`
			Summary  string `json:"summary"`
			Location string `json:"location"`
			Start    struct {
				DateTime string `json:"dateTime"`
				Date     string `json:"date"`
			} `json:"start"`
			End struct {
				DateTime string `json:"dateTime"`
				Date     string `json:"date"`
			} `json:"end"`
			Attendees []struct {
				Email string `json:"email"`
			} `json:"attendees"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	var out []Event
	for _, it := range parsed.Items {
		ev := Event{ID: it.ID, Summary: it.Summary, Location: it.Location,
			Start: firstNonEmpty(it.Start.DateTime, it.Start.Date),
			End:   firstNonEmpty(it.End.DateTime, it.End.Date)}
		for _, a := range it.Attendees {
			ev.Attendees = append(ev.Attendees, a.Email)
		}
		out = append(out, ev)
	}
	return out, nil
}

// CreateEvent inserts one event and returns its id.
func (c *Calendar) CreateEvent(ctx context.Context, ev Event, description string) (string, error) {
	body := map[string]any{
		"summary":     ev.Summary,
		"location":    ev.Location,
		"description": description,
		"start":       map[string]string{"dateTime": ev.Start},
		"end":         map[string]string{"dateTime": ev.End},
	}
	if len(ev.Attendees) > 0 {
		var ats []map[string]string
		for _, a := range ev.Attendees {
			ats = append(ats, map[string]string{"email": a})
		}
		body["attendees"] = ats
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+"/calendars/primary/events", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("calendar: %s", resp.Status)
	}
	var parsed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return "", err
	}
	return parsed.ID, nil
}

// FreeBusy reports which of the given calendars are busy in [from, to].
func (c *Calendar) FreeBusy(ctx context.Context, from, to time.Time, calendars ...string) (map[string][][2]string, error) {
	if len(calendars) == 0 {
		calendars = []string{"primary"}
	}
	var items []map[string]string
	for _, cal := range calendars {
		items = append(items, map[string]string{"id": cal})
	}
	raw, err := json.Marshal(map[string]any{
		"timeMin": from.UTC().Format(time.RFC3339),
		"timeMax": to.UTC().Format(time.RFC3339),
		"items":   items,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+"/freeBusy", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("calendar: %s", resp.Status)
	}
	var parsed struct {
		Calendars map[string]struct {
			Busy []struct {
				Start string `json:"start"`
				End   string `json:"end"`
			} `json:"busy"`
		} `json:"calendars"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, err
	}
	result := map[string][][2]string{}
	for cal, data := range parsed.Calendars {
		for _, b := range data.Busy {
			result[cal] = append(result[cal], [2]string{b.Start, b.End})
		}
	}
	return result, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
