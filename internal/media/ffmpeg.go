package media

// FFmpeg turns video bytes into the pieces the existing processors
// understand: a few JPEG key frames for the vision model and a WAV
// audio track for the transcriber. ffmpeg is an EXTERNAL tool the
// operator installs (like the whisper/VL/TTS services), so the agent
// binary stays pure Go. Stage 25c.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// VideoExtractor turns video bytes into key frames and an audio
// track; FFmpeg implements it, tests stub it.
type VideoExtractor interface {
	Extract(ctx context.Context, video []byte, frames int) (frameJPEGs [][]byte, audioWAV []byte, err error)
}

// FFmpeg locates and drives the ffmpeg binary.
type FFmpeg struct {
	Path string // empty resolves ffmpeg on PATH at construction time
}

// NewFFmpeg returns an extractor, or an error when no ffmpeg binary is
// available (video analysis then reports as not configured).
func NewFFmpeg(path string) (FFmpeg, error) {
	if path == "" {
		resolved, err := exec.LookPath("ffmpeg")
		if err != nil {
			return FFmpeg{}, fmt.Errorf("ffmpeg not found on PATH: %w", err)
		}
		path = resolved
	}
	return FFmpeg{Path: path}, nil
}

// Extract pulls up to frames JPEG stills (evenly spread via an fps
// filter) and the audio track as 16kHz mono WAV, the format
// transcription servers prefer. Everything happens in a temp dir that
// is always removed.
func (f FFmpeg) Extract(ctx context.Context, video []byte, frames int) (frameJPEGs [][]byte, audioWAV []byte, err error) {
	if frames < 1 {
		frames = 1
	}
	if frames > 8 {
		frames = 8
	}
	dir, err := os.MkdirTemp("", "fiveagent-video-")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(dir)

	in := filepath.Join(dir, "in.mp4")
	if err := os.WriteFile(in, video, 0o600); err != nil {
		return nil, nil, err
	}

	run := func(args ...string) error {
		if err := execFFmpeg(ctx, dir, f.Path, args...); err != nil {
			return fmt.Errorf("ffmpeg %v: %w", args[:2], err)
		}
		return nil
	}

	// Key frames: one frame every 3 seconds, capped, scaled to a
	// vision-friendly width.
	framePattern := filepath.Join(dir, "frame-%02d.jpg")
	if err := run("-i", in, "-vf", "fps=1/3,scale=640:-2", "-frames:v", fmt.Sprint(frames), "-f", "image2", framePattern); err != nil {
		return nil, nil, err
	}
	stills, _ := filepath.Glob(filepath.Join(dir, "frame-*.jpg"))
	for _, s := range stills {
		b, readErr := os.ReadFile(s)
		if readErr == nil && len(b) > 0 {
			frameJPEGs = append(frameJPEGs, b)
		}
	}
	if len(frameJPEGs) == 0 {
		return nil, nil, fmt.Errorf("ffmpeg produced no frames")
	}

	// Audio track; a video without audio is fine, frames alone carry on.
	wav := filepath.Join(dir, "audio.wav")
	if err := run("-i", in, "-vn", "-acodec", "pcm_s16le", "-ar", "16000", "-ac", "1", "-f", "wav", wav); err == nil {
		if b, readErr := os.ReadFile(wav); readErr == nil && len(b) > 44 {
			audioWAV = b
		}
	}
	return frameJPEGs, audioWAV, nil
}

// execFFmpeg runs one ffmpeg command inside dir. It is a package
// variable so tests can fake the binary cross-platform (CI has no
// ffmpeg); the real invocation is covered by live verification.
var execFFmpeg = func(ctx context.Context, dir, path string, args ...string) error {
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cctx, path, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w (%d bytes of output, not logged)", err, len(out))
	}
	return nil
}
