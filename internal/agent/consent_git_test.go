package agent

import "testing"

// git_commit changes the user's folder, so it must be held for
// confirmation like write_file; the read-only git tools must not be.
func TestGitCommitIsAnEffectTool(t *testing.T) {
	if !effectTools["git_commit"] {
		t.Fatal("git_commit must require effect confirmation")
	}
	for _, n := range []string{"git_status", "git_log"} {
		if effectTools[n] {
			t.Fatalf("%s is read-only and must not be gated", n)
		}
	}
}
