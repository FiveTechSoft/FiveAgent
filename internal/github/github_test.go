package github

// GitHub client tests (stage 27e): the whole flow against a fake
// endpoint - paths and params, JSON post body, auth and Accept
// headers, PR marking, error message extraction.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListRepos(t *testing.T) {
	var gotAuth, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		if !strings.HasPrefix(r.URL.Path, "/user/repos") {
			t.Errorf("bad path: %s", r.URL.Path)
		}
		w.Write([]byte(`[{"full_name":"me/one","private":false},{"full_name":"me/two","private":true}]`))
	}))
	defer srv.Close()
	c := &Client{HTTP: &http.Client{Transport: ghAuth{srv.Client().Transport}}, Root: srv.URL}
	repos, err := c.ListRepos(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 2 || repos[1].FullName != "me/two" || !repos[1].Private {
		t.Fatalf("unexpected repos: %+v", repos)
	}
	if gotAuth != "Bearer gho_test" {
		t.Fatalf("missing auth header: %q", gotAuth)
	}
	if gotAccept != "application/vnd.github+json" {
		t.Fatalf("missing API accept header: %q", gotAccept)
	}
}

func TestListIssuesMarksPRs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/me/app/issues" {
			t.Errorf("bad path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("state") != "open" {
			t.Errorf("state param: %s", r.URL.RawQuery)
		}
		w.Write([]byte(`[
			{"number":7,"title":"bug","state":"open"},
			{"number":8,"title":"feature PR","state":"open","pull_request":{"url":"..."}}
		]`))
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), Root: srv.URL}
	issues, err := c.ListIssues(context.Background(), "me", "app", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 2 || issues[0].IsPR || !issues[1].IsPR {
		t.Fatalf("PR marking wrong: %+v", issues)
	}
}

func TestCreateIssue(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"number":42}`))
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), Root: srv.URL}
	n, err := c.CreateIssue(context.Background(), "me", "app", "crash on start", "steps...")
	if err != nil {
		t.Fatal(err)
	}
	if n != 42 {
		t.Fatalf("number: %d", n)
	}
	if !strings.Contains(gotBody, `"title":"crash on start"`) || !strings.Contains(gotBody, `"body":"steps..."`) {
		t.Fatalf("post body: %s", gotBody)
	}
}

// GitHub answers errors as JSON with a message field - surface it.
func TestErrorMessageSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), Root: srv.URL}
	if _, err := c.ListIssues(context.Background(), "me", "ghost", "open", 5); err == nil || !strings.Contains(err.Error(), "Not Found") {
		t.Fatalf("error message should surface: %v", err)
	}
}

func TestCreateIssueRequiresTitle(t *testing.T) {
	c := &Client{HTTP: http.DefaultClient, Root: "http://unused"}
	if _, err := c.CreateIssue(context.Background(), "me", "app", "  ", "b"); err == nil || !strings.Contains(err.Error(), "title is required") {
		t.Fatalf("title validation: %v", err)
	}
}

type ghAuth struct{ base http.RoundTripper }

func (g ghAuth) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set("Authorization", "Bearer gho_test")
	return g.base.RoundTrip(r)
}
