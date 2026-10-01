package trajectory

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/model"
)

func TestNativeAttemptRedactionAndBounds(t *testing.T) {
	r := Record{}
	r.AddModelAttempt(model.NativeObservation{Thinking: "token: fixture-secret " + strings.Repeat("界", 3000), Content: "private@example.invalid " + strings.Repeat("x", 5000), DoneReason: "length", HTTPStatus: 200})
	RedactRecord(&r)
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "fixture-secret") || strings.Contains(string(b), "private@example.invalid") {
		t.Fatal("private telemetry leaked")
	}
	var decoded Record
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	a := decoded.ModelAttempts[0]
	if a.Sequence != 1 || a.DoneReason != "length" || a.HTTPStatus != 200 || a.ThinkingChars < 9000 || len(a.Thinking) > 4096 || len(a.Content) > 4096 || !a.ThinkingTruncated || !a.ContentTruncated {
		t.Fatalf("lost fields/bounds: %+v", a)
	}
	if len(decoded.Messages) != 0 || decoded.Outcome.ToolRounds != 0 {
		t.Fatal("telemetry altered conversation/tool rounds")
	}
}
