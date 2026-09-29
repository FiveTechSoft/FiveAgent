// Package web is the agent's browser (stage 23, simple-first cut):
// fetch a page over HTTP, present it as a numbered list of interactive
// elements (automation by structure, never pixels), and act by id -
// fill, click, submit. No JavaScript: pages that need it are the
// Playwright upgrade path documented in the roadmap.
//
// Safety by design: every form SUBMIT is gated behind an explicit
// confirm step (a pending token the model must request after asking
// the user), password values never reach logs, results or the model
// beyond a redacted marker, and every action is audit-logged.
package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

const (
	// maxPageBytes caps one fetch so a huge page cannot flood memory
	// or the model context.
	maxPageBytes = 512 * 1024
	// maxListElements caps the numbered list presented to the model.
	maxListElements = 60
	// maxTextExcerpt caps the visible-text excerpt in a page listing.
	maxTextExcerpt = 1500
)

// Element is one interactive element on the current page.
type Element struct {
	ID    int    `json:"id"`
	Kind  string `json:"kind"`  // link, input, textarea, select, button
	Label string `json:"label"` // text, placeholder, name or aria-label
	Name  string `json:"name,omitempty"`
	Type  string `json:"type,omitempty"` // input type: text, password, submit, ...
	Href  string `json:"href,omitempty"` // resolved absolute URL for links
	Form  int    `json:"form,omitempty"` // owning form index (1-based), 0 = none
}

// form is one parsed HTML form.
type form struct {
	index  int
	action string // resolved absolute URL
	method string // get or post
}

// pendingSubmit is a gated form submission awaiting confirmation.
type pendingSubmit struct {
	token   string
	summary string
	method  string
	url     string
	body    url.Values
	created time.Time
}

// Session is one user's browsing state: current page, filled values
// and the gated pending submit.
type Session struct {
	mu       sync.Mutex
	http     *http.Client
	audit    func(string) // receives one audit line per action
	page     string       // current page URL
	elems    []Element
	forms    []form
	fills    map[int]string // element id -> filled value
	pending  *pendingSubmit
	tokenSeq int
}

// NewSession builds a session. audit receives one line per action
// (password values are redacted BEFORE this call); nil disables.
func NewSession(client *http.Client, audit func(string)) *Session {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Session{http: client, audit: audit, fills: map[int]string{}}
}

func (s *Session) logf(format string, args ...any) {
	if s.audit != nil {
		s.audit(fmt.Sprintf(format, args...))
	}
}

// Open fetches url and returns the numbered element listing.
func (s *Session) Open(ctx context.Context, rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("browse: %q is not an http(s) URL", rawURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "FiveAgent/0.1 (+https://github.com/FiveTechSoft/FiveAgent)")
	resp, err := s.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("browse: %v", err)
	}
	defer resp.Body.Close()
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		return "", fmt.Errorf("browse: %s returned %s, not an HTML page", rawURL, ct)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes))
	if err != nil {
		return "", err
	}
	finalURL := resp.Request.URL.String()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.logf("open %s", finalURL)
	return s.landLocked(finalURL, b)
}

// landLocked parses a response body as the new current page. The
// caller holds s.mu.
func (s *Session) landLocked(finalURL string, b []byte) (string, error) {
	s.page = finalURL
	s.forms = nil
	s.elems = nil
	s.fills = map[int]string{}
	s.pending = nil
	return s.parseLocked(b, finalURL)
}

