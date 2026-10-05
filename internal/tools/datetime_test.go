package tools_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

// 23:46 UTC on Sunday 4 October is 01:46 on Monday 5 October in a
// UTC+2 zone: the late-night window where a UTC default named the
// wrong weekday (battery run 16 reported domingo for lunes).
func lateNight() time.Time { return time.Date(2026, 10, 4, 23, 46, 5, 0, time.UTC) }

// hostZone pins time.Local to UTC+9 for the test, so a result can only
// come from the configured zone or the explicit argument, never from
// whatever zone the CI host happens to use.
func hostZone(t *testing.T) {
	t.Helper()
	old := time.Local
	time.Local = time.FixedZone("JST", 9*3600)
	t.Cleanup(func() { time.Local = old })
}

func TestDatetimeDefaultsToUserZoneNotUTC(t *testing.T) {
	hostZone(t)
	madrid := time.FixedZone("CEST", 2*3600)
	dt := tools.Datetime{Zone: madrid, Now: lateNight}
	out, err := dt.Execute(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "Monday, 5 October 2026, 01:46:05") {
		t.Errorf("default zone must be the user's, got %q", out)
	}
}

func TestDatetimeExplicitZoneStillWins(t *testing.T) {
	hostZone(t)
	dt := tools.Datetime{Zone: time.FixedZone("CEST", 2*3600), Now: lateNight}
	out, err := dt.Execute(context.Background(), []byte(`{"timezone":"UTC"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "Sunday, 4 October 2026, 23:46:05") {
		t.Errorf("explicit UTC must be honored, got %q", out)
	}
	if _, err := dt.Execute(context.Background(), []byte(`{"timezone":"Nowhere/Land"}`)); err == nil {
		t.Error("unknown zone must still be an error")
	}
}

// With no configured zone the host's local zone applies, never UTC by
// construction: cron and calendar already read time.Local.
func TestDatetimeUnsetZoneUsesHostLocal(t *testing.T) {
	old := time.Local
	defer func() { time.Local = old }()
	time.Local = time.FixedZone("CEST", 2*3600)
	out, err := tools.Datetime{Now: lateNight}.Execute(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "Monday, 5 October 2026, 01:46:05") {
		t.Errorf("unset zone must use the host local zone, got %q", out)
	}
}
