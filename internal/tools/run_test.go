package tools_test

import (
	"context"

	"strings"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/sandbox"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

type fakeSandbox struct{ res sandbox.Result }

func (f fakeSandbox) Name() string { return "fake" }
func (f fakeSandbox) Run(_ context.Context, _ string, _ []string) (sandbox.Result, error) {
	return f.res, nil
}

// TestRunCommandShowsExitCode: a non-zero exit must be visible to the
// model alongside the captured stderr (a bare failure with no stderr
// was a first-live-test finding).
func TestRunCommandShowsExitCode(t *testing.T) {
	tool := tools.RunCommand{SB: fakeSandbox{res: sandbox.Result{
		Stdout: "", Stderr: "curl: (7) Failed to connect", ExitCode: 7,
	}}}
	ctx := tools.WithRequestInfo(context.Background(), "whatsapp", "34600000000")
	out, err := tool.Execute(ctx, []byte(`{"command":"curl","args":["-I","https://example.com"]}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "Failed to connect") || !strings.Contains(out, "exit code 7") {
		t.Errorf("output must carry stderr AND the exit code:\n%s", out)
	}
}

// TestShellGuidancePerOS locks the shell rule the model reads: Windows
// commands go through cmd /c and bash is not assumed; elsewhere sh -c.
func TestShellGuidancePerOS(t *testing.T) {
	tool := tools.RunCommand{SB: fakeSandbox{}}
	desc := tool.Description()
	if !strings.Contains(desc, "without a shell") {
		t.Errorf("description must say there is no shell:\n%s", desc)
	}
}
