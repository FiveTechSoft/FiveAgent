package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Fill records a value for an input element. Password values are
// stored for the submit but reported and audited redacted.
func (s *Session) Fill(id int, value string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.elemLocked(id)
	if err != nil {
		return "", err
	}
	switch e.Kind {
	case "input", "textarea", "select":
	default:
		return "", fmt.Errorf("fill: #%d is a %s, not a text field", id, e.Kind)
	}
	s.fills[id] = value
	if e.Type == "password" {
		s.logf("fill #%d (password, redacted)", id)
		return fmt.Sprintf("filled #%d (password, value redacted)", id), nil
	}
	shown := value
	if len(shown) > 40 {
		shown = shown[:40] + "..."
	}
	s.logf("fill #%d %q", id, shown)
	return fmt.Sprintf("filled #%d with %q", id, shown), nil
}

// Click follows a link (GET) or starts a form submit (gated).
func (s *Session) Click(ctx context.Context, id int) (string, error) {
	s.mu.Lock()
	e, err := s.elemLocked(id)
	if err != nil {
		s.mu.Unlock()
		return "", err
	}
	kind, href, etype, eform := e.Kind, e.Href, e.Type, e.Form
	s.mu.Unlock()

	switch {
	case kind == "link":
		s.logf("click #%d link -> %s", id, href)
		return s.Open(ctx, href)
	case (kind == "button" || (kind == "input" && etype == "submit")) && eform > 0:
		return s.submit(ctx, eform, id)
	}
	return "", fmt.Errorf("click: #%d (%s) is not clickable here - links and submit buttons are", id, kind)
}

// submit builds the form request and gates it behind confirmation.
// The actual POST/GET only happens in Confirm.
func (s *Session) submit(ctx context.Context, formIdx, clickedID int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var f *form
	for i := range s.forms {
		if s.forms[i].index == formIdx {
			f = &s.forms[i]
			break
		}
	}
	if f == nil {
		return "", fmt.Errorf("submit: form %d is gone - browse the page again", formIdx)
	}
	body := url.Values{}
	var fields []string
	for id, v := range s.fills {
		e, err := s.elemLocked(id)
		if err != nil || e.Form != formIdx || e.Name == "" {
			continue
		}
		body.Set(e.Name, v)
		if e.Type == "password" {
			fields = append(fields, e.Name+"=<redacted>")
		} else {
			shown := v
			if len(shown) > 30 {
				shown = shown[:30] + "..."
			}
			fields = append(fields, fmt.Sprintf("%s=%q", e.Name, shown))
		}
	}
	var rnd [4]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(rnd[:])
	summary := fmt.Sprintf("%s %s with %s", strings.ToUpper(f.method), f.action, strings.Join(fields, ", "))
	if len(fields) == 0 {
		summary = fmt.Sprintf("%s %s with no filled fields", strings.ToUpper(f.method), f.action)
	}
	s.pending = &pendingSubmit{token: token, summary: summary, method: strings.ToUpper(f.method), url: f.action, body: body, created: time.Now()}
	s.logf("submit gated: form %d -> %s (token issued, clicked #%d)", formIdx, f.action, clickedID)
	return fmt.Sprintf("SUBMIT NEEDS USER CONFIRMATION. Ask the user, then call confirm with token %q.\nsummary: %s", token, summary), nil
}

// Confirm executes a gated submit. Only the exact pending token
// works; tokens expire after 10 minutes and are single-use.
func (s *Session) Confirm(ctx context.Context, token string) (string, error) {
	s.mu.Lock()
	p := s.pending
	if p == nil || p.token != token {
		s.mu.Unlock()
		return "", fmt.Errorf("confirm: no pending submission with that token - nothing was sent")
	}
	if time.Since(p.created) > 10*time.Minute {
		s.pending = nil
		s.mu.Unlock()
		return "", fmt.Errorf("confirm: the pending submission expired - nothing was sent")
	}
	s.pending = nil // single-use, whatever happens next
	s.mu.Unlock()

	var req *http.Request
	var err error
	if p.method == http.MethodGet {
		u, _ := url.Parse(p.url)
		u.RawQuery = p.body.Encode()
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	} else {
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, p.url, strings.NewReader(p.body.Encode()))
		if err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "FiveAgent/0.1 (+https://github.com/FiveTechSoft/FiveAgent)")
	s.logf("submit CONFIRMED -> %s", p.url)
	resp, err := s.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("submit: %v", err)
	}
	defer resp.Body.Close()
	result := fmt.Sprintf("submitted: %s (HTTP %d)", p.url, resp.StatusCode)
	// Land on the response body itself, like a real browser rendering
	// the POST response - never re-request it.
	if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "text/html") {
		b, rerr := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes))
		if rerr == nil {
			s.mu.Lock()
			listing, lerr := s.landLocked(resp.Request.URL.String(), b)
			s.mu.Unlock()
			if lerr == nil {
				return result + "\n" + listing, nil
			}
		}
	}
	return result, nil
}

func (s *Session) elemLocked(id int) (Element, error) {
	for _, e := range s.elems {
		if e.ID == id {
			return e, nil
		}
	}
	return Element{}, fmt.Errorf("no element #%d on the current page - browse again to refresh the list", id)
}
