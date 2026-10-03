package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"runtime"
	"strings"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/sandbox"
)

// ctxKey carries the current request's channel and user so tools can
// scope their work (the sandbox keeps one writable folder per user).
type ctxKey struct{}

// WithRequestInfo returns a ctx carrying the channel and user ID of the
// message being answered. The agent sets it before calling tools.
func WithRequestInfo(ctx context.Context, channel, userID string) context.Context {
	return context.WithValue(ctx, ctxKey{}, channel+"/"+userID)
}

// RequestInfo reads back what WithRequestInfo set: the channel and
// user ID of the message being answered ("" when unset).
func RequestInfo(ctx context.Context) (channel, userID string) {
	s, _ := ctx.Value(ctxKey{}).(string)
	channel, userID, _ = strings.Cut(s, "/")
	return
}

// RunCommand lets the model execute commands inside the per-user
// sandbox: no network, only the user's own folder writable, killed
// after the configured timeout.
type RunCommand struct {
	SB sandbox.Sandbox
}

// Name implements Tool.
func (RunCommand) Name() string { return "run_command" }

// Description implements Tool.
func (r RunCommand) Description() string {
	limits := "no network, only the user's own folder is writable, killed after a timeout"
	if r.SB.Name() == "jobobject" {
		limits = "runs in the user's own folder with a RAM cap and a timeout; filesystem and network isolation are not enforced yet on Windows"
	}
	return fmt.Sprintf("Run a command inside the user's sandbox (%s backend): %s. Good for calculations, small scripts and file tasks. Files persist between calls. %s", r.SB.Name(), limits, shellGuidance(runtime.GOOS))
}

// shellGuidance tells the model how to invoke shell syntax on each OS:
// the tool receives a program + arguments and runs them WITHOUT a
// shell, so builtins (dir, echo, pipes, redirects) need the OS shell
// explicitly. On Windows that is cmd /c - bash is missing on most
// Windows machines (first live-test finding: the model ran bash -c and
// the command failed).
func shellGuidance(goos string) string {
	if goos == "windows" {
		return "The command runs directly, without a shell. On Windows use command \"cmd\" with args [\"/c\", \"...\"] (e.g. args [\"/c\", \"dir\"]); bash does not exist on most Windows machines - use it only when the user explicitly asks and it exists."
	}
	return "The command runs directly, without a shell: for shell syntax use command \"sh\" with args [\"-c\", \"...\"] (on Windows it would be cmd /c instead)."
}

// Parameters implements Tool.
func (RunCommand) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {
				"type": "string",
				"description": "The program to run, e.g. python3 or cmd"
			},
			"args": {
				"type": "array",
				"items": {"type": "string"},
				"description": "Arguments, e.g. [\"-c\", \"print(2+2)\"]"
			}
		},
		"required": ["command"]
	}`)
}

// Execute implements Tool.
func (r RunCommand) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	userKey, _ := ctx.Value(ctxKey{}).(string)
	if userKey == "" {
		return "", fmt.Errorf("run_command: no user context")
	}
	var a struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	if strings.TrimSpace(a.Command) == "" {
		return "", fmt.Errorf("run_command: empty command")
	}
	start := time.Now()
	argv := append([]string{a.Command}, a.Args...)
	res, err := r.SB.Run(ctx, userKey, argv)
	if err != nil && isMissingFile(err) && isCmdBuiltin(a.Command) && !hasCmdMetachar(argv) {
		// Battery run 11 MISS: on Windows, echo/dir & co. are cmd
		// builtins with no standalone executable, so CreateProcess
		// reports "cannot find the file" for the very command the user
		// asked for. Retry once through cmd /c with the whole command
		// as one payload (the shape proven live in battery run 11:
		// args ["/c", "echo auditoria-cinco"]); if that also fails,
		// keep the original error - it names the real program.
		payload := strings.Join(argv, " ")
		if res2, err2 := r.SB.Run(ctx, userKey, []string{"cmd", "/c", payload}); err2 == nil {
			log.Printf("run_command: %q has no own executable; ran it through cmd /c", a.Command)
			res, err = res2, nil
		}
	}
	if err != nil {
		// The audit trail distinguishes a real execution from a model
		// that claims it ran something (first-live-test finding).
		log.Printf("run_command audit user=%s cmd=%q args=%q error=%v", userKey, a.Command, a.Args, err)
		return "", err
	}
	// stdout rides along: the battery checks that an error the model
	// narrates really came from this tool (battery run 6: the model
	// quoted WSL's real "Acceso denegado" and the check called it a
	// hallucination because only stderr - empty - was on record).
	log.Printf("run_command audit user=%s cmd=%q args=%q exit=%d dur=%s stdout=%.160q stderr=%.160q", userKey, a.Command, a.Args, res.ExitCode, time.Since(start).Round(time.Millisecond), res.Stdout, res.Stderr)
	var sb strings.Builder
	if res.Stdout != "" {
		sb.WriteString(res.Stdout)
	}
	if res.Stderr != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("stderr: " + res.Stderr)
	}
	if res.TimedOut {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("(killed: timeout)")
	}
	if res.ExitCode != 0 && !res.TimedOut {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		fmt.Fprintf(&sb, "(exit code %d)", res.ExitCode)
	}
	out := sb.String()
	const maxForModel = 4000
	if len(out) > maxForModel {
		out = out[:maxForModel] + "\n(truncated)"
	}
	if out == "" {
		out = fmt.Sprintf("(no output, exit code %d)", res.ExitCode)
	}
	return out, nil
}

// cmdBuiltins are Windows shell builtins that have no standalone
// executable: CreateProcess cannot start them by name, only cmd /c can
// run them.
var cmdBuiltins = map[string]bool{
	"echo": true, "dir": true, "cls": true, "copy": true, "move": true,
	"del": true, "erase": true, "type": true, "ren": true, "rename": true,
	"md": true, "mkdir": true, "rd": true, "rmdir": true, "set": true,
	"date": true, "time": true, "ver": true, "vol": true, "cd": true,
	"chdir": true, "pushd": true, "popd": true, "more": true,
	"title": true, "color": true, "path": true, "assoc": true,
	"ftype": true,
}

// hasCmdMetachar reports whether any argument holds a character cmd.exe
// treats as syntax (command chaining, redirection, escapes, variable
// expansion, quotes, grouping, line breaks). The retry joins argv into one
// cmd /c payload, so such a character would turn an argument into a
// command; in that case the retry is skipped and the original error is
// kept.
func hasCmdMetachar(argv []string) bool {
	for _, a := range argv {
		if strings.ContainsAny(a, "&|<>^%!\"()`\r\n") {
			return true
		}
	}
	return false
}

// isCmdBuiltin reports whether command is a bare shell builtin name
// (no path separator - "C:\tools\echo" is a program, not the builtin).
func isCmdBuiltin(command string) bool {
	command = strings.ToLower(strings.TrimSpace(command))
	if strings.ContainsAny(command, `/\`) {
		return false
	}
	return cmdBuiltins[command]
}

// isMissingFile reports whether err says the executable itself could
// not be found: the Windows CreateProcess wording plus Go's own
// exec.LookPath wording (locale-independent).
func isMissingFile(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "cannot find the file") ||
		strings.Contains(msg, "executable file not found")
}
