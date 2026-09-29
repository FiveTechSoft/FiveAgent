package tools

// GitHub tools (stage 27e): last planned copy of the integration
// shape - authorized client factory with an honest "not connected"
// error, read (repos, issues) and write (create issue).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/FiveTechSoft/FiveAgent/internal/github"
)

// GitHubFor builds an authorized GitHub client or explains why it
// cannot.
type GitHubFor func(ctx context.Context) (*github.Client, error)

// GitHubRepos lists the connected user's repos.
type GitHubRepos struct {
	Client GitHubFor
}

func (t GitHubRepos) Name() string { return "github_repos" }

func (t GitHubRepos) Description() string {
	return "List the GitHub repos of the connected account, most recently updated first."
}

func (t GitHubRepos) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"max_results": {"type": "integer", "description": "1-100, default 20"}
		}
	}`)
}

func (t GitHubRepos) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		MaxResults int `json:"max_results"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	c, err := t.Client(ctx)
	if err != nil {
		return "", err
	}
	repos, err := c.ListRepos(ctx, a.MaxResults)
	if err != nil {
		return "", err
	}
	if len(repos) == 0 {
		return "no repos visible", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d repo(s):\n", len(repos))
	for _, r := range repos {
		priv := ""
		if r.Private {
			priv = " (private)"
		}
		fmt.Fprintf(&b, "- %s%s\n", r.FullName, priv)
	}
	return b.String(), nil
}

// GitHubIssues lists issues of one repo.
type GitHubIssues struct {
	Client GitHubFor
}

func (t GitHubIssues) Name() string { return "github_issues" }

func (t GitHubIssues) Description() string {
	return "List issues of a GitHub repo (owner/repo). Pull requests appear too, marked as PR."
}

func (t GitHubIssues) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"owner": {"type": "string"},
			"repo": {"type": "string"},
			"state": {"type": "string", "description": "open (default), closed, all"},
			"max_results": {"type": "integer", "description": "1-100, default 20"}
		},
		"required": ["owner", "repo"]
	}`)
}

func (t GitHubIssues) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Owner      string `json:"owner"`
		Repo       string `json:"repo"`
		State      string `json:"state"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	c, err := t.Client(ctx)
	if err != nil {
		return "", err
	}
	issues, err := c.ListIssues(ctx, a.Owner, a.Repo, a.State, a.MaxResults)
	if err != nil {
		return "", err
	}
	if len(issues) == 0 {
		return "no issues", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d issue(s):\n", len(issues))
	for _, it := range issues {
		pr := ""
		if it.IsPR {
			pr = " [PR]"
		}
		fmt.Fprintf(&b, "- #%d (%s)%s %s\n", it.Number, it.State, pr, it.Title)
	}
	return b.String(), nil
}

// GitHubCreateIssue opens one issue.
type GitHubCreateIssue struct {
	Client GitHubFor
}

func (t GitHubCreateIssue) Name() string { return "github_create_issue" }

func (t GitHubCreateIssue) Description() string {
	return "Open an issue on a GitHub repo (owner/repo, title, body). Confirm the repo, title and body with the user before calling unless they already approved them."
}

func (t GitHubCreateIssue) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"owner": {"type": "string"},
			"repo": {"type": "string"},
			"title": {"type": "string"},
			"body": {"type": "string"}
		},
		"required": ["owner", "repo", "title"]
	}`)
}

func (t GitHubCreateIssue) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	c, err := t.Client(ctx)
	if err != nil {
		return "", err
	}
	n, err := c.CreateIssue(ctx, a.Owner, a.Repo, a.Title, a.Body)
	if err != nil {
		return "", fmt.Errorf("github_create_issue: %w", err)
	}
	return fmt.Sprintf("issue #%d created in %s/%s", n, a.Owner, a.Repo), nil
}
