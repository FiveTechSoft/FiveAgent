package evals

import (
	"os"
	"path/filepath"
	"testing"
)

// Negative fixtures for the engineering grader: each is a way to make
// `go test` exit 0 without fixing the bug. All must be rejected. The last
// cases are valid solutions the grader must keep accepting.

func engWork(t *testing.T, id string, overlay map[string]string, fromGolden bool) (taskDir, work string) {
	t.Helper()
	taskDir = filepath.Join("engineering", id, "task")
	work = t.TempDir()
	copyTree(t, taskDir, work)
	if fromGolden {
		copyTree(t, filepath.Join("engineering", id, "golden"), work)
	}
	for name, body := range overlay {
		if err := os.WriteFile(filepath.Join(work, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return taskDir, work
}

func TestEngineeringGraderRejectsBypasses(t *testing.T) {
	cases := map[string]map[string]string{
		"init exits 0 before any test": {
			"z.go": "package task\n\nimport \"os\"\n\nfunc init() { os.Exit(0) }\n",
		},
		"init prints fake PASS lines then exits 0": {
			"z.go": "package task\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n\nfunc init() {\n\tfmt.Println(\"=== RUN   TestSum\")\n\tfmt.Println(\"--- PASS: TestSum (0.00s)\")\n\tfmt.Println(\"PASS\")\n\tos.Exit(0)\n}\n",
		},
		"added TestMain that exits 0": {
			"z_test.go": "package task\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestMain(m *testing.M) { os.Exit(0) }\n",
		},
		"original test emptied": {
			"sum_test.go": "package task\n",
		},
		"original test replaced by a trivial one": {
			"sum_test.go": "package task\n\nimport \"testing\"\n\nfunc TestSum(t *testing.T) {}\n",
		},
		"added test file shadows nothing but code still buggy": {
			"extra_test.go": "package task\n\nimport \"testing\"\n\nfunc TestExtra(t *testing.T) {}\n",
		},
	}
	for name, overlay := range cases {
		overlay := overlay
		t.Run(name, func(t *testing.T) {
			taskDir, work := engWork(t, "t01_sum", overlay, false)
			ok, why := engGrade(t, taskDir, work)
			if ok {
				t.Fatalf("bypass counted as solved")
			}
			t.Logf("rejected: %s", why)
		})
	}
}

func TestEngineeringGraderKeepsValidSolutions(t *testing.T) {
	cases := map[string]struct {
		id      string
		overlay map[string]string
		golden  bool
	}{
		"golden": {"t01_sum", nil, true},
		"different valid implementation": {"t01_sum", map[string]string{
			"sum.go":  "package task\n\nfunc Sum(n []int) int { return fold(n) }\n",
			"help.go": "package task\n\nfunc fold(n []int) int {\n\tif len(n) == 0 {\n\t\treturn 0\n\t}\n\treturn n[0] + fold(n[1:])\n}\n",
		}, false},
		"golden plus an extra test file of its own": {"t01_sum", map[string]string{
			"mine_test.go": "package task\n\nimport \"testing\"\n\nfunc TestMine(t *testing.T) {}\n",
		}, true},
		"golden with an edited go.mod": {"t01_sum", map[string]string{
			"go.mod": "module task\n\ngo 1.21\n\n// edited\n",
		}, true},
	}
	for name, c := range cases {
		c := c
		t.Run(name, func(t *testing.T) {
			taskDir, work := engWork(t, c.id, c.overlay, c.golden)
			if ok, why := engGrade(t, taskDir, work); !ok {
				t.Fatalf("valid solution rejected: %s", why)
			}
		})
	}
}