// parseLocked walks the DOM and builds the numbered listing.
func (s *Session) parseLocked(b []byte, base string) (string, error) {
	doc, err := html.Parse(strings.NewReader(string(b)))
	if err != nil {
		return "", fmt.Errorf("browse: cannot parse the page: %v", err)
	}
	baseURL, _ := url.Parse(base)
	var title strings.Builder
	var text strings.Builder
	var out strings.Builder
	formIdx := 0
	elemID := 0
	inBody := false
	skipDepth := 0 // inside script/style

	var walk func(n *html.Node, curForm int)
	walk = func(n *html.Node, curForm int) {
		if n.Type == html.ElementNode {
			tag := n.Data
			switch tag {
			case "script", "style", "noscript":
				skipDepth++
				defer func() { skipDepth-- }()
			case "title":
				if n.FirstChild != nil {
					title.WriteString(strings.TrimSpace(n.FirstChild.Data))
				}
			case "body":
				inBody = true
			case "form":
				formIdx++
				f := form{index: formIdx, method: "get"}
				for _, a := range n.Attr {
					if a.Key == "action" {
						if ref, err := url.Parse(a.Val); err == nil {
							f.action = baseURL.ResolveReference(ref).String()
						}
					}
					if a.Key == "method" && strings.EqualFold(a.Val, "post") {
						f.method = "post"
					}
				}
				if f.action == "" {
					f.action = base
				}
				s.forms = append(s.forms, f)
				curForm = formIdx
			case "a":
				var href, label string
				for _, at := range n.Attr {
					if at.Key == "href" {
						if ref, err := url.Parse(at.Val); err == nil {
							href = baseURL.ResolveReference(ref).String()
						}
					}
				}
				label = strings.TrimSpace(innerText(n))
				if href != "" && elemID < maxListElements {
					elemID++
					s.elems = append(s.elems, Element{ID: elemID, Kind: "link", Label: label, Href: href})
					fmt.Fprintf(&out, "#%d link %q -> %s\n", elemID, label, href)
				}
				return // link text already captured
			case "input", "textarea", "select", "button":
				if elemID < maxListElements {
					e := Element{ID: elemID + 1, Kind: tag, Form: curForm}
					for _, at := range n.Attr {
						switch at.Key {
						case "type":
							e.Type = strings.ToLower(at.Val)
						case "name":
							e.Name = at.Val
						case "placeholder", "aria-label", "value":
							if e.Label == "" {
								e.Label = at.Val
							}
						}
					}
					if tag == "textarea" || tag == "select" || tag == "button" {
						if t := strings.TrimSpace(innerText(n)); t != "" && e.Label == "" {
							e.Label = t
						}
					}
					if tag == "button" && e.Type == "" {
						e.Type = "submit"
					}
					if tag == "input" && e.Type == "" {
						e.Type = "text"
					}
					if e.Type == "hidden" || e.Type == "submit" && tag == "input" {
						// hidden inputs are submitted but not listed;
						// submit inputs ARE listed as buttons.
						if e.Type == "hidden" {
							break
						}
					}
					elemID++
					s.elems = append(s.elems, e)
					fmt.Fprintf(&out, "#%d %s", elemID, tag)
					if e.Type != "" {
						fmt.Fprintf(&out, " type=%s", e.Type)
					}
					if e.Name != "" {
						fmt.Fprintf(&out, " name=%q", e.Name)
					}
					if e.Label != "" {
						fmt.Fprintf(&out, " label=%q", e.Label)
					}
					if curForm > 0 {
						fmt.Fprintf(&out, " (form %d)", curForm)
					}
					out.WriteByte('\n')
				}
			}
		}
		if n.Type == html.TextNode && inBody && skipDepth == 0 && text.Len() < maxTextExcerpt {
			if t := strings.Join(strings.Fields(n.Data), " "); t != "" {
				text.WriteString(t)
				text.WriteByte(' ')
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, curForm)
		}
	}
	walk(doc, 0)

	var head strings.Builder
	fmt.Fprintf(&head, "page: %s\ntitle: %s\n", s.page, title.String())
	if out.Len() == 0 {
		head.WriteString("no interactive elements found")
	} else {
		fmt.Fprintf(&head, "elements:\n%s", out.String())
	}
	if text.Len() > 0 {
		excerpt := strings.TrimSpace(text.String())
		if len(excerpt) > maxTextExcerpt {
			excerpt = excerpt[:maxTextExcerpt] + "..."
		}
		fmt.Fprintf(&head, "text: %s", excerpt)
	}
	return head.String(), nil
}

func innerText(n *html.Node) string {
	var b strings.Builder
	var w func(*html.Node)
	w = func(x *html.Node) {
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			w(c)
		}
	}
	w(n)
	return b.String()
}
