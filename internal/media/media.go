// Package media turns inbound media bytes into text the agent can work
// with: audio through a transcription service (whisper.cpp server),
// images through a vision-language model (any OpenAI-compatible
// endpoint). Both are plain HTTP so the agent binary stays pure Go -
// the heavy models run as separate services the operator configures.
//
// Privacy: media bytes and transcripts are NEVER logged here or by
// callers - only byte counts and media ids.
package media

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

// Transcriber turns audio bytes into text (e.g. a voice note).
type Transcriber interface {
	Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error)
}

// Describer turns an image into a short text description.
type Describer interface {
	Describe(ctx context.Context, image []byte, mimeType, caption string) (string, error)
}

// HTTPTranscriber posts audio to a whisper.cpp server
// (https://github.com/ggml-org/whisper.cpp): POST /inference with the
// audio as multipart field "file", JSON {"text": "..."} back.
type HTTPTranscriber struct {
	URL    string // e.g. "http://localhost:8090/inference"
	Client *http.Client
}

func (t HTTPTranscriber) client() *http.Client {
	if t.Client != nil {
		return t.Client
	}
	return &http.Client{Timeout: 120 * time.Second}
}

func (t HTTPTranscriber) Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", "audio"+extFor(mimeType))
	if err != nil {
		return "", err
	}
	if _, err := part.Write(audio); err != nil {
		return "", err
	}
	_ = mw.WriteField("response_format", "json")
	if err := mw.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.URL, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := t.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("transcriber: %s", resp.Status)
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("transcriber: %w", err)
	}
	return out.Text, nil
}

// HTTPDescriber asks a vision-language model over an OpenAI-compatible
// /v1/chat/completions endpoint to describe an image.
type HTTPDescriber struct {
	URL    string // e.g. "http://localhost:8091/v1/chat/completions"
	Model  string // VL model name as the endpoint expects it
	APIKey string // optional bearer token
	Client *http.Client
}

func (d HTTPDescriber) client() *http.Client {
	if d.Client != nil {
		return d.Client
	}
	return &http.Client{Timeout: 120 * time.Second}
}

func (d HTTPDescriber) Describe(ctx context.Context, image []byte, mimeType, caption string) (string, error) {
	question := "Describe this image briefly in Spanish for a chat agent."
	if caption != "" {
		question = "The user sent this image with the caption " + fmt.Sprintf("%q", caption) + ". Describe it briefly in Spanish."
	}
	body := map[string]any{
		"model": d.Model,
		"messages": []map[string]any{{
			"role": "user",
			"content": []map[string]any{
				{"type": "text", "text": question},
				{"type": "image_url", "image_url": map[string]string{
					"url": "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(image),
				}},
			},
		}},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.URL, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if d.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+d.APIKey)
	}
	resp, err := d.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("describer: %s", resp.Status)
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return "", fmt.Errorf("describer: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("describer: empty response")
	}
	return parsed.Choices[0].Message.Content, nil
}

// Synthesizer turns reply text into audio bytes for an outbound
// voice note.
type Synthesizer interface {
	Synthesize(ctx context.Context, text string) (audio []byte, mimeType string, err error)
}

// HTTTSynthesizer posts text to an OpenAI-compatible /v1/audio/speech
// endpoint: openedai-speech (Piper) and kokoro-fastapi both speak this
// protocol, so one client covers the local Spanish TTS options.
type HTTTSynthesizer struct {
	URL    string // e.g. "http://localhost:8100/v1/audio/speech"
	Model  string // model name the endpoint expects (e.g. "piper", "kokoro")
	Voice  string // voice id as the endpoint expects it
	APIKey string // optional bearer token
	Client *http.Client
}

func (s HTTTSynthesizer) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return &http.Client{Timeout: 120 * time.Second}
}

func (s HTTTSynthesizer) Synthesize(ctx context.Context, text string) ([]byte, string, error) {
	body, err := json.Marshal(map[string]any{
		"model":           s.Model,
		"voice":           s.Voice,
		"input":           text,
		"response_format": "opus",
	})
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.URL, bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.APIKey)
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	audio, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("tts: %s", resp.Status)
	}
	if len(audio) == 0 {
		return nil, "", fmt.Errorf("tts: empty audio")
	}
	mimeType := resp.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "audio/ogg"
	}
	return audio, mimeType, nil
}

// extFor maps common audio MIME types to a file extension the
// transcription server can sniff.
func extFor(mimeType string) string {
	switch mimeType {
	case "audio/ogg", "audio/ogg; codecs=opus":
		return ".ogg"
	case "audio/mpeg":
		return ".mp3"
	case "audio/mp4", "audio/aac":
		return ".m4a"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "audio/webm":
		return ".webm"
	default:
		return ".bin"
	}
}
