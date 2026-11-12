package cli

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"innobytes.tech/bflow/internal/engine"
	"innobytes.tech/bflow/internal/i18n"
	"innobytes.tech/bflow/internal/metrics"
)

// maxReadRows es el tope de filas de la tabla en texto (R16).
const maxReadRows = 30

// readReadEvents lee reads-all.jsonl; si no puede, devuelve ReadsSummary{} (R19).
func readReadEvents(e *engine.Engine, id string) metrics.ReadsSummary {
	b, err := e.Store.ReadFile(id, metrics.ReadsAllFile)
	if err != nil {
		return metrics.ReadsSummary{}
	}
	return metrics.SummarizeReads(metrics.ParseReadEvents(strings.NewReader(string(b))))
}

// renderReads es el bloque de texto de stats --reads (R13, R16).
func renderReads(s metrics.ReadsSummary, loc *i18n.Localizer) string {
	if loc == nil {
		loc = i18n.DefaultLocalizer()
	}
	if !s.Available {
		return loc.T("reads.none")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s", loc.T("reads.summary", s.Reads, s.Files, s.Partial))
	fmt.Fprintf(&b, "%s", loc.T("reads.cross_rereads", s.CrossRereads, s.Reads, s.CrossPct, s.SharedFiles, s.Files, s.SharedPct))
	rows := [][]string{{
		loc.T("reads.col_path"),
		loc.T("reads.col_reads"),
		loc.T("reads.col_partial"),
		loc.T("reads.col_agents"),
		loc.T("reads.col_who"),
	}}
	for i, r := range s.Rows {
		if i == maxReadRows {
			break
		}
		rows = append(rows, []string{r.Path, fmt.Sprint(r.Reads), fmt.Sprint(r.Partial), fmt.Sprint(len(r.Agents)), strings.Join(r.Agents, ",")})
	}
	w := make([]int, 5)
	for _, row := range rows {
		for i, c := range row {
			w[i] = max(w[i], utf8.RuneCountInString(c))
		}
	}
	for _, row := range rows {
		b.WriteString(" ")
		for i, c := range row {
			pad := strings.Repeat(" ", w[i]-utf8.RuneCountInString(c))
			if i == 0 || i == 4 {
				b.WriteString(" " + c + pad)
			} else {
				b.WriteString(" " + pad + c)
			}
		}
		b.WriteString("\n")
	}
	if more := len(s.Rows) - maxReadRows; more > 0 {
		fmt.Fprintf(&b, "%s", loc.T("reads.more_files", more))
	}
	return strings.TrimRight(b.String(), " \n")
}
