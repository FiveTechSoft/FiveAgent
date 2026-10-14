package tools_test

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/FiveTechSoft/FiveAgent/internal/sandbox"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

func runOut(t *testing.T, tool tools.RunCommand) string {
	t.Helper()
	ctx := tools.WithRequestInfo(context.Background(), "test", "u1")
	out, err := tool.Execute(ctx, []byte(`{"command":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// A long build/test log keeps its START and its END (where the failure
// usually is) and says how much was cut.
func TestRunCommandOutputKeepsHeadAndTail(t *testing.T) {
	long := "HEAD-LINE\n" + strings.Repeat("noise\n", 5000) + "FAIL: the real cause\n"
	out := runOut(t, tools.RunCommand{SB: fakeSandbox{res: sandbox.Result{Stdout: long}}})
	if !strings.Contains(out, "HEAD-LINE") || !strings.Contains(out, "the real cause") {
		t.Fatalf("head and tail must survive:\n%.200q ... %.200q", out, out[len(out)-200:])
	}
	if !strings.Contains(out, "bytes omitted from the middle") {
		t.Fatalf("truncation must be announced")
	}
	if len(out) > 4200 {
		t.Fatalf("default cap exceeded: %d", len(out))
	}
}

// The cap is configurable per tool.
func TestRunCommandOutputCapConfigurable(t *testing.T) {
	long := strings.Repeat("a", 3000) + "END"
	small := runOut(t, tools.RunCommand{SB: fakeSandbox{res: sandbox.Result{Stdout: long}}, MaxOutput: 500})
	if len(small) > 700 || !strings.Contains(small, "END") {
		t.Fatalf("500 cap: len=%d", len(small))
	}
	big := runOut(t, tools.RunCommand{SB: fakeSandbox{res: sandbox.Result{Stdout: long}}, MaxOutput: 10000})
	if big != long {
		t.Fatalf("under the cap the output is returned whole")
	}
}

// Cutting never splits a multibyte character.
func TestRunCommandOutputCutIsValidUTF8(t *testing.T) {
	long := strings.Repeat("ñ", 4000)
	out := runOut(t, tools.RunCommand{SB: fakeSandbox{res: sandbox.Result{Stdout: long}}, MaxOutput: 501})
	if !utf8.ValidString(out) {
		t.Fatal("output must be valid UTF-8")
	}
}
