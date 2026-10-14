package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// Local git over the user's workspace folder: status, log and commit.
// It uses the same repository the pre-write snapshots use, never talks
// to a network and has no push, fetch, clone or remote handling at all.

const (
	maxGitMessage = 200
	maxGitLogRows = 20
)

// GitStatus lists the changed files in the workspace folder (read-only).
type GitStatus struct{ WS Workspace }

func (GitStatus) Name() string { return "git_status" }
func (GitStatus) Description() string {
	return "List the files changed since the last local commit in your workspace folder (read-only)."
}
func (GitStatus) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (t GitStatus) Execute(ctx context.Context, _ json.RawMessage) (string, error) {
	root, err := t.WS.userRoot(ctx)
	if err != nil {
		return "", err
	}
	r, err := git.PlainOpen(root)
	if errors.Is(err, git.ErrRepositoryNotExists) {
		return "no local history yet", nil
	} else if err != nil {
		return "", err
	}
	wt, err := r.Worktree()
	if err != nil {
		return "", err
	}
	st, err := wt.Status()
	if err != nil {
		return "", err
	}
	if st.IsClean() {
		return "clean", nil
	}
	var b strings.Builder
	for f, s := range st {
		fmt.Fprintf(&b, "%c%c %s\n", s.Staging, s.Worktree, f)
	}
	return b.String(), nil
}

// GitLog shows the latest local commits (read-only).
type GitLog struct{ WS Workspace }

func (GitLog) Name() string { return "git_log" }
func (GitLog) Description() string {
	return "Show the latest local commits of your workspace folder, including automatic pre-write snapshots (read-only)."
}
func (GitLog) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (t GitLog) Execute(ctx context.Context, _ json.RawMessage) (string, error) {
	root, err := t.WS.userRoot(ctx)
	if err != nil {
		return "", err
	}
	r, err := git.PlainOpen(root)
	if errors.Is(err, git.ErrRepositoryNotExists) {
		return "no local history yet", nil
	} else if err != nil {
		return "", err
	}
	it, err := r.Log(&git.LogOptions{})
	if err != nil {
		return "no commits yet", nil
	}
	var b strings.Builder
	n := 0
	for {
		c, err := it.Next()
		if err != nil || n >= maxGitLogRows {
			break
		}
		fmt.Fprintf(&b, "%s %s\n", c.Hash.String()[:7], strings.SplitN(c.Message, "\n", 2)[0])
		n++
	}
	if n == 0 {
		return "no commits yet", nil
	}
	return b.String(), nil
}

// GitCommit commits every change in the workspace folder to its local
// history. Local only: there is no push. Listed in effectTools.
type GitCommit struct{ WS Workspace }

func (GitCommit) Name() string { return "git_commit" }
func (GitCommit) Description() string {
	return "Commit all current changes of your workspace folder to its LOCAL history with a short message. Does not publish anything anywhere."
}
func (GitCommit) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"message":{"type":"string","description":"short commit message"}},"required":["message"]}`)
}
func (t GitCommit) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	msg := strings.TrimSpace(a.Message)
	if msg == "" {
		return "", errors.New("git_commit: empty message")
	}
	if len(msg) > maxGitMessage {
		msg = msg[:maxGitMessage]
	}
	root, err := t.WS.userRoot(ctx)
	if err != nil {
		return "", err
	}
	r, err := git.PlainOpen(root)
	if errors.Is(err, git.ErrRepositoryNotExists) {
		r, err = git.PlainInit(root, false)
	}
	if err != nil {
		return "", err
	}
	wt, err := r.Worktree()
	if err != nil {
		return "", err
	}
	if _, err := wt.Add("."); err != nil {
		return "", err
	}
	st, err := wt.Status()
	if err != nil {
		return "", err
	}
	if st.IsClean() {
		return "nothing to commit", nil
	}
	h, err := wt.Commit(msg, &git.CommitOptions{
		Author: &object.Signature{Name: "fiveagent", Email: "fiveagent@localhost", When: time.Now()},
	})
	if err != nil {
		return "", err
	}
	return "committed locally " + h.String()[:7], nil
}
