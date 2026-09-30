// Package batteryreport builds the live-battery run report: the stage
// 24a first use case of make_report_link (the runner mints its report
// through the same signed-link machinery the model uses) and the stage
// 25e battery-chart variation of the send_chart renderer (charts ride
// the report page as embedded PNGs, rendered by internal/chart - one
// renderer for chat-sent and report-embedded charts).
package batteryreport

import (
	"encoding/base64"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/chart"
)

// CategoryStat is one battery category's scoreboard for one run.
type CategoryStat struct {
	Name                                  string
	Pass, Abstention, Miss, Halluc, Total int
	Writes, WriteTotal                    int // M1 write-through
	JudgeScore, JudgeCount                int // judge points / scored prompts
}

// PassChartSpec is the report's bar chart: pass rate per category, in
// the same order the report table lists. Exported so the runner (and
// the CI proof) renders the exact chart the page embeds.
func PassChartSpec(stats []CategoryStat) chart.Spec {
	spec := chart.Spec{Title: "Aciertos por categoría (%)", Kind: "bar"}
	for _, st := range stats {
		spec.Labels = append(spec.Labels, st.Name)
		rate := 0.0
		if st.Total > 0 {
			rate = 100 * float64(st.Pass) / float64(st.Total)
		}
		spec.Values = append(spec.Values, rate)
	}
	return spec
}

// RenderHTML builds the report page. Charts come from chart.Render -
// the send_chart renderer - embedded as data URIs, so the report works
// as a single self-contained page behind the PIN gate.
func RenderHTML(title, modelName string, at time.Time, stats []CategoryStat) (string, error) {
	png, err := chart.Render(PassChartSpec(stats))
	if err != nil {
		return "", fmt.Errorf("batteryreport: pass-rate chart: %w", err)
	}
	var b strings.Builder
	b.WriteString("<!doctype html><html><head><meta charset=utf-8><title>")
	b.WriteString(html.EscapeString(title))
	b.WriteString(`</title><style>body{font-family:sans-serif;max-width:60em;margin:2em auto;padding:0 1em}` +
		`table{border-collapse:collapse}td,th{border:1px solid #ccc;padding:.3em .7em;text-align:right}` +
		`td:first-child,th:first-child{text-align:left}.bad{color:#b00;font-weight:bold}</style></head><body><h1>`)
	b.WriteString(html.EscapeString(title))
	b.WriteString("</h1><p>Modelo: " + html.EscapeString(modelName) + " - " + at.Format("2006-01-02 15:04 MST") + "</p>")
	b.WriteString(`<img alt="pass rate per category" src="data:image/png;base64,`)
	b.WriteString(base64.StdEncoding.EncodeToString(png))
	b.WriteString(`">`)
	b.WriteString("<table><tr><th>categoría</th><th>pass</th><th>abstención</th><th>miss</th><th>alucinación</th><th>total</th><th>M1 writes</th><th>juez</th></tr>")
	tot := CategoryStat{Name: "TOTAL"}
	for _, st := range stats {
		tot.Pass += st.Pass
		tot.Abstention += st.Abstention
		tot.Miss += st.Miss
		tot.Halluc += st.Halluc
		tot.Total += st.Total
		tot.Writes += st.Writes
		tot.WriteTotal += st.WriteTotal
		tot.JudgeScore += st.JudgeScore
		tot.JudgeCount += st.JudgeCount
		b.WriteString("<tr><td>" + html.EscapeString(st.Name) + "</td>")
		fmt.Fprintf(&b, "<td>%d</td><td>%d</td><td>%d</td>", st.Pass, st.Abstention, st.Miss)
		if st.Halluc > 0 {
			fmt.Fprintf(&b, "<td class=bad>%d</td>", st.Halluc)
		} else {
			fmt.Fprintf(&b, "<td>%d</td>", st.Halluc)
		}
		fmt.Fprintf(&b, "<td>%d</td>", st.Total)
		if st.WriteTotal > 0 {
			fmt.Fprintf(&b, "<td>%d/%d</td>", st.Writes, st.WriteTotal)
		} else {
			b.WriteString("<td>-</td>")
		}
		if st.JudgeCount > 0 {
			fmt.Fprintf(&b, "<td>%d/%d</td>", st.JudgeScore, 2*st.JudgeCount)
		} else {
			b.WriteString("<td>-</td>")
		}
		b.WriteString("</tr>")
	}
	b.WriteString("<tr><td><b>TOTAL</b></td>")
	fmt.Fprintf(&b, "<td><b>%d</b></td><td><b>%d</b></td><td><b>%d</b></td>", tot.Pass, tot.Abstention, tot.Miss)
	if tot.Halluc > 0 {
		fmt.Fprintf(&b, "<td class=bad><b>%d</b></td>", tot.Halluc)
	} else {
		fmt.Fprintf(&b, "<td><b>0</b></td>")
	}
	fmt.Fprintf(&b, "<td><b>%d</b></td><td><b>%d/%d</b></td>", tot.Total, tot.Writes, tot.WriteTotal)
	if tot.JudgeCount > 0 {
		fmt.Fprintf(&b, "<td><b>%d/%d</b></td>", tot.JudgeScore, 2*tot.JudgeCount)
	} else {
		b.WriteString("<td>-</td>")
	}
	b.WriteString("</tr></table></body></html>")
	return b.String(), nil
}
