package tools

// send_chart (stage 25e): the model renders a small bar or line chart
// from data it already has and the image goes out as native chat media
// (WhatsApp: upload + send by id). The renderer is pure Go (internal/
// chart), so no external service or runtime is involved. Channels
// without media support report an honest error to the model.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/FiveTechSoft/FiveAgent/internal/chart"
)

// MediaSender delivers rendered image bytes to one user on one
// channel (the dispatcher in main wires it to media-capable adapters).
type MediaSender func(ctx context.Context, channel, userID, mimeType, caption string, data []byte) error

// SendChart renders a chart and sends it as an image.
type SendChart struct {
	Send MediaSender
}

func (t SendChart) Name() string { return "send_chart" }

func (t SendChart) Description() string {
	return "Render a small bar or line chart from data you already have and send it as an image. Use it when a picture answers better than a table (trends, comparisons). Do not invent data."
}

func (t SendChart) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"title": {"type": "string", "description": "chart title"},
			"kind": {"type": "string", "enum": ["bar", "line"]},
			"labels": {"type": "array", "items": {"type": "string"}, "description": "x-axis labels, one per value"},
			"values": {"type": "array", "items": {"type": "number"}, "description": "data points, 1 to 40"},
			"caption": {"type": "string", "description": "optional message sent with the image"}
		},
		"required": ["kind", "values"]
	}`)
}

func (t SendChart) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Title   string    `json:"title"`
		Kind    string    `json:"kind"`
		Labels  []string  `json:"labels"`
		Values  []float64 `json:"values"`
		Caption string    `json:"caption"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	if t.Send == nil {
		return "", fmt.Errorf("send_chart: media sending is not configured on this deployment")
	}
	channel, userID := RequestInfo(ctx)
	if userID == "" {
		return "", fmt.Errorf("send_chart: no user in the request context")
	}
	png, err := chart.Render(chart.Spec{
		Title:  a.Title,
		Kind:   a.Kind,
		Labels: a.Labels,
		Values: a.Values,
	})
	if err != nil {
		return "", err
	}
	if err := t.Send(ctx, channel, userID, "image/png", a.Caption, png); err != nil {
		return "", fmt.Errorf("send_chart: chart rendered (%d bytes) but the send failed: %w", len(png), err)
	}
	return fmt.Sprintf("chart sent as image (%d bytes)", len(png)), nil
}
