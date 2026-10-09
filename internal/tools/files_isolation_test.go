package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fakeSecret = "FAKE-SECRET-not-a-real-credential-0000"

// Two users and an outside file, all fictional. A model-chosen path must
// never reach another user's folder or anything outside the workspace.
func isolationRig(t *testing.T) (Workspace, context.Context, string) {
	t.Helper()
	base := t.TempDir()
	ws := Workspace{Root: filepath.Join(base, "ws")}
	other := WithRequestInfo(context.Background(), "wa", "victim")
	root, err := ws.userRoot(other)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte(fakeSecret), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "outside.txt")
	if err := os.WriteFile(outside, []byte(fakeSecret), 0o644); err != nil {
		t.Fatal(err)
	}
	return ws, WithRequestInfo(context.Background(), "wa", "attacker"), outside
}

func readAs(ws Workspace, ctx context.Context, path string) string {
	args, _ := json.Marshal(map[string]string{"path": path})
	out, err := (ReadFile{WS: ws}).Execute(ctx, args)
	if err != nil {
		return "error: " + err.Error()
	}
	return out
}

func TestReadFileCannotReachOtherUserOrOutside(t *testing.T) {
	ws, ctx, outside := isolationRig(t)
	for _, p := range []string{
		"../wa-victim/secret.txt",
		"../../wa-victim/secret.txt",
		"/wa-victim/secret.txt",
		outside,
		"..\\wa-victim\\secret.txt",
	} {
		if out := readAs(ws, ctx, p); strings.Contains(out, fakeSecret) {
			t.Fatalf("path %q leaked the fake secret", p)
		}
	}
}

func TestReadFileSymlinkCannotEscapeWorkspace(t *testing.T) {
	ws, ctx, outside := isolationRig(t)
	root, _ := ws.userRoot(ctx)
	victim, _ := ws.userRoot(WithRequestInfo(context.Background(), "wa", "victim"))
	for name, target := range map[string]string{"link-out": outside, "link-user": filepath.Join(victim, "secret.txt")} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if out := readAs(ws, ctx, name); strings.Contains(out, fakeSecret) {
			t.Fatalf("symlink %s leaked the fake secret", name)
		}
	}
	// A symlinked directory must not let a nested path through either.
	if err := os.Symlink(victim, filepath.Join(root, "dir-user")); err == nil {
		if out := readAs(ws, ctx, "dir-user/secret.txt"); strings.Contains(out, fakeSecret) {
			t.Fatal("symlinked directory leaked the fake secret")
		}
	}
}
