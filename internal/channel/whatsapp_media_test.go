package channel

// Stage 25 (media pipeline): inbound voice notes and images are
// downloaded with the authenticated Graph two-step and turned into
// text by pluggable processors; outbound media round-trips through
// UploadMedia + send-by-id. Every Graph call goes to a fake server -
// a real graph.facebook.com call in tests would leak tokens into
// access logs.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/media"
)

type stubTranscriber struct {
	got []byte
	out string
}

func (s *stubTranscriber) Transcribe(_ context.Context, audio []byte, _ string) (string, error) {
	s.got = audio
	return s.out, nil
}

type stubDescriber struct {
	got []byte
	out string
}

func (s *stubDescriber) Describe(_ context.Context, image []byte, _, caption string) (string, error) {
	s.got = image
	return s.out, nil
}

// fakeMediaGraph serves the Graph endpoints media work touches:
// GET /{mediaID} -> download URL, GET the download URL -> bytes,
// POST /{phoneID}/media -> upload, POST /{phoneID}/messages -> send.
// It records auth headers so tests can prove every call went out
// authenticated, and the upload/send bodies for the outbound case.
type fakeMediaGraph struct {
	mu           sync.Mutex
	authFailures []string
	uploaded     []byte
	sentBody     map[string]any
	// sentByType keeps the last message body per payload type, so the
	// status reactions (eyes/check) posted around a reply cannot race
	// the reply body itself in assertions.
	sentByType map[string]map[string]any
	mediaBytes []byte
}

