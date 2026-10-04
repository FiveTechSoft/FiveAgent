package evals

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// scorerV2Label tags every scorer-v2 verdict. Scorer v1 (the must_contain
// and audit_contains checks in battery_test.go) is untouched and stays
// the scorer of record for every historical run; v2 only adds verdict
// lines next to it and never changes pass/miss/hallucination counts.
//
// Why v2 exists: a saved FrogNano execution run (2026-10-03) scored
// PASS on "echo FooBAR-Baz_123" although every run_command call had
// exited 127 (the model passed whole command lines as the program name,
// so no executable existed) and the reply was the token copied from the
// prompt; and PASS on the Windows launch question although the reply
// gave command="cmd /c" with args ["-c", ...]. v1 checks that words
// appear in the reply, not that they came from an observed execution or
// that the argv is the exact one asked for.
const scorerV2Label = "exec-scorer-v2"

// v2Counts tallies verdicts over one battery run ("exec ok", "exec env",
// "exec fail", "argv ok", "argv fail"); printed once at the end.
var v2Counts = map[string]int{}

func v2Summary() string {
	keys := []string{"exec ok", "exec env", "exec fail", "argv ok", "argv fail"}
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, v2Counts[k]))
	}
	return strings.Join(parts, " ") + " (only v1-pass cases tagged v2_* are judged)"
}

var auditLineRE = regexp.MustCompile(`run_command audit user=\S+ cmd="((?:[^"\\]|\\.)*)" args=(\[[^\]]*\]) exit=(\d+)`)

type auditLine struct {
	cmd, args string
	exit      string
	raw       string
}

func parseAuditLines(delta string) []auditLine {
	var out []auditLine
	for _, ln := range strings.Split(delta, "\n") {
		if m := auditLineRE.FindStringSubmatch(ln); m != nil {
			out = append(out, auditLine{cmd: m[1], args: m[2], exit: m[3], raw: ln})
		}
	}
	return out
}

// execVerdictV2 judges a reply that cites an execution result token.
// "ok": an audit line with exit=0 carries the token in its command line.
// "env": no successful execution and every failing audit line is a
// sandbox start failure by failureClass (not evidence about the model, case should not be scored).
// "unobserved": the token is cited but no successful audit line carries
// it (a hallucinated output when the reply cites it).
// failureClass separates a sandbox that could not start from a call the
// model shaped wrong. A saved FrogNano run showed why: docker answered
// "failed to create task ... runc create failed" for BOTH a dead runtime
// and a program that does not exist, and the audit line cut the cause
// off. So the runc wording alone proves nothing.
//
//	"env":     the cause names the sandbox itself (docker.sock permission
//	           denied, daemon unreachable, image/arch mismatch);
//	"model":   a whole command line passed as the program name (spaces,
//	           no path, no args), or the cause says the executable or file
//	           does not exist;
//	"unknown": anything else, including a runc line whose cause is cut off.
//
// Only "env" can invalidate a case; "unknown" stays a miss on the model.
func failureClass(l auditLine) string {
	low := strings.ToLower(l.raw)
	for _, m := range []string{"docker.sock", "cannot connect to the docker daemon", "exec format error", "no matching manifest", "pull access denied", "unable to find image"} {
		if strings.Contains(low, m) {
			return "env"
		}
	}
	if strings.Contains(low, "executable file not found") || strings.Contains(low, "no such file or directory") {
		return "model"
	}
	fields := strings.Fields(l.cmd)
	if len(fields) > 1 && l.args == "[]" && !strings.ContainsAny(fields[0], `/\`) {
		return "model"
	}
	return "unknown"
}

func execVerdictV2(delta, token string) string {
	lines := parseAuditLines(delta)
	tok := strings.ToLower(token)
	for _, l := range lines {
		if l.exit == "0" && strings.Contains(strings.ToLower(l.cmd+" "+l.args), tok) {
			return "ok"
		}
	}
	if len(lines) > 0 {
		allEnv, envFails := true, 0
		for _, l := range lines {
			if l.exit == "0" {
				continue // an unrelated success (e.g. a probe) is not the target
			}
			if failureClass(l) != "env" {
				allEnv = false
			} else {
				envFails++
			}
		}
		if allEnv && envFails > 0 {
			return "env"
		}
	}
	return "unobserved"
}

var (
	argvCommandRE = regexp.MustCompile(`command\s*[=:]\s*"([^"]*)"`)
	argvArgsRE    = regexp.MustCompile(`args\s*[=:]\s*\[\s*"([^"]*)"`)
)

