// Package vmgui is the stage-26 proof of concept: drive a real virtual
// screen through the capture/click/type/key surface the ROADMAP sketches
// for the VM GUI backend, and verify the effect on a real screenshot.
//
// This X11 backend runs against any X display, typically an Xvfb virtual
// framebuffer. It is REAL screen control of a REAL X server - real pixels
// in, real key/button events out - but it is NOT a virtual machine: there
// is no QEMU, no KVM, no guest OS boot. The QEMU/KVM + VNC/AT-SPI backend
// from the roadmap stays the planned production path; this backend exists
// to prove the tool surface and the see-act-verify loop end to end.
//
// External programs it shells out to: xdotool (input, window info),
// ImageMagick import (screenshot). No new Go dependencies.
package vmgui

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Backend is the minimal screen-control surface of the stage-26 design:
// see the screen, act on it. A future QEMU/VNC backend implements the
// same interface.
type Backend interface {
	// Capture returns a PNG screenshot of the whole virtual screen.
	Capture(ctx context.Context) ([]byte, error)
	// Click moves the pointer to (x, y) and presses mouse button 1.
	Click(ctx context.Context, x, y int) error
	// Type sends text as individual key events.
	Type(ctx context.Context, text string) error
	// Key sends one key combination in xdotool syntax ("ctrl+a", "Return").
	Key(ctx context.Context, combo string) error
}

// X11 is a Backend driving one X display, typically an Xvfb virtual screen.
type X11 struct {
	// Display is the X display name, e.g. ":99".
	Display string
}

func (x X11) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(cmd.Environ(), "DISPLAY="+x.Display)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("vmgui: %s %s: %w: %s", name, strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return out, nil
}

// Capture screenshots the root window as PNG via ImageMagick import.
func (x X11) Capture(ctx context.Context) ([]byte, error) {
	png, err := x.run(ctx, "import", "-window", "root", "png:-")
	if err != nil {
		return nil, err
	}
	if len(png) < 8 || !bytes.Equal(png[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		return nil, fmt.Errorf("vmgui: capture is not a PNG (%d bytes)", len(png))
	}
	return png, nil
}

// Click moves the pointer and presses button 1.
func (x X11) Click(ctx context.Context, px, py int) error {
	_, err := x.run(ctx, "xdotool", "mousemove", strconv.Itoa(px), strconv.Itoa(py), "click", "1")
	return err
}

// Type sends text as key events, one character at a time.
func (x X11) Type(ctx context.Context, text string) error {
	_, err := x.run(ctx, "xdotool", "type", "--clearmodifiers", "--", text)
	return err
}

// Key sends one key combination, e.g. "ctrl+a" or "Return".
func (x X11) Key(ctx context.Context, combo string) error {
	_, err := x.run(ctx, "xdotool", "key", "--clearmodifiers", combo)
	return err
}

// WindowID returns the first window whose class or name matches pattern.
func (x X11) WindowID(ctx context.Context, pattern string) (string, error) {
	out, err := x.run(ctx, "xdotool", "search", "--onlyvisible", "--class", pattern)
	if err != nil {
		return "", err
	}
	id := strings.Fields(string(out))
	if len(id) == 0 {
		return "", fmt.Errorf("vmgui: no window matches %q", pattern)
	}
	return id[0], nil
}

// WindowGeometry returns the x, y, width and height of a window.
func (x X11) WindowGeometry(ctx context.Context, id string) (gx, gy, w, h int, err error) {
	out, err := x.run(ctx, "xdotool", "getwindowgeometry", "--shell", id)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	vals := map[string]int{}
	for _, line := range strings.Split(string(out), "\n") {
		kv := strings.SplitN(line, "=", 2)
		if len(kv) != 2 {
			continue
		}
		vals[kv[0]], _ = strconv.Atoi(strings.TrimSpace(kv[1]))
	}
	return vals["X"], vals["Y"], vals["WIDTH"], vals["HEIGHT"], nil
}

// WindowFocus gives the window the X input focus. Unlike windowactivate
// it does not need a window manager: it calls XSetInputFocus directly.
func (x X11) WindowFocus(ctx context.Context, id string) error {
	_, err := x.run(ctx, "xdotool", "windowfocus", id)
	return err
}

// FocusedWindow returns the window holding the X input focus.
func (x X11) FocusedWindow(ctx context.Context) (string, error) {
	out, err := x.run(ctx, "xdotool", "getwindowfocus")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
