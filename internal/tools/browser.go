package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/web"
)

// Browser gives the model a numbered-element web page view and
// by-id actions (stage 23). One browsing session per user; every
// action is audit-logged per user, with password values redacted
// inside the web package BEFORE they reach the log.
type Browser struct {
	// AuditDir receives one <channel>-<user>.log file per user;
	// empty disables the file audit (actions still work).
	AuditDir string

	mu    sync.Mutex
	sess  map[string]*web.Session
	audit map[string]func(string)
}

func (b *Browser) session(ctx context.Context) (*web.Session, error) {
	channel, userID := RequestInfo(ctx)
	if userID == "" {
		return nil, errors.New("browser: no user in the request context")
	}
	key := channel + "-" + userID
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sess == nil {
		b.sess = map[string]*web.Session{}
		b.audit = map[string]func(string){}
	}
	if s, ok := b.sess[key]; ok {
		return s, nil
	}
	audit := func(line string) {}
	if b.AuditDir != "" {
		keySafe := strings.Map(func(r rune) rune {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
				return r
			}
			return '_'
		}, key)
		path := filepath.Join(b.AuditDir, keySafe+".log")
		audit = func(line string) {
			if err := os.MkdirAll(b.AuditDir, 0o755); err != nil {
				return
			}
			f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if err != nil {
				return
			}
			defer f.Close()
			fmt.Fprintf(f, "%s %s\n", time.Now().Format(time.RFC3339), line)
		}
	}
	s := web.NewSession(nil, audit)
	b.sess[key] = s
	return s, nil
}

// BrowsePage fetches a URL and returns its numbered element listing.
type BrowsePage struct{ B *Browser }

func (t BrowsePage) Name() string { return "browse_page" }
func (t BrowsePage) Description() string {
	return "Open a web page and see it as a numbered list of elements (links, inputs, buttons) plus a text excerpt. Act on elements by id with browser_act. The page has no JavaScript: if a site needs it, say so instead of guessing."
}
func (t BrowsePage) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","description":"http(s) URL to open"}},"required":["url"]}`)
}
func (t BrowsePage) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	s, err := t.B.session(ctx)
	if err != nil {
		return "", err
	}
	bctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	return s.Open(bctx, a.URL)
}

// BrowserAct fills, clicks, submits and confirms on the current page.
type BrowserAct struct{ B *Browser }

func (t BrowserAct) Name() string { return "browser_act" }
func (t BrowserAct) Description() string {
	return "Act on the current page by element id: fill #id value (text fields), click #id (links and submit buttons), confirm token (executes a gated submit). Submitting a form NEVER sends data right away: you get a token and a summary, you ask the user, and only then confirm. Every action is audit-logged; password values are redacted everywhere."
}
func (t BrowserAct) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["fill","click","confirm"]},"id":{"type":"integer","description":"element id for fill/click"},"value":{"type":"string","description":"text for fill"},"token":{"type":"string","description":"pending-submit token for confirm"}},"required":["action"]}`)
}
func (t BrowserAct) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Action string `json:"action"`
		ID     int    `json:"id"`
		Value  string `json:"value"`
		Token  string `json:"token"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	s, err := t.B.session(ctx)
	if err != nil {
		return "", err
	}
	bctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	switch a.Action {
	case "fill":
		if a.ID == 0 {
			return "", errors.New("browser_act: fill needs an element id")
		}
		return s.Fill(a.ID, a.Value)
	case "click":
		if a.ID == 0 {
			return "", errors.New("browser_act: click needs an element id")
		}
		out, err := s.Click(bctx, a.ID)
		if err == nil {
			log.Printf("browser: click #%d done", a.ID)
		}
		return out, err
	case "confirm":
		if a.Token == "" {
			return "", errors.New("browser_act: confirm needs the pending-submit token")
		}
		return s.Confirm(bctx, a.Token)
	}
	return "", fmt.Errorf("browser_act: unknown action %q (fill, click, confirm)", a.Action)
}