// argvVerdictV2 requires the exact argv shape "command|firstArg", e.g.
// "cmd|/c": the reply must give command exactly "cmd" and the first
// args element exactly "/c". "cmd /c" as a single command string, or a
// "-c" first arg, fails even though both words appear in the reply.
func argvVerdictV2(reply, want string) bool {
	parts := strings.SplitN(want, "|", 2)
	if len(parts) != 2 {
		return false
	}
	c := argvCommandRE.FindStringSubmatch(reply)
	a := argvArgsRE.FindStringSubmatch(reply)
	return c != nil && a != nil && c[1] == parts[0] && a[1] == parts[1]
}

const (
	// Sandbox that could not start: the cause names the daemon.
	fixtureEnvBroken = `run_command audit user=whatsapp/battery cmd="echo" args=["x"] exit=127 dur=142ms stderr="docker: permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock"
run_command audit user=whatsapp/battery cmd="/bin/true" args=[] exit=0 dur=157ms stderr=""
`
	// Saved FrogNano run: whole command lines as the program name; docker
	// says "runc create failed: unable to st" (cause cut off by the log).
	fixtureMalformed = `run_command audit user=whatsapp/battery cmd="sh -c \"echo 'FooBAR-Baz_123'\"" args=[] exit=127 dur=142ms stderr="docker: Error response from daemon: failed to create task for container: failed to create shim task: OCI runtime create failed: runc create failed: unable to st"
run_command audit user=whatsapp/battery cmd="echo 'FooBAR-Baz_123'" args=[] exit=127 dur=138ms stderr="docker: Error response from daemon: failed to create task for container: runc create failed: unable to st"
run_command audit user=whatsapp/battery cmd="/bin/true" args=[] exit=0 dur=157ms stderr=""
`
	// A runc line with the cause cut off and a well-shaped command: not
	// provably the environment.
	fixtureRuncCutOff = `run_command audit user=whatsapp/battery cmd="echo" args=["x"] exit=127 dur=142ms stderr="docker: Error response from daemon: failed to create task for container: runc create failed: unable to st"` + "\n"
	fixtureEchoOK     = `run_command audit user=whatsapp/battery cmd="echo" args=["auditoria-cinco"] exit=0 dur=138ms stderr=""` + "\n"
	fixtureRealFail   = `run_command audit user=whatsapp/battery cmd="echo 'FooBAR-Baz_123'" args=[] exit=1 dur=10ms stderr="boom"` + "\n"
)

// Negative fixtures replayed from the saved evidence: v1 passed these,
// v2 must not.
func TestScorerV2ExecVerdict(t *testing.T) {
	cases := []struct {
		name, delta, token, want string
	}{
		{"daemon unreachable (docker.sock permission denied), only a probe succeeded", fixtureEnvBroken, "FooBAR-Baz_123", "env"},
		{"whole command lines as program name: model failure, not environment", fixtureMalformed, "FooBAR-Baz_123", "unobserved"},
		{"runc line with the cause cut off is not provably environment", fixtureRuncCutOff, "FooBAR-Baz_123", "unobserved"},
		{"no audit lines at all", "", "FooBAR-Baz_123", "unobserved"},
		{"real failing run (not a sandbox start failure)", fixtureRealFail, "FooBAR-Baz_123", "unobserved"},
		{"genuine successful run", fixtureEchoOK, "auditoria-cinco", "ok"},
		{"successful run of a different command does not cover the token", fixtureEchoOK, "FooBAR-Baz_123", "unobserved"},
	}
	for _, c := range cases {
		if got := execVerdictV2(c.delta, c.token); got != c.want {
			t.Errorf("%s: %s verdict %q, want %q", c.name, scorerV2Label, got, c.want)
		}
	}
}

func TestScorerV2Argv(t *testing.T) {
	bad := "En Windows, usarías:\n```\ncommand=\"cmd /c\"\nargs=[\"-c\", \"echo FooBAR-Baz_123\"]\n```"
	good := "```\ncommand=\"cmd\"\nargs=[\"/c\", \"echo hola\"]\n```"
	badMixed := "command=\"cmd\"\nargs=[\"-c\", \"echo\"]"
	if argvVerdictV2(bad, "cmd|/c") {
		t.Error("command=\"cmd /c\" with args [\"-c\"] must fail v2 (v1 passes it on the words alone)")
	}
	if argvVerdictV2(badMixed, "cmd|/c") {
		t.Error("args first element \"-c\" must fail v2")
	}
	if !argvVerdictV2(good, "cmd|/c") {
		t.Error("exact command=cmd args[0]=/c must pass v2")
	}
	if argvVerdictV2(good, "malformed") {
		t.Error("malformed want must fail closed")
	}
	// The v1 words test would have passed the bad reply.
	if !containsAll(strings.ToLower(bad), []string{"cmd", "/c"}) {
		t.Fatal("fixture no longer demonstrates the v1 gap")
	}
}
