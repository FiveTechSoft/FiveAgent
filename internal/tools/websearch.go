package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// SearchResult is one hit from a web search provider.
type SearchResult struct {
	Title   string
	URL     string
	Snippet string
}

// SearchProvider is a web search backend. Implementations must be safe
// for concurrent use.
type SearchProvider interface {
	// Name identifies the provider ("duckduckgo", "brave").
	Name() string
	// Search returns up to max results for query.
	Search(ctx context.Context, query string, max int) ([]SearchResult, error)
}

// WebSearch is the agent's web_search tool. The model asks for a query
// and gets back a numbered list of titles, URLs and snippets.
type WebSearch struct {
	P SearchProvider
	// MaxResults caps the per-call result count. Default 5, hard cap 10.
	MaxResults int
	// Timeout caps one search round-trip. Default 15s.
	Timeout time.Duration
}

// Name implements Tool.
func (WebSearch) Name() string { return "web_search" }

// Description implements Tool.
func (WebSearch) Description() string {
	return "Search the public web. Returns titles, URLs and short snippets. " +
		"Use it for current facts (news, weather, prices, releases) and for anything you are not sure about. " +
		"Prefer it over guessing."
}

// Parameters implements Tool.
func (WebSearch) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "The search query, in the user's language."
			},
			"count": {
				"type": "integer",
				"description": "Number of results, 1-10. Default 5."
			}
		},
		"required": ["query"]
	}`)
}

// Execute implements Tool.
func (w WebSearch) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Query string `json:"query"`
		Count int    `json:"count"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	a.Query = strings.TrimSpace(a.Query)
	if a.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	max := w.MaxResults
	if max <= 0 {
		max = 5
	}
	if a.Count > 0 && a.Count < max {
		max = a.Count
	}
	if max > 10 {
		max = 10
	}
	if w.Timeout <= 0 {
		w.Timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, w.Timeout)
	defer cancel()
	res, err := w.P.Search(ctx, a.Query, max)
	if err != nil {
		log.Printf("websearch: %s query=%q: %v", w.P.Name(), a.Query, err)
		return "", fmt.Errorf("search failed: %w", err)
	}
	if len(res) == 0 {
		return fmt.Sprintf("No web results for %q.", a.Query), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Web results for %q:\n", a.Query)
	for i, r := range res {
		fmt.Fprintf(&b, "%d. %s\n   %s\n", i+1, r.Title, r.URL)
		if r.Snippet != "" {
			fmt.Fprintf(&b, "   %s\n", r.Snippet)
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// --- DuckDuckGo (default, no API key) ---

// DuckDuckGo searches via the public HTML endpoint. It needs no API key,
// which makes it the default; heavy use may get rate-limited, in which
// case Brave is the upgrade path.
type DuckDuckGo struct {
	// BaseURL is overridable for tests; default https://html.duckduckgo.com/html/
	BaseURL string
	Client  *http.Client
}

// Name implements SearchProvider.
func (DuckDuckGo) Name() string { return "duckduckgo" }

// Search implements SearchProvider.
func (d DuckDuckGo) Search(ctx context.Context, query string, max int) ([]SearchResult, error) {
	base := d.BaseURL
	if base == "" {
		base = "https://html.duckduckgo.com/html/"
	}
	u := base + "?q=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "FiveAgent/1.0 (+https://github.com/FiveTechSoft/FiveAgent)")
	c := d.Client
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("duckduckgo: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	return parseDDGHTML(body, max)
}

// parseDDGHTML extracts results from the DuckDuckGo HTML endpoint: each
// result is an <a class="result__a"> (title + redirect URL in the uddg
// parameter) followed by an <a class="result__snippet"> (the snippet).
func parseDDGHTML(b []byte, max int) ([]SearchResult, error) {
	doc, err := html.Parse(strings.NewReader(string(b)))
	if err != nil {
		return nil, fmt.Errorf("duckduckgo: parse: %w", err)
	}
	var out []SearchResult
	var cur *SearchResult
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if len(out) >= max {
			return
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			cls := attr(n, "class")
			switch {
			case strings.Contains(cls, "result__a"):
				u := decodeDDGRedirect(attr(n, "href"))
				if u != "" {
					out = append(out, SearchResult{Title: strings.TrimSpace(text(n)), URL: u})
					cur = &out[len(out)-1]
				}
			case strings.Contains(cls, "result__snippet"):
				if cur != nil && cur.Snippet == "" {
					cur.Snippet = strings.TrimSpace(text(n))
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out, nil
}

// decodeDDGRedirect unwraps DDG's //duckduckgo.com/l/?uddg=<url> redirect
// links; a plain absolute URL is returned as-is.
func decodeDDGRedirect(href string) string {
	if href == "" {
		return ""
	}
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}
	u, err := url.Parse(href)
	if err != nil || u.Host == "" {
		return ""
	}
	if v := u.Query().Get("uddg"); v != "" {
		return v
	}
	return href
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// text returns the concatenated text content of n and its descendants.
func text(n *html.Node) string {
	var b strings.Builder
	var walk func(m *html.Node)
	walk = func(m *html.Node) {
		if m.Type == html.TextNode {
			b.WriteString(m.Data)
		}
		for c := m.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

// --- Brave (needs an API key) ---

// Brave searches via the Brave Search API (JSON). It needs a subscription
// token; it is the reliable option when DuckDuckGo rate-limits.
type Brave struct {
	APIKey string
	// BaseURL is overridable for tests; default https://api.search.brave.com/res/v1/web/search
	BaseURL string
	Client  *http.Client
}

// Name implements SearchProvider.
func (Brave) Name() string { return "brave" }

// Search implements SearchProvider.
func (br Brave) Search(ctx context.Context, query string, max int) ([]SearchResult, error) {
	base := br.BaseURL
	if base == "" {
		base = "https://api.search.brave.com/res/v1/web/search"
	}
	u := fmt.Sprintf("%s?q=%s&count=%d", base, url.QueryEscape(query), max)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Subscription-Token", br.APIKey)
	req.Header.Set("Accept", "application/json")
	c := br.Client
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("brave: HTTP %d", resp.StatusCode)
	}
	var out struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("brave: decode: %w", err)
	}
	res := make([]SearchResult, 0, len(out.Web.Results))
	for _, r := range out.Web.Results {
		res = append(res, SearchResult{Title: r.Title, URL: r.URL, Snippet: r.Description})
		if len(res) >= max {
			break
		}
	}
	return res, nil
}
