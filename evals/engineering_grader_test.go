package evals

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// engGrade decides whether the agent's work folder solves an engineering
// task, and says why not. It does not trust the folder the agent edited:
//
//  1. The original *_test.go files must be byte-identical in the work folder.
//  2. Grading happens in a FRESH folder built from the agent's top-level
//     non-test .go files, the ORIGINAL go.mod and the ORIGINAL tests. Test
//     files the agent added and any go.mod edit are dropped.
//  3. The grader adds a TestMain that records, outside that folder, that the
//     test binary really reached the end of its run. Code that calls
//     os.Exit in init() never gets there.
//  4. `go test -json` must show every test the original files declare as
//     passed (not skipped, not failed), and the package as passed. Printing
//     "PASS" lines from init() does not produce the marker.
//
// Any alternative fix that passes the original tests is accepted, helper
// files included. Limit: this defeats the bypasses above, not a program
// that reads the grader's own files at run time and forges the marker.
func engGrade(t *testing.T, taskDir, work string) (bool, string) {
	t.Helper()
	origTests, _ := filepath.Glob(filepath.Join(taskDir, "*_test.go"))
	var want []string
	re := regexp.MustCompile(`(?m)^func (Test\w+)\(`)
	for _, f := range origTests {
		a, _ := os.ReadFile(f)
		b, err := os.ReadFile(filepath.Join(work, filepath.Base(f)))
		if err != nil || !bytes.Equal(a, b) {
			return false, "original test file modified: " + filepath.Base(f)
		}
		for _, m := range re.FindAllStringSubmatch(string(a), -1) {
			want = append(want, m[1])
		}
	}
	if len(want) == 0 {
		return false, "task declares no tests"
	}

	g, marker := t.TempDir(), filepath.Join(t.TempDir(), "ran")
	srcs, _ := filepath.Glob(filepath.Join(work, "*.go"))
	for _, f := range srcs {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		if err := os.WriteFile(filepath.Join(g, filepath.Base(f)), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range origTests {
		b, _ := os.ReadFile(f)
		os.WriteFile(filepath.Join(g, filepath.Base(f)), b, 0o644)
	}
	mod, _ := os.ReadFile(filepath.Join(taskDir, "go.mod"))
	os.WriteFile(filepath.Join(g, "go.mod"), mod, 0o644)
	tm := fmt.Sprintf(`package task

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	code := m.Run()
	os.WriteFile(%q, []byte("ran"), 0o600)
	os.Exit(code)
}
`, marker)
	if err := os.WriteFile(filepath.Join(g, "zz_grader_main_test.go"), []byte(tm), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "test", "-json", "-count=1", "-timeout", "10s", "./...")
	cmd.Dir = g
	var out bytes.Buffer
	cmd.Stdout = &out
	runErr := cmd.Run()

	status := map[string]string{}
	pkgAction := ""
	for _, line := range strings.Split(out.String(), "\n") {
		var ev struct{ Action, Test string }
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		switch ev.Action {
		case "pass", "fail", "skip":
			if ev.Test == "" {
				pkgAction = ev.Action
			} else {
				status[ev.Test] = ev.Action
			}
		}
	}
	if runErr != nil || pkgAction != "pass" {
		return false, "go test failed or timed out"
	}
	if b, err := os.ReadFile(marker); err != nil || string(b) != "ran" {
		return false, "test binary did not reach the end of its run (early exit)"
	}
	for _, n := range want {
		if status[n] != "pass" {
			return false, fmt.Sprintf("expected test %s is %q, not pass", n, status[n])
		}
	}
	return true, ""
}
