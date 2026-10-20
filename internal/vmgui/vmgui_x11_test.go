package vmgui

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// End-to-end proof of the stage-26 tool surface against a REAL virtual
// screen: an Xvfb X server (no VM - no QEMU/KVM needed - so the virtual
// display stands in for the VM's screen). The test launches a real GUI
// app (gedit), focuses it, clicks into it, types text as key events, and
// verifies the result three ways:
//
//  1. focus: after WindowFocus + Click the window holds the X input focus;
//  2. content: Ctrl+A / Ctrl+C and the clipboard returns the typed text;
//  3. pixels: before/after screenshots differ visibly inside the window.
//
// Artifacts (before.png, after.png) are written to $VMGUI_ARTIFACTS when
// set, so a human can look at the proof.
func TestX11DrivesRealAppOnVirtualScreen(t *testing.T) {
	for _, bin := range []string{"Xvfb", "xdotool", "import", "gedit", "xclip", "xdpyinfo"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("PoC needs %s in PATH", bin)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	display := freeDisplay(t)
	xvfb := exec.CommandContext(ctx, "Xvfb", display, "-screen", "0", "1280x800x24")
	if err := xvfb.Start(); err != nil {
		t.Fatalf("start Xvfb: %v", err)
	}
	defer xvfb.Process.Kill()
	// Readiness is proven by the xdpyinfo poll below: if Xvfb failed to
	// start, the poll times out with a clear error.
	x := X11{Display: display}
	waitFor(t, ctx, 15*time.Second, "Xvfb up", func() bool {
		cmd := exec.CommandContext(ctx, "xdpyinfo", "-display", display)
		return cmd.Run() == nil
	})

	gedit := exec.CommandContext(ctx, "gedit")
	gedit.Env = append(os.Environ(), "DISPLAY="+display)
	if err := gedit.Start(); err != nil {
		t.Fatalf("start gedit: %v", err)
	}
	defer gedit.Process.Kill()

	var winID string
	waitFor(t, ctx, 30*time.Second, "gedit window", func() bool {
		id, err := x.WindowID(ctx, "gedit")
		if err == nil {
			winID = id
		}
		return err == nil
	})

	gx, gy, w, h, err := x.WindowGeometry(ctx, winID)
	if err != nil {
		t.Fatalf("window geometry: %v", err)
	}
	t.Logf("gedit window %s at %d,%d %dx%d on %s", winID, gx, gy, w, h, display)

	// Wait for the screen to settle (window fully painted) so the
	// before/after diff isolates the typing, not the initial render.
	before := waitStable(t, ctx, x, 15*time.Second)

	// Act: focus the window, click into the text area, type.
	if err := x.WindowFocus(ctx, winID); err != nil {
		t.Fatalf("window focus: %v", err)
	}
	if err := x.Click(ctx, gx+w/2, gy+h/2); err != nil {
		t.Fatalf("click: %v", err)
	}
	const typed = "HELLO FIVEAGENT POC 12345"
	if err := x.Type(ctx, typed); err != nil {
		t.Fatalf("type: %v", err)
	}

	// Verify 1: the window really holds the X input focus.
	focused, err := x.FocusedWindow(ctx)
	if err != nil {
		t.Fatalf("get focus: %v", err)
	}
	fid, ferr1 := strconv.ParseInt(focused, 0, 64)
	wid, ferr2 := strconv.ParseInt(winID, 0, 64)
	if ferr1 != nil || ferr2 != nil || fid != wid {
		t.Fatalf("input focus on %s, want gedit window %s", focused, winID)
	}

	// Verify 2: select all + copy inside gedit, read the X clipboard.
	if err := x.Key(ctx, "ctrl+a"); err != nil {
		t.Fatalf("ctrl+a: %v", err)
	}
	if err := x.Key(ctx, "ctrl+c"); err != nil {
		t.Fatalf("ctrl+c: %v", err)
	}
	var clip string
	waitFor(t, ctx, 10*time.Second, "clipboard holds typed text", func() bool {
		cmd := exec.CommandContext(ctx, "xclip", "-o", "-selection", "clipboard")
		cmd.Env = append(os.Environ(), "DISPLAY="+display)
		out, err := cmd.Output()
		if err == nil {
			clip = string(out)
		}
		return err == nil && strings.TrimSpace(clip) == typed
	})
	t.Logf("clipboard round-trip verified: %q", clip)

	// Verify 3: the screenshot changed visibly inside the window.
	after, err := x.Capture(ctx)
	if err != nil {
		t.Fatalf("capture after: %v", err)
	}
	changed, bbox := diffPixels(t, before, after)
	t.Logf("visible change: %d pixels differ, bbox %v", changed, bbox)
	if changed < 1000 {
		t.Fatalf("only %d pixels changed; typing left no visible trace", changed)
	}
	winRect := image.Rect(gx, gy, gx+w, gy+h)
	if !bbox.Overlaps(winRect) {
		t.Fatalf("change bbox %v does not overlap gedit window %v", bbox, winRect)
	}

	if dir := os.Getenv("VMGUI_ARTIFACTS"); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err == nil {
			os.WriteFile(filepath.Join(dir, "before.png"), before, 0o644)
			os.WriteFile(filepath.Join(dir, "after.png"), after, 0o644)
			t.Logf("artifacts in %s", dir)
		}
	}
}

