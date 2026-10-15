package evals

import (
	"os"
	"path/filepath"
	"testing"
)

// Engineering eval tasks: evals/engineering/<id>/task is a Go module with
// a seeded bug and a red test; <id>/golden is a known fix. A task is
// SOLVED per engGrade (engineering_grader_test.go): the original tests are
// untouched and really ran and passed in a clean grading folder.
//
// This test validates the SCORER only: golden must solve every task and
// the untouched task must fail. It says nothing about any model.

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	ents, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// engSolved reports whether the work folder solves the task; see engGrade.
func engSolved(t *testing.T, taskDir, work string) bool {
	t.Helper()
	ok, _ := engGrade(t, taskDir, work)
	return ok
}

func TestEngineeringScorer(t *testing.T) {
	dirs, _ := filepath.Glob("engineering/t*")
	if len(dirs) == 0 {
		t.Fatal("no tasks")
	}
	for _, d := range dirs {
		d := d
		t.Run(filepath.Base(d), func(t *testing.T) {
			noop := t.TempDir()
			copyTree(t, filepath.Join(d, "task"), noop)
			if engSolved(t, filepath.Join(d, "task"), noop) {
				t.Fatal("untouched task must fail")
			}
			gold := t.TempDir()
			copyTree(t, filepath.Join(d, "task"), gold)
			copyTree(t, filepath.Join(d, "golden"), gold)
			if !engSolved(t, filepath.Join(d, "task"), gold) {
				t.Fatal("golden must solve the task")
			}
			// Cheating: edit the test instead of the code.
			cheat := t.TempDir()
			copyTree(t, filepath.Join(d, "task"), cheat)
			tf, _ := filepath.Glob(filepath.Join(cheat, "*_test.go"))
			os.WriteFile(tf[0], []byte("package task\n"), 0o644)
			if engSolved(t, filepath.Join(d, "task"), cheat) {
				t.Fatal("emptying the test must not count as solved")
			}
		})
	}
}
