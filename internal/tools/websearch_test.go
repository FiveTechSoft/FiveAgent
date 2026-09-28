package tools_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

const ddgPage = `<!DOCTYPE html><html><body>
<div class="result">
  <a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fharbour&amp;rut=abc">Harbour project home</a>
  <a class="result__snippet">Harbour is an open source xBase compiler.</a>
</div>
<div class="result">
  <a class="result__a" href="https://example.org/direct">Direct link title</a>
  <a class="result__snippet">A direct https link, no redirect.</a>
</div>
</body></html>`

const bravePage = `{"web":{"results":[
  {"title":"Harbour project home","url":"https://example.com/harbour","description":"Harbour is an open source xBase compiler."},
  {"title":"Second hit","url":"https://example.org/two","description":"Second snippet."}
]}}`

func TestDuckDuckGoParsesResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") == "" {
			t.Error("query param missing")
		}
		w.Write([]byte(ddgPage))
	}))
	defer srv.Close()
	p := tools.DuckDuckGo{BaseURL: srv.URL, Client: srv.Client()}
	res, err := p.Search(context.Background(), "harbour compiler", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("got %d results, want 2", len(res))
	}
	if res[0].Title != "Harbour project home" {
		t.Errorf("title: %q", res[0].Title)
	}
	// The uddg redirect must be unwrapped to the real URL.
	if res[0].URL != "https://example.com/harbour" {
		t.Errorf("redirect not unwrapped: %q", res[0].URL)
	}
	if res[0].Snippet != "Harbour is an open source xBase compiler." {
		t.Errorf("snippet: %q", res[0].Snippet)
	}
	if res[1].URL != "https://example.org/direct" {
		t.Errorf("direct link kept: %q", res[1].URL)
	}
}

func TestBraveParsesResultsAndSendsKey(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Subscription-Token")
		w.Write([]byte(bravePage))
	}))
	defer srv.Close()
	p := tools.Brave{APIKey: "test-key", BaseURL: srv.URL, Client: srv.Client()}
	res, err := p.Search(context.Background(), "harbour", 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotKey != "test-key" {
		t.Errorf("api key header: %q", gotKey)
	}
	if len(res) != 1 || res[0].URL != "https://example.com/harbour" {
		t.Fatalf("results: %+v", res)
	}
}

type fakeProvider struct {
	called int
	res    []tools.SearchResult
	err    error
}

func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) Search(_ context.Context, q string, max int) ([]tools.SearchResult, error) {
	f.called++
	if max > len(f.res) {
		max = len(f.res)
	}
	return f.res[:max], f.err
}

func TestWebSearchToolFormatsAndClamps(t *testing.T) {
	fp := &fakeProvider{res: []tools.SearchResult{
		{Title: "One", URL: "https://one.example", Snippet: "first"},
		{Title: "Two", URL: "https://two.example", Snippet: "second"},
		{Title: "Three", URL: "https://three.example"},
	}}
	tool := tools.WebSearch{P: fp, MaxResults: 5}
	args, _ := json.Marshal(map[string]any{"query": "test", "count": 2})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "1. One") || !strings.Contains(out, "2. Two") || strings.Contains(out, "Three") {
		t.Errorf("output:\n%s", out)
	}
	if !strings.Contains(out, "https://one.example") {
		t.Errorf("URL missing:\n%s", out)
	}

	// Empty results are a sentence, not an error.
	fp.res = nil
	out, err = tool.Execute(context.Background(), []byte(`{"query":"nada"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "No web results") {
		t.Errorf("empty output: %q", out)
	}

	// An empty query is rejected.
	if _, err := tool.Execute(context.Background(), []byte(`{"query":"  "}`)); err == nil {
		t.Error("empty query should fail")
	}
}

func TestWebSearchProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	tool := tools.WebSearch{P: tools.DuckDuckGo{BaseURL: srv.URL, Client: srv.Client()}}
	_, err := tool.Execute(context.Background(), []byte(`{"query":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Errorf("want HTTP 429 surfaced, got %v", err)
	}
}
