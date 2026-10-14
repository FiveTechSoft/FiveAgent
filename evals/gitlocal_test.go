package evals

import (
	"path/filepath"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"

	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

// Local git tools: status/log/commit on the user's folder, no network.
func TestGitLocalCommitStatusLog(t *testing.T) {
	root := t.TempDir()
	ws := tools.Workspace{Root: root}
	ctx := wsCtx()

	if got := mustExec(t, tools.GitStatus{WS: ws}, ctx, `{}`); got != "no local history yet" {
		t.Fatalf("fresh folder status: %q", got)
	}
	mustExec(t, tools.WriteFile{WS: ws}, ctx, `{"path":"a.txt","content":"uno"}`)
	// write_file snapshots BEFORE writing, so a.txt is still uncommitted.
	if st := mustExec(t, tools.GitStatus{WS: ws}, ctx, `{}`); !strings.Contains(st, "a.txt") {
		t.Fatalf("status must list a.txt, got %q", st)
	}
	out := mustExec(t, tools.GitCommit{WS: ws}, ctx, `{"message":"add a"}`)
	if !strings.HasPrefix(out, "committed locally ") {
		t.Fatalf("commit output: %q", out)
	}
	if st := mustExec(t, tools.GitStatus{WS: ws}, ctx, `{}`); st != "clean" {
		t.Fatalf("after commit status must be clean, got %q", st)
	}
	if got := mustExec(t, tools.GitCommit{WS: ws}, ctx, `{"message":"again"}`); got != "nothing to commit" {
		t.Fatalf("clean commit: %q", got)
	}
	if lg := mustExec(t, tools.GitLog{WS: ws}, ctx, `{}`); !strings.Contains(lg, "add a") {
		t.Fatalf("log must show the commit, got %q", lg)
	}
	if _, err := (tools.GitCommit{WS: ws}).Execute(ctx, []byte(`{"message":"  "}`)); err == nil {
		t.Fatal("empty message must be refused")
	}
}

// The tools never configure a remote: the history stays on the machine.
func TestGitLocalHasNoRemote(t *testing.T) {
	root := t.TempDir()
	ws := tools.Workspace{Root: root}
	ctx := wsCtx()
	mustExec(t, tools.WriteFile{WS: ws}, ctx, `{"path":"a.txt","content":"x"}`)
	mustExec(t, tools.GitCommit{WS: ws}, ctx, `{"message":"m"}`)
	for _, n := range []string{"git_push", "git_fetch", "git_clone", "git_remote"} {
		for _, tl := range []tools.Tool{tools.GitStatus{}, tools.GitLog{}, tools.GitCommit{}} {
			if tl.Name() == n {
				t.Fatalf("unexpected network tool %s", n)
			}
		}
	}
	// find the user's repo: the only folder under root
	dirs, _ := filepathGlob(root)
	if len(dirs) != 1 {
		t.Fatalf("expected one user folder, got %v", dirs)
	}
	r, err := git.PlainOpen(dirs[0])
	if err != nil {
		t.Fatal(err)
	}
	if rem, _ := r.Remotes(); len(rem) != 0 {
		t.Fatalf("repo must have no remotes, got %v", rem)
	}
}

// Two users never see each other's history.
func TestGitLocalPerUser(t *testing.T) {
	ws := tools.Workspace{Root: t.TempDir()}
	a := tools.WithRequestInfo(wsCtx(), "test", "alice")
	b := tools.WithRequestInfo(wsCtx(), "test", "bob")
	mustExec(t, tools.WriteFile{WS: ws}, a, `{"path":"s.txt","content":"secret"}`)
	mustExec(t, tools.GitCommit{WS: ws}, a, `{"message":"alice only"}`)
	if lg := mustExec(t, tools.GitLog{WS: ws}, b, `{}`); strings.Contains(lg, "alice") {
		t.Fatalf("bob sees alice's history: %q", lg)
	}
}

func filepathGlob(root string) ([]string, error) { return filepath.Glob(filepath.Join(root, "*")) }
