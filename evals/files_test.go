package evals

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

// Stage 21: workspace file tools - read/write/edit scoped to the
// user's folder, true diffs, and a go-git snapshot before every write.

func wsCtx() context.Context { return tools.WithRequestInfo(context.Background(), "test", "user1") }

func mustExec(t *testing.T, tool tools.Tool, ctx context.Context, args string) string {
	t.Helper()
	out, err := tool.Execute(ctx, json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s: %v", tool.Name(), err)
	}
	return out
}

// TestWorkspaceRoundtrip: write then read returns the exact content.
func TestWorkspaceRoundtrip(t *testing.T) {
	ws := tools.Workspace{Root: t.TempDir()}
	ctx := wsCtx()
	mustExec(t, tools.WriteFile{WS: ws}, ctx, `{"path":"nota.txt","content":"hola workspace"}`)
	got := mustExec(t, tools.ReadFile{WS: ws}, ctx, `{"path":"nota.txt"}`)
	if got != "hola workspace" {
		t.Fatalf("read back %q", got)
	}
}

// TestEditTrueDiff: the edit verifies a single exact match, applies it,
// and returns the diff; unknown and ambiguous old_text are refused.
func TestEditTrueDiff(t *testing.T) {
	ws := tools.Workspace{Root: t.TempDir()}
	ctx := wsCtx()
	mustExec(t, tools.WriteFile{WS: ws}, ctx, `{"path":"lista.txt","content":"pan\nleche\nhuevos\n"}`)

	out := mustExec(t, tools.EditFile{WS: ws}, ctx, `{"path":"lista.txt","old_text":"leche","new_text":"leche de avena"}`)
	if !strings.Contains(out, "-leche") || !strings.Contains(out, "+leche de avena") {
		t.Fatalf("edit must return the applied diff, got:\n%s", out)
	}
	got := mustExec(t, tools.ReadFile{WS: ws}, ctx, `{"path":"lista.txt"}`)
	if got != "pan\nleche de avena\nhuevos\n" {
		t.Fatalf("file after edit: %q", got)
	}

	_, err := tools.EditFile{WS: ws}.Execute(ctx, json.RawMessage(`{"path":"lista.txt","old_text":"mantequilla","new_text":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown old_text must be refused, got %v", err)
	}

	mustExec(t, tools.WriteFile{WS: ws}, ctx, `{"path":"rep.txt","content":"aa\naa\n"}`)
	_, err = tools.EditFile{WS: ws}.Execute(ctx, json.RawMessage(`{"path":"rep.txt","old_text":"aa","new_text":"bb"}`))
	if err == nil || !strings.Contains(err.Error(), "2 times") {
		t.Fatalf("ambiguous old_text must be refused, got %v", err)
	}
	// The refused edit changed nothing.
	if got := mustExec(t, tools.ReadFile{WS: ws}, ctx, `{"path":"rep.txt"}`); got != "aa\naa\n" {
		t.Fatalf("file changed by a refused edit: %q", got)
	}
}

// TestWorkspacePathEscape: however the path is written, it stays
// inside the user's folder.
func TestWorkspacePathEscape(t *testing.T) {
	root := t.TempDir()
	ws := tools.Workspace{Root: root}
	ctx := wsCtx()
	for _, p := range []string{"../evil.txt", "../../etc/passwd", "/etc/passwd"} {
		mustExec(t, tools.WriteFile{WS: ws}, ctx, `{"path":`+strconv(p)+`,"content":"x"}`)
	}
	// Nothing may exist outside the user's folder.
	matches, _ := filepath.Glob(filepath.Join(root, "*.txt"))
	if len(matches) != 0 {
		t.Fatalf("escape wrote outside the user folder: %v", matches)
	}
	// And the files landed inside, escaped names flattened.
	if _, err := os.Stat(filepath.Join(root, "test-user1", "etc", "passwd")); err != nil {
		t.Fatalf("expected escaped path flattened inside the folder: %v", err)
	}
}

func strconv(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// TestWorkspaceSnapshotPerWrite: every write snapshots the PRIOR
// state, so any previous content is recoverable from the git history.
// Three writes = two snapshots: the last commit holds version dos,
// the one before holds version uno; the working tree has version
// tres, so undoing the last write is "check out HEAD".
func TestWorkspaceSnapshotPerWrite(t *testing.T) {
	root := t.TempDir()
	ws := tools.Workspace{Root: root}
	ctx := wsCtx()
	mustExec(t, tools.WriteFile{WS: ws}, ctx, `{"path":"doc.txt","content":"version uno"}`)
	out := mustExec(t, tools.WriteFile{WS: ws}, ctx, `{"path":"doc.txt","content":"version dos"}`)
	if !strings.Contains(out, "snapshotted") {
		t.Fatalf("the second write must report its snapshot, got %q", out)
	}
	mustExec(t, tools.WriteFile{WS: ws}, ctx, `{"path":"doc.txt","content":"version tres"}`)

	repo, err := git.PlainOpen(filepath.Join(root, "test-user1"))
	if err != nil {
		t.Fatalf("workspace must hold a git repo: %v", err)
	}
	var commits []*object.Commit
	iter, err := repo.Log(&git.LogOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_ = iter.ForEach(func(c *object.Commit) error {
		commits = append(commits, c)
		return nil
	})
	if len(commits) != 2 {
		t.Fatalf("three writes must leave exactly 2 prior-state snapshots, got %d", len(commits))
	}
	// commits[0] is the newest: snapshot taken before the third write
	// (holds version dos); commits[1] holds version uno.
	want := []string{"version dos", "version uno"}
	for i, w := range want {
		tree, err := commits[i].Tree()
		if err != nil {
			t.Fatal(err)
		}
		f, err := tree.File("doc.txt")
		if err != nil {
			t.Fatal(err)
		}
		content, err := f.Contents()
		if err != nil {
			t.Fatal(err)
		}
		if content != w {
			t.Fatalf("snapshot %d must preserve %q, got %q", i, w, content)
		}
	}
}

// TestWorkspacePerUserIsolation: two users never share a folder.
func TestWorkspacePerUserIsolation(t *testing.T) {
	ws := tools.Workspace{Root: t.TempDir()}
	ctx1 := tools.WithRequestInfo(context.Background(), "test", "user1")
	ctx2 := tools.WithRequestInfo(context.Background(), "test", "user2")
	mustExec(t, tools.WriteFile{WS: ws}, ctx1, `{"path":"privado.txt","content":"solo user1"}`)
	if _, err := (tools.ReadFile{WS: ws}).Execute(ctx2, json.RawMessage(`{"path":"privado.txt"}`)); err == nil {
		t.Fatal("user2 read user1's file")
	}
}