func (f *fakeMediaGraph) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(rw http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			f.mu.Lock()
			f.authFailures = append(f.authFailures, r.Method+" "+r.URL.Path)
			f.mu.Unlock()
		}
		rw.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/m123":
			fmt.Fprintf(rw, `{"url":"%s","mime_type":"audio/ogg"}`, "http://"+r.Host+"/bin/m123")
		case r.Method == http.MethodGet && r.URL.Path == "/bin/m123":
			rw.Write(f.mediaBytes)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/media"):
			raw, _ := io.ReadAll(r.Body)
			f.mu.Lock()
			f.uploaded = raw
			f.mu.Unlock()
			rw.Write([]byte(`{"id":"mid-out-1"}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/messages"):
			var body map[string]any
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			f.mu.Lock()
			f.sentBody = body
			if f.sentByType == nil {
				f.sentByType = map[string]map[string]any{}
			}
			if typ, _ := body["type"].(string); typ != "" {
				f.sentByType[typ] = body
			}
			f.mu.Unlock()
			rw.Write([]byte(`{"messages":[{"id":"wamid.fake"}]}`))
		default:
			rw.Write([]byte(`{}`))
		}
	})
	return mux
}

// awaitCore polls until the fake core received a turn (processing is
// async after the webhook 200).
func awaitCore(t *testing.T, fc *fakeCore) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		fc.mu.Lock()
		got := fc.text
		fc.mu.Unlock()
		if got != "" {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("core was never called")
	return ""
}

func postWebhook(t *testing.T, w *whatsapp, body string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhook/whatsapp", strings.NewReader(body))
	rec := httptest.NewRecorder()
	w.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("webhook: got %d", rec.Code)
	}
}

func TestMediaVoiceNoteTranscribed(t *testing.T) {
	graph := &fakeMediaGraph{mediaBytes: []byte("fake-opus-bytes")}
	srv := httptest.NewServer(graph.handler())
	t.Cleanup(srv.Close)
	fc := &fakeCore{}
	w := NewWhatsApp(config.Channel{
		VerifyToken: "secret-token", PhoneNumberID: "123", AccessToken: "token",
	}, fc).(*whatsapp)
	w.baseURL = srv.URL
	w.debounce = 10 * time.Millisecond
	st := &stubTranscriber{out: "hola, quiero una mesa para dos"}
	w.transcriber = st

	postWebhook(t, w, `{"entry":[{"changes":[{"value":{"messages":[{"from":"34600123456","id":"wamid.1","type":"audio","audio":{"id":"m123","mime_type":"audio/ogg"}}]}}]}]}`)
	got := awaitCore(t, fc)
	if got != "[voice note] hola, quiero una mesa para dos" {
		t.Fatalf("core got %q", got)
	}
	if string(st.got) != "fake-opus-bytes" {
		t.Fatalf("transcriber got %d bytes", len(st.got))
	}
	graph.mu.Lock()
	defer graph.mu.Unlock()
	if len(graph.authFailures) > 0 {
		t.Fatalf("unauthenticated Graph calls: %v", graph.authFailures)
	}
}

func TestMediaVoiceNoteNoTranscriber(t *testing.T) {
	fc := &fakeCore{}
	w := newTestWhatsApp(t, fc)
	w.debounce = 10 * time.Millisecond
	postWebhook(t, w, `{"entry":[{"changes":[{"value":{"messages":[{"from":"34600123456","id":"wamid.1","type":"audio","audio":{"id":"m123","mime_type":"audio/ogg"}}]}}]}]}`)
	if got := awaitCore(t, fc); got != "[voice note - transcription not configured]" {
		t.Fatalf("core got %q", got)
	}
}

func TestMediaImageDescribed(t *testing.T) {
	graph := &fakeMediaGraph{mediaBytes: []byte("fake-jpeg")}
	srv := httptest.NewServer(graph.handler())
	t.Cleanup(srv.Close)
	fc := &fakeCore{}
	w := NewWhatsApp(config.Channel{
		VerifyToken: "secret-token", PhoneNumberID: "123", AccessToken: "token",
	}, fc).(*whatsapp)
	w.baseURL = srv.URL
	w.debounce = 10 * time.Millisecond
	sd := &stubDescriber{out: "un gato negro sobre un sofa"}
	w.describer = sd

	postWebhook(t, w, `{"entry":[{"changes":[{"value":{"messages":[{"from":"34600123456","id":"wamid.1","type":"image","image":{"id":"m123","mime_type":"image/jpeg","caption":"mira"}}]}}]}]}`)
	if got := awaitCore(t, fc); got != "[image: un gato negro sobre un sofa]" {
		t.Fatalf("core got %q", got)
	}
	if string(sd.got) != "fake-jpeg" {
		t.Fatalf("describer got %d bytes", len(sd.got))
	}
}

func TestMediaUploadAndSendByID(t *testing.T) {
	graph := &fakeMediaGraph{}
	srv := httptest.NewServer(graph.handler())
	t.Cleanup(srv.Close)
	w := newTestWhatsApp(t, &fakeCore{})
	w.baseURL = srv.URL

	id, err := w.UploadMedia(context.Background(), "image/png", "chart.png", []byte("png-bytes"))
	if err != nil {
		t.Fatal(err)
	}
	if id != "mid-out-1" {
		t.Fatalf("upload id %q", id)
	}
	graph.mu.Lock()
	if !strings.Contains(string(graph.uploaded), "png-bytes") {
		t.Fatalf("upload body missing bytes: %d bytes", len(graph.uploaded))
	}
	graph.mu.Unlock()

	if _, err := w.post(context.Background(), map[string]any{
		"messaging_product": "whatsapp",
		"to":                "34600123456",
		"type":              "image",
		"image":             map[string]any{"id": id},
	}); err != nil {
		t.Fatal(err)
	}
	graph.mu.Lock()
	defer graph.mu.Unlock()
	img, ok := graph.sentBody["image"].(map[string]any)
	if !ok || img["id"] != "mid-out-1" {
		t.Fatalf("send body: %v", graph.sentBody)
	}
	if graph.sentBody["to"] != "34600123456" {
		t.Fatalf("send to: %v", graph.sentBody["to"])
	}
	if len(graph.authFailures) > 0 {
		t.Fatalf("unauthenticated Graph calls: %v", graph.authFailures)
	}
}

// Transcription failures must surface to the agent as an honest note,
// never as a silent drop or a fake transcript.
func TestMediaTranscriptionFailureHonest(t *testing.T) {
	graph := &fakeMediaGraph{mediaBytes: []byte("x")}
	srv := httptest.NewServer(graph.handler())
	t.Cleanup(srv.Close)
	fc := &fakeCore{}
	w := NewWhatsApp(config.Channel{
		VerifyToken: "secret-token", PhoneNumberID: "123", AccessToken: "token",
	}, fc).(*whatsapp)
	w.baseURL = srv.URL
	w.debounce = 10 * time.Millisecond
	w.transcriber = &stubTranscriber{out: ""} // empty transcript = failure

	postWebhook(t, w, `{"entry":[{"changes":[{"value":{"messages":[{"from":"34600123456","id":"wamid.1","type":"audio","audio":{"id":"m123","mime_type":"audio/ogg"}}]}}]}]}`)
	if got := awaitCore(t, fc); got != "[voice note - transcription failed]" {
		t.Fatalf("core got %q", got)
	}
}

// Stage 25d (outbound voice): with a TTS endpoint configured the reply
// goes out as a native voice note - synthesize, upload, send by id.
// The fake TTS server asserts it received the exact reply text and the
// fake Graph server asserts the uploaded bytes are the TTS output, so
// the test fails if the voice path never fires.
func TestTTSReplyVoiceNote(t *testing.T) {
	graph := &fakeMediaGraph{}
	graphSrv := httptest.NewServer(graph.handler())
	t.Cleanup(graphSrv.Close)

	var ttsMu sync.Mutex
	var ttsInput string
	ttsSrv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		var body struct {
			Input string `json:"input"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		ttsMu.Lock()
		ttsInput = body.Input
		ttsMu.Unlock()
		rw.Header().Set("Content-Type", "audio/ogg")
		rw.Write([]byte("fake-opus-out"))
	}))
	t.Cleanup(ttsSrv.Close)

	fc := &fakeCore{}
	w := NewWhatsApp(config.Channel{
		VerifyToken: "secret-token", PhoneNumberID: "123", AccessToken: "token",
	}, fc).(*whatsapp)
	w.baseURL = graphSrv.URL
	w.debounce = 10 * time.Millisecond
	w.tts = media.HTTTSynthesizer{URL: ttsSrv.URL, Model: "piper", Voice: "es-default"}

	postWebhook(t, w, `{"entry":[{"changes":[{"value":{"messages":[{"from":"34600123456","id":"wamid.1","type":"text","text":{"body":"hola"}}]}}]}]}`)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		graph.mu.Lock()
		sent := graph.sentByType["audio"]
		graph.mu.Unlock()
		if sent != nil {
			audio, ok := sent["audio"].(map[string]any)
			if !ok || audio["id"] != "mid-out-1" {
				t.Fatalf("send body: %v", sent)
			}
			graph.mu.Lock()
			up := string(graph.uploaded)
			graph.mu.Unlock()
			if !strings.Contains(up, "fake-opus-out") {
				t.Fatalf("upload missing tts bytes: %d bytes", len(up))
			}
			ttsMu.Lock()
			in := ttsInput
			ttsMu.Unlock()
			if in != "ok reply" {
				t.Fatalf("tts got input %q", in)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("voice note was never sent")
}

// A TTS failure must degrade to the plain text reply, never to
// silence or a fake send.
func TestTTSFailureFallsBackToText(t *testing.T) {
	graph := &fakeMediaGraph{}
	graphSrv := httptest.NewServer(graph.handler())
	t.Cleanup(graphSrv.Close)
	ttsSrv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(ttsSrv.Close)

	fc := &fakeCore{}
	w := NewWhatsApp(config.Channel{
		VerifyToken: "secret-token", PhoneNumberID: "123", AccessToken: "token",
	}, fc).(*whatsapp)
	w.baseURL = graphSrv.URL
	w.debounce = 10 * time.Millisecond
	w.tts = media.HTTTSynthesizer{URL: ttsSrv.URL, Model: "piper", Voice: "es"}

	postWebhook(t, w, `{"entry":[{"changes":[{"value":{"messages":[{"from":"34600123456","id":"wamid.1","type":"text","text":{"body":"hola"}}]}}]}]}`)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		graph.mu.Lock()
		sent := graph.sentByType["text"]
		graph.mu.Unlock()
		if sent != nil {
			txt, ok := sent["text"].(map[string]any)
			if !ok || txt["body"] != "ok reply" {
				t.Fatalf("fallback body: %v", sent)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("text fallback was never sent")
}

// Stage 25e: code-made artifacts (charts) go out as native images -
// upload the bytes, send by id with the caption.
func TestSendMediaBytesImage(t *testing.T) {
	graph := &fakeMediaGraph{}
	srv := httptest.NewServer(graph.handler())
	t.Cleanup(srv.Close)
	w := newTestWhatsApp(t, &fakeCore{})
	w.baseURL = srv.URL

	if err := w.SendMediaBytes(context.Background(), "34600123456", "image/png", "las ventas", []byte("real-png-bytes")); err != nil {
		t.Fatal(err)
	}
	graph.mu.Lock()
	defer graph.mu.Unlock()
	if !strings.Contains(string(graph.uploaded), "real-png-bytes") {
		t.Fatalf("upload missing bytes: %d bytes", len(graph.uploaded))
	}
	img := graph.sentByType["image"]
	if img == nil {
		t.Fatalf("no image send recorded: %v", graph.sentByType)
	}
	body, ok := img["image"].(map[string]any)
	if !ok || body["id"] != "mid-out-1" || body["caption"] != "las ventas" {
		t.Fatalf("send body: %v", img)
	}
	if len(graph.authFailures) > 0 {
		t.Fatalf("unauthenticated Graph calls: %v", graph.authFailures)
	}
}

type stubVideoExtractor struct {
	got    []byte
	frames [][]byte
	audio  []byte
}

func (s *stubVideoExtractor) Extract(_ context.Context, video []byte, _ int) ([][]byte, []byte, error) {
	s.got = video
	return s.frames, s.audio, nil
}

// Stage 25c: an inbound video arrives as transcript + frame
// descriptions, reusing the transcriber and describer processors.
func TestMediaVideoAnalyzed(t *testing.T) {
	graph := &fakeMediaGraph{mediaBytes: []byte("fake-mp4")}
	srv := httptest.NewServer(graph.handler())
	t.Cleanup(srv.Close)
	fc := &fakeCore{}
	w := NewWhatsApp(config.Channel{
		VerifyToken: "secret-token", PhoneNumberID: "123", AccessToken: "token",
	}, fc).(*whatsapp)
	w.baseURL = srv.URL
	w.debounce = 10 * time.Millisecond
	ve := &stubVideoExtractor{frames: [][]byte{[]byte("f1"), []byte("f2")}, audio: []byte("wav")}
	w.video = ve
	st := &stubTranscriber{out: "el perro corre por el parque"}
	sd := &stubDescriber{out: "un perro marron"}
	w.transcriber = st
	w.describer = sd

	postWebhook(t, w, `{"entry":[{"changes":[{"value":{"messages":[{"from":"34600123456","id":"wamid.1","type":"video","video":{"id":"m123","mime_type":"video/mp4","caption":"mira"}}]}}]}]}`)
	got := awaitCore(t, fc)
	want := "[video] audio: el perro corre por el parque | frames: un perro marron / un perro marron"
	if got != want {
		t.Fatalf("core got %q, want %q", got, want)
	}
	if string(ve.got) != "fake-mp4" {
		t.Fatalf("extractor got %d bytes", len(ve.got))
	}
	if string(st.got) != "wav" {
		t.Fatalf("transcriber got %q", st.got)
	}
	graph.mu.Lock()
	defer graph.mu.Unlock()
	if len(graph.authFailures) > 0 {
		t.Fatalf("unauthenticated Graph calls: %v", graph.authFailures)
	}
}

// Without an extractor the video falls back to the plain bracket
// announcement; with an extractor but no processors the agent gets an
// honest "not configured" note, never an empty analysis.
func TestMediaVideoHonestFallbacks(t *testing.T) {
	fc := &fakeCore{}
	w := newTestWhatsApp(t, fc)
	w.debounce = 10 * time.Millisecond
	w.video = nil // explicit: this case tests the no-extractor fallback
	postWebhook(t, w, `{"entry":[{"changes":[{"value":{"messages":[{"from":"34600123456","id":"wamid.1","type":"video","video":{"id":"m123","mime_type":"video/mp4","caption":"mira"}}]}}]}]}`)
	if got := awaitCore(t, fc); got != "[video: mira]" {
		t.Fatalf("no extractor: core got %q", got)
	}

	graph := &fakeMediaGraph{mediaBytes: []byte("fake-mp4")}
	srv := httptest.NewServer(graph.handler())
	t.Cleanup(srv.Close)
	fc2 := &fakeCore{}
	w2 := NewWhatsApp(config.Channel{
		VerifyToken: "secret-token", PhoneNumberID: "123", AccessToken: "token",
	}, fc2).(*whatsapp)
	w2.baseURL = srv.URL
	w2.debounce = 10 * time.Millisecond
	w2.video = &stubVideoExtractor{frames: [][]byte{[]byte("f1")}, audio: []byte("wav")}
	postWebhook(t, w2, `{"entry":[{"changes":[{"value":{"messages":[{"from":"34600123456","id":"wamid.1","type":"video","video":{"id":"m123","mime_type":"video/mp4"}}]}}]}]}`)
	if got := awaitCore(t, fc2); got != "[video - analysis not configured]" {
		t.Fatalf("no processors: core got %q", got)
	}
}