// waitStable captures until two consecutive screenshots are byte-identical
// (or the timeout runs out) and returns the settled capture.
func waitStable(t *testing.T, ctx context.Context, x X11, d time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(d)
	var prev []byte
	for time.Now().Before(deadline) {
		cur, err := x.Capture(ctx)
		if err != nil {
			t.Fatalf("capture while settling: %v", err)
		}
		if prev != nil && bytes.Equal(prev, cur) {
			return cur
		}
		prev = cur
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("screen never settled within %s", d)
	return nil
}

func freeDisplay(t *testing.T) string {
	t.Helper()
	for n := 90; n < 110; n++ {
		d := ":" + strconv.Itoa(n)
		if _, err := os.Stat("/tmp/.X" + strconv.Itoa(n) + "-lock"); os.IsNotExist(err) {
			return d
		}
	}
	t.Fatal("no free X display")
	return ""
}

func waitFor(t *testing.T, ctx context.Context, d time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			t.Fatalf("timeout waiting for %s: %v", what, ctx.Err())
		}
		if ok() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("timed out (%s) waiting for %s", d, what)
}

// diffPixels counts pixels whose channels differ by more than 32 and
// returns the count and their bounding box.
func diffPixels(t *testing.T, aPNG, bPNG []byte) (int, image.Rectangle) {
	t.Helper()
	a, err := png.Decode(bytes.NewReader(aPNG))
	if err != nil {
		t.Fatalf("decode before.png: %v", err)
	}
	b, err := png.Decode(bytes.NewReader(bPNG))
	if err != nil {
		t.Fatalf("decode after.png: %v", err)
	}
	if !a.Bounds().Eq(b.Bounds()) {
		t.Fatalf("screenshot sizes differ: %v vs %v", a.Bounds(), b.Bounds())
	}
	abs := func(v int) int {
		if v < 0 {
			return -v
		}
		return v
	}
	count := 0
	bbox := image.Rectangle{Min: a.Bounds().Max, Max: a.Bounds().Min}
	for y := a.Bounds().Min.Y; y < a.Bounds().Max.Y; y++ {
		for x := a.Bounds().Min.X; x < a.Bounds().Max.X; x++ {
			ar, ag, ab, _ := a.At(x, y).RGBA()
			br, bg, bb, _ := b.At(x, y).RGBA()
			if abs(int(ar>>8)-int(br>>8)) > 32 || abs(int(ag>>8)-int(bg>>8)) > 32 || abs(int(ab>>8)-int(bb>>8)) > 32 {
				count++
				if x < bbox.Min.X {
					bbox.Min.X = x
				}
				if y < bbox.Min.Y {
					bbox.Min.Y = y
				}
				if x > bbox.Max.X {
					bbox.Max.X = x
				}
				if y > bbox.Max.Y {
					bbox.Max.Y = y
				}
			}
		}
	}
	return count, bbox
}
