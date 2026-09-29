// Package github is the last planned integration copy (roadmap stage
// 27e): same shape as the Google and Slack clients - overridable
// base URL, OAuth-authorized HTTP client from the shared oauth
// package, read (repos, issues) and write (create issue).
//
// GitHub quirks handled honestly: the OAuth token endpoint answers
// form-encoded unless the shared oauth flow sends
// "Accept: application/json" (declared as oauth.Config.TokenHeaders);
// and OAuth-app tokens do not expire, so this provider never
// exercises the refresh path.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Base is the GitHub API root, overridable in tests.
var Base = "https://api.github.com"

// OAuth endpoints for the shared oauth package.
var (
	AuthURL  = "https://github.com/login/oauth/authorize"
	TokenURL = "https://github.com/login/oauth/access_token"
)

// Client reads and writes GitHub.
type Client struct {
	HTTP *http.Client
	Root string // empty uses Base
}

func (c *Client) base() string {
	if c.Root != "" {
		return c.Root
	}
	return Base
}

// Repo is one repository.
type Repo struct {
	FullName string `json:"full_name"`
	Private  bool   `json:"private"`
}

// Issue is one issue (pull requests included - GitHub serves PRs
// through the issues API; the pull_request key marks them).
type Issue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"`
	IsPR   bool   `json:"-"`
}

type issueJSON struct {
	Number int              `json:"number"`
	Title  string           `json:"title"`
	State  string           `json:"state"`
	PR     *json.RawMessage `json:"pull_request"`
}

// ListRepos returns the authenticated user's repos (newest first).
func (c *Client) ListRepos(ctx context.Context, maxResults int) ([]Repo, error) {
	if maxResults <= 0 || maxResults > 100 {
		maxResults = 20
	}
	u := fmt.Sprintf("%s/user/repos?sort=updated&per_page=%d", c.base(), maxResults)
	raw, err := c.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	var repos []Repo
	if err := json.Unmarshal(raw, &repos); err != nil {
		return nil, fmt.Errorf("github: repos: %w", err)
	}
	return repos, nil
}

// ListIssues returns the issues of one repo (state: open, closed, all).
func (c *Client) ListIssues(ctx context.Context, owner, repo, state string, maxResults int) ([]Issue, error) {
	if owner == "" || repo == "" {
		return nil, fmt.Errorf("github: owner and repo are required")
	}
	if state == "" {
		state = "open"
	}
	if maxResults <= 0 || maxResults > 100 {
		maxResults = 20
	}
	u := fmt.Sprintf("%s/repos/%s/%s/issues?state=%s&per_page=%d",
		c.base(), url.PathEscape(owner), url.PathEscape(repo), url.QueryEscape(state), maxResults)
	raw, err := c.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	var parsed []issueJSON
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("github: issues: %w", err)
	}
	var out []Issue
	for _, it := range parsed {
		out = append(out, Issue{Number: it.Number, Title: it.Title, State: it.State, IsPR: it.PR != nil})
	}
	return out, nil
}

// CreateIssue opens one issue and returns its number.
func (c *Client) CreateIssue(ctx context.Context, owner, repo, title, body string) (int, error) {
	if owner == "" || repo == "" {
		return 0, fmt.Errorf("github: owner and repo are required")
	}
	if strings.TrimSpace(title) == "" {
		return 0, fmt.Errorf("github: title is required")
	}
	payload, err := json.Marshal(map[string]string{"title": title, "body": body})
	if err != nil {
		return 0, err
	}
	u := fmt.Sprintf("%s/repos/%s/%s/issues", c.base(), url.PathEscape(owner), url.PathEscape(repo))
	raw, err := c.do(ctx, http.MethodPost, u, payload)
	if err != nil {
		return 0, err
	}
	var parsed struct {
		Number int `json:"number"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return 0, fmt.Errorf("github: create issue: %w", err)
	}
	return parsed.Number, nil
}

func (c *Client) do(ctx context.Context, method, u string, body []byte) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Message != "" {
			return nil, fmt.Errorf("github: %s: %s", resp.Status, e.Message)
		}
		return nil, fmt.Errorf("github: %s", resp.Status)
	}
	return raw, nil
}
