package tools

// GitHub tools tests (stage 27e): honest not-connected errors, arg
// wiring, formatted output.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/github"
)

func githubNotConnected(ctx context.Context) (*github.Client, error) {
	return nil, fmt.Errorf("[github not connected - ask the owner to open /oauth/github/start]")
}

func TestGitHubToolsHonestWhenNotConnected(t *testing.T) {
	for name, tool := range map[string]Tool{
		"github_repos":        GitHubRepos{Client: githubNotConnected},
		"github_issues":       GitHubIssues{Client: githubNotConnected},
		"github_create_issue": GitHubCreateIssue{Client: githubNotConnected},
	} {
		_, err := tool.Execute(context.Background(), []byte(`{"owner":"me","repo":"app","title":"t"}`))
		if err == nil || !strings.Contains(err.Error(), "not connected") {
			t.Fatalf("%s should surface the honest not-connected error, got: %v", name, err)
		}
	}
}

func TestGitHubIssuesFormats(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"number":3,"title":"crash","state":"open"}]`)
	}))
	defer srv.Close()
	tool := GitHubIssues{Client: func(ctx context.Context) (*github.Client, error) {
		return &github.Client{HTTP: srv.Client(), Root: srv.URL}, nil
	}}
	out, err := tool.Execute(context.Background(), []byte(`{"owner":"me","repo":"app"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "#3") || !strings.Contains(out, "crash") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestGitHubCreateIssueReportsNumber(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"number":9}`)
	}))
	defer srv.Close()
	tool := GitHubCreateIssue{Client: func(ctx context.Context) (*github.Client, error) {
		return &github.Client{HTTP: srv.Client(), Root: srv.URL}, nil
	}}
	out, err := tool.Execute(context.Background(), []byte(`{"owner":"me","repo":"app","title":"bug","body":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "#9") || !strings.Contains(out, "me/app") {
		t.Fatalf("create should report the number and repo: %q", out)
	}
}
