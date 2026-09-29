package trajectory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sample(id string) Record {
	return Record{
		ID:        id,
		StartedAt: time.Now(),
		Channel:   "whatsapp",
		Messages: []Message{
			{Role: "user", Content: "llámame al +34 600 123 456 o escribe a pepe@example.com"},
			{Role: "assistant", ToolCalls: []ToolCall{{Name: "run_command", Arguments: `{"argv":["curl","-H","Authorization: Bearer abc123secreto","x"]}`}}},
			{Role: "tool", Name: "run_command", Content: "token: abc123secreto\n[exit code: 0]"},
		},
		Outcome:   Outcome{Reply: "hecho", DurationMs: 12, ToolRounds: 1},
		ToolStats: map[string]*Stat{"run_command": {Calls: 1, OK: 1}},
	}
}

func TestRedact(t *testing.T) {
	in := "mi correo es pepe@example.com, mi tfno +34 600 123 456, Authorization: Bearer abc123secreto y token: abc123secreto"
	out := Redact(in)
	for _, leaked := range []string{"pepe@example.com", "600 123 456", "abc123secreto"} {
		if strings.Contains(out, leaked) {
			t.Errorf("Redact must strip %q: %q", leaked, out)
		}
	}
	if !strings.Contains(out, "<redacted>") {
		t.Errorf("Redact must mark what it strips: %q", out)
	}
}

func TestLogRedactsAndParses(t *testing.T) {
	l, err := Open(t.TempDir(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Log(sample("t1")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(l.Dir, sessionFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"pepe@example.com", "600 123 456", "abc123secreto"} {
		if strings.Contains(string(b), leaked) {
			t.Errorf("the log must never carry %q", leaked)
		}
	}
	var r Record
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(b))), &r); err != nil {
		t.Fatalf("every line must be a valid trajectory: %v", err)
	}
	if r.ID != "t1" || len(r.Messages) != 3 || r.ToolStats["run_command"].OK != 1 {
		t.Errorf("round-trip lost the record: %+v", r)
	}
}

func TestRotationIsBounded(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	l.MaxBytes = 120 // force a rotation every couple of records
	for i := 0; i < 8; i++ {
		if err := l.Log(sample(fmt.Sprintf("r%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "trajectories-*.jsonl"))
	if len(matches) > 1 { // MaxFiles 2: the live file plus at most 1 rotated kept... see below
		t.Fatalf("rotation must keep at most MaxFiles-1 rotated files, got %d", len(matches))
	}
	if _, err := os.Stat(filepath.Join(dir, sessionFile)); err != nil {
		t.Error("the live file must exist after rotation")
	}
}

func TestLogCaseAndCompare(t *testing.T) {
	oldDir, newDir := t.TempDir(), t.TempDir()
	lo, _ := Open(oldDir, 0, 0)
	ln, _ := Open(newDir, 0, 0)
	if err := lo.LogCase("harbour_fivewin/0", sample("old")); err != nil {
		t.Fatal(err)
	}
	r := sample("new")
	r.AddToolCall("run_command", false)
	if err := ln.LogCase("harbour_fivewin/0", r); err != nil {
		t.Fatal(err)
	}
	report, err := Compare(oldDir, newDir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report, "harbour_fivewin_0") || !strings.Contains(report, "1 failures") {
		t.Errorf("the comparison must surface the new failure: %q", report)
	}
	same, err := Compare(oldDir, oldDir)
	if err != nil || !strings.Contains(same, "no trajectory differences") {
		t.Errorf("identical runs must compare clean: %q %v", same, err)
	}
	// Case ids sanitize to safe filenames.
	if _, err := os.Stat(filepath.Join(newDir, "harbour_fivewin_0.jsonl")); err != nil {
		t.Error("case ids must land in sanitized per-case files")
	}
}
