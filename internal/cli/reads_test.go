package cli

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"innobytes.tech/bflow/internal/engine"
	"innobytes.tech/bflow/internal/metrics"
)

// putReads escribe eventos en reads-all.jsonl de la tarea, uno por línea y un segundo entre cada uno.
func putReads(t *testing.T, e *engine.Engine, id string, evs ...metrics.ReadEvent) {
	t.Helper()
	base := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	var lines [][]byte
	for i, ev := range evs {
		ev.TS = base.Add(time.Duration(i) * time.Second)
		if ev.Phase == "" {
			ev.Phase = "implementing"
		}
		if ev.Tool == "" {
			ev.Tool = "claude"
		}
		b, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, b)
	}
	if err := e.Store.AppendFile(id, metrics.ReadsAllFile, lines); err != nil {
		t.Fatal(err)
	}
}

func rd(agent, path string, partial bool) metrics.ReadEvent {
	return metrics.ReadEvent{Agent: agent, Path: path, Partial: partial}
}

// readsFixture: 5 lecturas, 2 archivos; x.go lo leyeron 3 agentes (2 relecturas entre agentes).
func readsFixture(t *testing.T) (*Env, *engine.Engine, string) {
	t.Helper()
	env, e, id := callsFixture(t, false)
	putReads(t, e, id, rd("main", "x.go", false), rd("implementer", "x.go", true), rd("scout", "x.go", false), rd("main", "y.go", false), rd("main", "y.go", false))
	return env, e, id
}

// rowOf devuelve los campos de la fila de la tabla cuyo primer campo es path.
func rowOf(out, path string) []string {
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) > 0 && f[0] == path {
			return f
		}
	}
	return nil
}

// R13, R16: resumen arriba y tabla por archivo; como máximo 30 filas.
func TestStatsReadsText(t *testing.T) {
	env, _, id := readsFixture(t)
	code, out := textOf(t, env, "stats", id, "--reads")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.HasPrefix(out, id+" · ") {
		t.Errorf("empieza con la línea de stats: %q", out)
	}
	for _, w := range []string{"lecturas: 5 · archivos 2 · parciales 1",
		"relectura entre agentes: 2 de 5 (40.0%) · archivos compartidos: 1 de 2 (50.0%)"} {
		if !strings.Contains(out, w) {
			t.Errorf("falta %q en:\n%s", w, out)
		}
	}
	x := rowOf(out, "x.go")
	if len(x) < 5 || strings.Join(x[1:4], " ") != "3 1 3" || strings.Join(x[4:], "") != "implementer,main,scout" {
		t.Errorf("fila de x.go (3 lecturas, 1 parcial, 3 agentes): %v", x)
	}
	if y := rowOf(out, "y.go"); len(y) < 5 || strings.Join(y[1:4], " ") != "2 0 1" || strings.Join(y[4:], "") != "main" {
		t.Errorf("fila de y.go: %v", y)
	}
	if strings.Index(out, "x.go") > strings.Index(out, "y.go") {
		t.Errorf("x.go (3 lecturas) va antes que y.go (2): %s", out)
	}

	// R16: 35 archivos, solo se muestran 30.
	env, e, id := callsFixture(t, false)
	var evs []metrics.ReadEvent
	for i := 0; i < 35; i++ {
		evs = append(evs, rd("main", fmt.Sprintf("f%02d.go", i), false))
	}
	putReads(t, e, id, evs...)
	_, out = textOf(t, env, "stats", id, "--reads")
	rows := 0
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) > 0 && regexp.MustCompile(`^f\d\d\.go$`).MatchString(f[0]) {
			rows++
		}
	}
	if rows != 30 || !strings.Contains(out, "y 5 archivos más (--json para todos)") {
		t.Errorf("30 filas y el cierre: %d filas\n%s", rows, out)
	}
	_, full, _ := runJSON(t, env, "stats", id, "--reads")
	if got := len(full["data"].(map[string]any)["reads"].(map[string]any)["rows"].([]any)); got != 35 {
		t.Errorf("el JSON trae todas las filas: %d", got)
	}
}

