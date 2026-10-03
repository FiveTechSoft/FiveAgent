package tools_test

import (
	"context"
	"encoding/json"
	"errors"

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

// windowsBuiltinSandbox mimics Windows CreateProcess: argv[0] is
// resolved as a real executable, so shell builtins such as echo fail
// with "cannot find the file" while cmd /c <builtin> runs fine.
type windowsBuiltinSandbox struct{ calls [][]string }

func (w *windowsBuiltinSandbox) Name() string { return "fake-windows" }

func (w *windowsBuiltinSandbox) Run(_ context.Context, _ string, argv []string) (sandbox.Result, error) {
	w.calls = append(w.calls, append([]string(nil), argv...))
	if argv[0] != "cmd" {
		return sandbox.Result{}, errors.New("sandbox: CreateProcess: The system cannot find the file specified.")
	}
	return sandbox.Result{Stdout: strings.Join(argv[2:], " ") + "\r\n"}, nil
}

// TestRunCommandRetriesWindowsBuiltinViaCmd: battery run 11 MISS - the
// model invoked the shell builtin `echo` the user asked for, but
// Windows has no echo.exe, so CreateProcess reported "cannot find the
// file". The tool must retry such builtins once through cmd /c.
func TestRunCommandRetriesWindowsBuiltinViaCmd(t *testing.T) {
	sb := &windowsBuiltinSandbox{}
	tool := tools.RunCommand{SB: sb}
	ctx := tools.WithRequestInfo(context.Background(), "whatsapp", "34600000000")
	out, err := tool.Execute(ctx, []byte(`{"command":"echo","args":["FooBAR-Baz_123"]}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "FooBAR-Baz_123") {
		t.Errorf("output must carry the builtin's stdout:\n%s", out)
	}
	if len(sb.calls) != 2 {
		t.Fatalf("calls = %v, want [echo ...] then [cmd /c payload]", sb.calls)
	}
	retry := sb.calls[1]
	if len(retry) != 3 || retry[0] != "cmd" || retry[1] != "/c" || retry[2] != "echo FooBAR-Baz_123" {
		t.Errorf("retry call = %v, want [cmd /c \"echo FooBAR-Baz_123\"] as one payload", retry)
	}
}

// TestRunCommandDoesNotRetryUnknownCommand: an executable that is
// genuinely absent keeps its own error - the wrap would name the wrong
// program (battery run 11 relies on this error shape).
func TestRunCommandDoesNotRetryUnknownCommand(t *testing.T) {
	fl := failingSandbox{err: errors.New("sandbox: CreateProcess: The system cannot find the file specified.")}
	tool := tools.RunCommand{SB: &fl}
	ctx := tools.WithRequestInfo(context.Background(), "whatsapp", "34600000000")
	_, err := tool.Execute(ctx, []byte(`{"command":"comando_que_no_existe_xyz123"}`))
	if err == nil {
		t.Fatal("missing executable must stay an error")
	}
	if len(fl.calls) != 1 {
		t.Errorf("calls = %v, want exactly one (no cmd /c wrap)", fl.calls)
	}
}

// TestRunCommandDoesNotRetryOtherErrors: only a missing-file error
// triggers the wrap; retrying e.g. an access failure would run the
// command twice.
func TestRunCommandDoesNotRetryOtherErrors(t *testing.T) {
	fl := failingSandbox{err: errors.New("sandbox: CreateProcess: Access is denied.")}
	tool := tools.RunCommand{SB: &fl}
	ctx := tools.WithRequestInfo(context.Background(), "whatsapp", "34600000000")
	if _, err := tool.Execute(ctx, []byte(`{"command":"echo","args":["x"]}`)); err == nil {
		t.Fatal("access denied must stay an error")
	}
	if len(fl.calls) != 1 {
		t.Errorf("calls = %v, want exactly one (no retry)", fl.calls)
	}
}

type failingSandbox struct {
	err   error
	calls [][]string
}

func (f *failingSandbox) Name() string { return "fake-failing" }

func (f *failingSandbox) Run(_ context.Context, _ string, argv []string) (sandbox.Result, error) {
	f.calls = append(f.calls, append([]string(nil), argv...))
	return sandbox.Result{}, f.err
}

// The cmd /c retry joins argv into one payload, so an argument holding cmd
// syntax must NOT be retried: the original error comes back and cmd never
// sees the argument.
func TestRunCommandBuiltinRetrySkipsCmdMetacharacters(t *testing.T) {
	for _, arg := range []string{"hi & del x", "a|b", "x > out.txt", "%PATH%", "^&", "\"q\"", "(a)", "a\nb"} {
		sb := &windowsBuiltinSandbox{}
		tool := tools.RunCommand{SB: sb}
		ctx := tools.WithRequestInfo(context.Background(), "whatsapp", "34600000000")
		payload, _ := json.Marshal(map[string]any{"command": "echo", "args": []string{arg}})
		out, _ := tool.Execute(ctx, payload)
		for _, c := range sb.calls {
			if c[0] == "cmd" {
				t.Errorf("arg %q reached cmd /c: %v", arg, sb.calls)
			}
		}
		if strings.Contains(out, arg) && len(sb.calls) != 1 {
			t.Errorf("arg %q: unexpected retry output %q", arg, out)
		}
	}
}
