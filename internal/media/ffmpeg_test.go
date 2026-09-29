package media

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeExec writes the files a real ffmpeg would produce, so the
// extraction logic (globbing, reading, error paths) is exercised
// without needing the binary in CI. The real ffmpeg invocation is
// pending live verification.
func fakeExec(t *testing.T, frames int, wavBytes int) {
	t.Helper()
	orig := execFFmpeg
	execFFmpeg = func(ctx context.Context, dir, path string, args ...string) error {
		last := args[len(args)-1]
		switch {
		case strings.Contains(last, "frame-"):
			for i := 1; i <= frames; i++ {
				name := filepath.Join(dir, "frame-0"+string(rune('0'+i))+".jpg")
				if err := os.WriteFile(name, []byte("fake-jpeg-frame"), 0o600); err != nil {
					return err
				}
			}
		case strings.HasSuffix(last, ".wav"):
			if wavBytes > 0 {
				return os.WriteFile(last, make([]byte, wavBytes), 0o600)
			}
		}
		return nil
	}
	t.Cleanup(func() { execFFmpeg = orig })
}

func TestFFmpegExtract(t *testing.T) {
	fakeExec(t, 3, 100)
	ff := FFmpeg{Path: "/fake/ffmpeg"}
	frames, audio, err := ff.Extract(context.Background(), []byte("fake-video"), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 3 {
		t.Fatalf("frames: %d", len(frames))
	}
	if string(frames[0]) != "fake-jpeg-frame" {
		t.Fatalf("frame bytes: %q", frames[0])
	}
	if len(audio) != 100 {
		t.Fatalf("audio: %d bytes", len(audio))
	}
}

// A video without an audio track still yields its frames.
func TestFFmpegNoAudio(t *testing.T) {
	fakeExec(t, 2, 0)
	ff := FFmpeg{Path: "/fake/ffmpeg"}
	frames, audio, err := ff.Extract(context.Background(), []byte("fake-video"), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 || len(audio) != 0 {
		t.Fatalf("frames %d audio %d", len(frames), len(audio))
	}
}

// No frames produced is an error - the agent must never announce a
// successful analysis of nothing.
func TestFFmpegNoFramesFails(t *testing.T) {
	fakeExec(t, 0, 0)
	ff := FFmpeg{Path: "/fake/ffmpeg"}
	if _, _, err := ff.Extract(context.Background(), []byte("fake-video"), 3); err == nil {
		t.Fatal("expected no-frames error")
	}
}
