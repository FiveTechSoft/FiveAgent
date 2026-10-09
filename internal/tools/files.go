package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// Workspace file tools (stage 21): read_file, write_file and edit_file,
// scoped to the current user's folder. Every write snapshots the prior
// state with go-git, so every edit is undoable and auditable.

// maxReadBytes caps one read_file result so a huge file cannot flood
// the model context.
const maxReadBytes = 64 * 1024

// Workspace locates the per-user folders the file tools operate on.
type Workspace struct {
	// Root is the base folder; each user gets Root/<channel>-<userID>/.
	Root string
}

// userRoot resolves and creates the current user's folder.
func (w Workspace) userRoot(ctx context.Context) (string, error) {
	channel, userID := RequestInfo(ctx)
	if userID == "" {
		return "", errors.New("workspace: no user in the request context")
	}
	var b strings.Builder
	for _, r := range channel + "-" + userID {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	root := filepath.Join(w.Root, b.String())
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	return root, nil
}

// resolve maps a model-given path into root, refusing escapes: the
// path is treated as relative to the user's folder however it is
// written ("../x", "/etc/passwd" all stay inside).
func resolve(root, rel string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		return "", errors.New("path is empty")
	}
	clean := filepath.Clean(string(filepath.Separator) + filepath.FromSlash(rel))
	full := filepath.Join(root, clean)
	if err := insideRoot(root, full); err != nil {
		return "", err
	}
	return full, nil
}

// insideRoot refuses a path whose real location, after following symlinks
// in its deepest existing part, leaves the user's folder. The lexical
// cleaning above cannot see a symlink that points elsewhere.
func insideRoot(root, full string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	p := full
	var rest string
	for {
		real, err := filepath.EvalSymlinks(p)
		if err == nil {
			real = filepath.Join(real, rest)
			if real == realRoot || strings.HasPrefix(real, realRoot+string(filepath.Separator)) {
				return nil
			}
			return errors.New("path leaves the workspace folder")
		}
		if !errors.Is(err, os.ErrNotExist) {
			// A dangling symlink or other failure: do not guess.
			if _, lerr := os.Lstat(p); lerr == nil {
				return errors.New("path leaves the workspace folder")
			}
		}
		parent := filepath.Dir(p)
		if parent == p {
			return errors.New("path leaves the workspace folder")
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}

// snapshot commits the folder's current state (pre-write) so the write
// that follows can be undone. Returns the short commit hash, or "" when
// the tree was already clean. Best-effort by contract: callers log but
// never fail a write because a snapshot failed.
func snapshot(root, msg string) (string, error) {
	r, err := git.PlainOpen(root)
	if errors.Is(err, git.ErrRepositoryNotExists) {
		if r, err = git.PlainInit(root, false); err != nil {
			return "", err
		}
	} else if err != nil {
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
		return "", nil // nothing new to snapshot
	}
	h, err := wt.Commit(msg, &git.CommitOptions{
		Author: &object.Signature{Name: "fiveagent", Email: "fiveagent@localhost", When: time.Now()},
	})
	if err != nil {
		return "", err
	}
	return h.String()[:7], nil
}

// ReadFile reads a file from the user's workspace folder.
type ReadFile struct{ WS Workspace }

func (t ReadFile) Name() string { return "read_file" }
func (t ReadFile) Description() string {
	return "Read a text file from your workspace folder. Use it before editing, and to check your own notes."
}
func (t ReadFile) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"file path inside the workspace, e.g. nota.txt or listas/compra.txt"}},"required":["path"]}`)
}
func (t ReadFile) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	root, err := t.WS.userRoot(ctx)
	if err != nil {
		return "", err
	}
	full, err := resolve(root, a.Path)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("read_file: %s does not exist in the workspace", a.Path)
		}
		return "", err
	}
	if len(b) > maxReadBytes {
		return fmt.Sprintf("%s\n... [truncated: file is %d bytes, showing the first %d]", b[:maxReadBytes], len(b), maxReadBytes), nil
	}
	return string(b), nil
}

// WriteFile creates or replaces a file, snapshotting the prior state.
type WriteFile struct{ WS Workspace }

func (t WriteFile) Name() string { return "write_file" }
func (t WriteFile) Description() string {
	return "Create or replace a text file in your workspace folder. Every write is snapshotted, so it can be undone. Prefer edit_file for small changes to an existing file."
}
func (t WriteFile) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`)
}
func (t WriteFile) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	root, err := t.WS.userRoot(ctx)
	if err != nil {
		return "", err
	}
	full, err := resolve(root, a.Path)
	if err != nil {
		return "", err
	}
	snap, serr := snapshot(root, "before write "+a.Path)
	if serr != nil {
		return "", fmt.Errorf("write_file: snapshot failed, write NOT applied: %v", serr)
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(full, []byte(a.Content), 0o644); err != nil {
		return "", err
	}
	if snap != "" {
		return fmt.Sprintf("written %d bytes to %s (previous state snapshotted as %s)", len(a.Content), a.Path, snap), nil
	}
	return fmt.Sprintf("written %d bytes to %s", len(a.Content), a.Path), nil
}

// EditFile replaces one exact text occurrence, returning the diff.
type EditFile struct{ WS Workspace }

func (t EditFile) Name() string { return "edit_file" }
func (t EditFile) Description() string {
	return "Change one exact piece of text in a workspace file: you pass old_text and new_text, the tool verifies old_text appears exactly once, applies the change and returns the diff. Every edit is snapshotted, so it can be undone."
}
func (t EditFile) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"old_text":{"type":"string","description":"exact text to replace; must match exactly once, including spaces and newlines"},"new_text":{"type":"string"}},"required":["path","old_text","new_text"]}`)
}
func (t EditFile) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path    string `json:"path"`
		OldText string `json:"old_text"`
		NewText string `json:"new_text"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	if a.OldText == "" {
		return "", errors.New("edit_file: old_text is empty; use write_file to create a file")
	}
	root, err := t.WS.userRoot(ctx)
	if err != nil {
		return "", err
	}
	full, err := resolve(root, a.Path)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("edit_file: %s does not exist in the workspace", a.Path)
		}
		return "", err
	}
	content := string(b)
	n := strings.Count(content, a.OldText)
	switch {
	case n == 0:
		return "", fmt.Errorf("edit_file: old_text not found in %s - read the file first and copy the text exactly", a.Path)
	case n > 1:
		return "", fmt.Errorf("edit_file: old_text matches %d times in %s - include more surrounding context so it matches exactly once", n, a.Path)
	}
	snap, serr := snapshot(root, "before edit "+a.Path)
	if serr != nil {
		return "", fmt.Errorf("edit_file: snapshot failed, edit NOT applied: %v", serr)
	}
	updated := strings.Replace(content, a.OldText, a.NewText, 1)
	if err := os.WriteFile(full, []byte(updated), 0o644); err != nil {
		return "", err
	}
	var diff strings.Builder
	fmt.Fprintf(&diff, "--- a/%s\n+++ b/%s\n", a.Path, a.Path)
	for _, line := range strings.Split(strings.TrimRight(a.OldText, "\n"), "\n") {
		fmt.Fprintf(&diff, "-%s\n", line)
	}
	for _, line := range strings.Split(strings.TrimRight(a.NewText, "\n"), "\n") {
		fmt.Fprintf(&diff, "+%s\n", line)
	}
	out := "applied diff:\n" + diff.String()
	if snap != "" {
		out += fmt.Sprintf("(previous state snapshotted as %s)", snap)
	}
	return out, nil
}
