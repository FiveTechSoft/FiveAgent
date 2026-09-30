package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/memory"
)

// Stage 7f: save_learning writes the lesson to learnings.md on disk
// (the test fails if the mechanism never fires), dedups a repeat, and
// the lesson is recallable by the feedback alias.
func TestSaveLearning(t *testing.T) {
	kn, err := memory.OpenKnowledge(filepath.Join(t.TempDir(), "memory"))
	if err != nil {
		t.Fatal(err)
	}
	tl := SaveLearning{K: kn}
	call := func(entry string) string {
		args, _ := json.Marshal(map[string]string{"entry": entry})
		out, err := tl.Execute(context.Background(), args)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := call("when asked for the other Taylor, ask which one instead of guessing"); got != "saved" {
		t.Fatalf("first save = %q, want saved", got)
	}
	if got := call("when asked for the other Taylor, ask which one instead of guessing"); got != "already stored" {
		t.Fatalf("duplicate save = %q, want already stored", got)
	}
	if got := call("no"); !strings.HasPrefix(got, "error:") {
		t.Fatalf("short lesson = %q, want an error line", got)
	}
	raw, err := os.ReadFile(filepath.Join(kn.Dir(), "learnings.md"))
	if err != nil {
		t.Fatalf("learnings.md not on disk: %v", err)
	}
	if !strings.Contains(string(raw), "the other Taylor") {
		t.Fatalf("lesson not in learnings.md:\n%s", raw)
	}
	// Recall by the alias word must inject the learnings file even
	// when no body word matches.
	hits, err := kn.Recall("revisemos los aprendizajes")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].ID != "learnings" {
		t.Fatalf("alias recall hits = %+v, want learnings first", hits)
	}
}