// R17, R18: data.reads con resumen y filas; data.stats igual; sin --reads no hay data.reads.
func TestStatsReadsJSON(t *testing.T) {
	env, _, id := readsFixture(t)
	code, out, raw := runJSON(t, env, "stats", id, "--reads")
	data, _ := out["data"].(map[string]any)
	if code != 0 || data == nil {
		t.Fatalf("exit %d: %s", code, raw)
	}
	if keys := mapKeys(data); !slices.Equal(keys, []string{"reads", "stats"}) {
		t.Errorf("data trae reads y stats: %v", keys)
	}
	r := data["reads"].(map[string]any)
	for k, want := range map[string]float64{"files": 2, "reads": 5, "partial": 1, "cross_rereads": 2, "cross_pct": 40, "shared_files": 1, "shared_pct": 50} {
		if r[k] != want {
			t.Errorf("reads.%s = %v, quiero %v", k, r[k], want)
		}
	}
	rows := r["rows"].([]any)
	first := rows[0].(map[string]any)
	if r["available"] != true || len(rows) != 2 || first["path"] != "x.go" || first["reads"] != 3.0 || first["partial"] != 1.0 ||
		fmt.Sprint(first["agents"]) != "[implementer main scout]" {
		t.Errorf("filas: %s", raw)
	}

	_, plain, _ := runJSON(t, env, "stats", id)
	if keys := mapKeys(plain["data"].(map[string]any)); !slices.Equal(keys, []string{"stats"}) {
		t.Errorf("sin --reads data solo trae stats: %v", keys)
	}
	if a, b := mapKeys(plain["data"].(map[string]any)["stats"].(map[string]any)), mapKeys(data["stats"].(map[string]any)); !slices.Equal(a, b) {
		t.Errorf("data.stats igual con y sin --reads: %v vs %v", a, b)
	}
	_, txt := textOf(t, env, "stats", id)
	if strings.Contains(txt, "lecturas") {
		t.Errorf("sin --reads el texto no cambia: %s", txt)
	}
}

// R19: sin reads-all.jsonl (o sin líneas válidas) se avisa y el código es OK.
func TestStatsReadsLegacy(t *testing.T) {
	env, e, id := callsFixture(t, false)
	code, out := textOf(t, env, "stats", id, "--reads")
	if code != 0 || !strings.Contains(out, "sin registro de lecturas") {
		t.Errorf("texto: %d %q", code, out)
	}
	code, j, raw := runJSON(t, env, "stats", id, "--reads")
	if code != 0 || j["ok"] != true {
		t.Fatalf("JSON: %d %s", code, raw)
	}
	r := j["data"].(map[string]any)["reads"].(map[string]any)
	if r["available"] != false || r["rows"] != nil {
		t.Errorf("available false y sin filas: %s", raw)
	}
	if err := e.Store.AppendFile(id, metrics.ReadsAllFile, [][]byte{[]byte("basura"), []byte(`{"agent":"main"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, out = textOf(t, env, "stats", id, "--reads"); !strings.Contains(out, "sin registro de lecturas") {
		t.Errorf("solo líneas inválidas cuentan como sin registro: %q", out)
	}
}

// R21: con --calls y --reads salen los dos bloques, llamadas primero.
func TestStatsReadsWithCalls(t *testing.T) {
	env, e, id := callsFixture(t, true)
	addTokenEntry(t, e, id, 14, 165, 14000, 350)
	putReads(t, e, id, rd("main", "x.go", false), rd("implementer", "x.go", false))
	_, callsOnly := textOf(t, env, "stats", id, "--calls")
	_, both := textOf(t, env, "stats", id, "--calls", "--reads")
	callsOnly = strings.TrimRight(callsOnly, "\n")
	if !strings.HasPrefix(both, callsOnly) || !strings.Contains(both[len(callsOnly):], "lecturas: 2 · archivos 1") {
		t.Errorf("llamadas y luego lecturas:\n%s", both)
	}
	_, j, raw := runJSON(t, env, "stats", id, "--calls", "--reads")
	if keys := mapKeys(j["data"].(map[string]any)); !slices.Equal(keys, []string{"calls", "reads", "stats"}) {
		t.Errorf("data trae calls, reads y stats: %v\n%s", keys, raw)
	}
}

// R22: --reads sin ID es usage; con un ID inexistente, not_found como stats hoy.
func TestStatsReadsNeedsID(t *testing.T) {
	env, _, _ := readsFixture(t)
	code, out, raw := runJSON(t, env, "stats", "--reads")
	if code == 0 || out["ok"] != false || out["code"] != "usage" || !strings.Contains(raw, "--reads requiere un ID") {
		t.Errorf("sin ID falla con usage: %d %s", code, raw)
	}
	code, out, raw = runJSON(t, env, "stats", "NOPE-999", "--reads")
	if code == 0 || out["code"] != "not_found" {
		t.Errorf("ID inexistente: %d %s", code, raw)
	}
}
