package tools_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

// The send must carry the exact rendered PNG bytes and the request's
// channel/user: the test fails if the chart never leaves the process.
func TestSendChartSendsPNG(t *testing.T) {
	var gotChannel, gotUser, gotMime, gotCaption string
	var gotBytes []byte
	send := func(_ context.Context, channel, userID, mimeType, caption string, data []byte) error {
		gotChannel, gotUser, gotMime, gotCaption = channel, userID, mimeType, caption
		gotBytes = data
		return nil
	}
	tool := tools.SendChart{Send: send}
	ctx := tools.WithRequestInfo(context.Background(), "whatsapp", "34600000000")
	out, err := tool.Execute(ctx, json.RawMessage(`{
		"title": "Ventas", "kind": "bar",
		"labels": ["ene", "feb"], "values": [3, 7],
		"caption": "ahi va"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "chart sent") {
		t.Fatalf("output %q", out)
	}
	if gotChannel != "whatsapp" || gotUser != "34600000000" {
		t.Fatalf("routing %s/%s", gotChannel, gotUser)
	}
	if gotMime != "image/png" || gotCaption != "ahi va" {
		t.Fatalf("mime/caption %s %q", gotMime, gotCaption)
	}
	if !bytes.HasPrefix(gotBytes, []byte("\x89PNG")) || len(gotBytes) < 500 {
		t.Fatalf("not a real chart PNG: %d bytes", len(gotBytes))
	}
}

func TestSendChartValidation(t *testing.T) {
	tool := tools.SendChart{Send: func(context.Context, string, string, string, string, []byte) error {
		t.Fatal("send must not fire on invalid input")
		return nil
	}}
	ctx := tools.WithRequestInfo(context.Background(), "whatsapp", "34600000000")
	for _, args := range []string{
		`{"kind": "pie", "values": [1]}`,
		`{"kind": "bar"}`,
		`{"kind": "bar", "labels": ["a"], "values": [1, 2]}`,
	} {
		if _, err := tool.Execute(ctx, json.RawMessage(args)); err == nil {
			t.Fatalf("expected error for %s", args)
		}
	}
}

// A send failure must surface to the model as an error, not a success
// message - otherwise the agent would claim the image was sent.
func TestSendChartSendFailureHonest(t *testing.T) {
	tool := tools.SendChart{Send: func(context.Context, string, string, string, string, []byte) error {
		return context.DeadlineExceeded
	}}
	ctx := tools.WithRequestInfo(context.Background(), "whatsapp", "34600000000")
	_, err := tool.Execute(ctx, json.RawMessage(`{"kind": "line", "values": [1, 2]}`))
	if err == nil || !strings.Contains(err.Error(), "send failed") {
		t.Fatalf("expected honest send failure, got %v", err)
	}
}
