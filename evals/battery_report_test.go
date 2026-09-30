package evals

// Stage 24a/25e CI proof for the runner report: the page carries the
// run's metrics, the embedded chart is the send_chart renderer's
// output BYTE FOR BYTE (one renderer for chat-sent and report-embedded
// charts), and the page only leaves the bot through the signed-link +
// PIN gate. A report that skips the renderer or the gate fails here.

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/batteryreport"
	"github.com/FiveTechSoft/FiveAgent/internal/chart"
	"github.com/FiveTechSoft/FiveAgent/internal/links"
)

func TestBatteryReportThroughLinks(t *testing.T) {
	stats := []batteryreport.CategoryStat{
		{Name: "memory", Pass: 8, Abstention: 1, Miss: 1, Halluc: 0, Total: 10, Writes: 6, WriteTotal: 7},
		{Name: "tools", Pass: 5, Miss: 0, Halluc: 1, Total: 6, JudgeScore: 9, JudgeCount: 5},
	}
	page, err := batteryreport.RenderHTML("Batería de prueba", "qwen3.5:9b", time.Now(), stats)
	if err != nil {
		t.Fatal(err)
	}
	// The run's metrics are on the page.
	for _, tok := range []string{"memory", "tools", "TOTAL", "6/7", "9/10", "qwen3.5:9b", "13</b>", "15"} {
		if !strings.Contains(page, tok) {
			t.Fatalf("report page missing %q", tok)
		}
	}
	// The chart is the send_chart renderer's output, byte for byte:
	// if the report ever draws its own chart, this fails.
	wantPNG, err := chart.Render(batteryreport.PassChartSpec(stats))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page, "data:image/png;base64,"+base64.StdEncoding.EncodeToString(wantPNG)) {
		t.Fatal("report chart is not the send_chart renderer's output")
	}
	// The pass rate of the fixture is real: memory 80%, tools ~83.3%.
	spec := batteryreport.PassChartSpec(stats)
	if len(spec.Values) != 2 || spec.Values[0] != 80 || spec.Values[1] < 83 || spec.Values[1] > 84 {
		t.Fatalf("pass chart carries wrong data: %+v", spec)
	}

	// 24a: mint and serve through the real signed-link machinery.
	dir := t.TempDir()
	svc, err := links.Open(dir+"/secret", "http://test", dir+"/pages", dir+"/vault", nil)
	if err != nil {
		t.Fatal(err)
	}
	link, pin, err := svc.MintReportHTML("battery", "Batería de prueba", page)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(svc.Handler())
	t.Cleanup(srv.Close)
	url := strings.Replace(link, "http://test", srv.URL, 1)

	// No PIN: the gate holds.
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("report served without PIN: %d", resp.StatusCode)
	}
	// Wrong PIN: the gate holds.
	resp, err = http.Get(url + "?pin=0000")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("report served with wrong PIN: %d", resp.StatusCode)
	}
	// Right PIN: the exact page comes back.
	resp, err = http.Get(url + "?pin=" + pin)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("report not served with correct PIN: %d", resp.StatusCode)
	}
	if string(body) != page {
		t.Fatal("served page differs from the minted report")
	}
}
