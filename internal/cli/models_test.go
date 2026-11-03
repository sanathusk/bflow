package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"innobytes.tech/bflow/internal/flow"
	"innobytes.tech/bflow/internal/metrics"
	"innobytes.tech/bflow/internal/store"
	"innobytes.tech/bflow/internal/tracker/trackertest"
)

// seedModelLog escribe en el log del repo 3 tareas con sonnet de reviewer y 3
// con opus; las de opus rondan más (rondas 2 contra 1), así que sube.
func seedModelLog(t *testing.T, env *Env, despuesRounds int) {
	t.Helper()
	e, err := env.Build(env.Dir)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-30 * 24 * time.Hour)
	var all []store.Entry
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("M-%d", i+1)
		rev, rounds := "sonnet", 1
		if i >= 3 {
			rev, rounds = "opus", despuesRounds
		}
		ts := base.Add(time.Duration(i) * 24 * time.Hour)
		all = append(all,
			store.Entry{TS: ts, ID: id, Event: "start", To: flow.Spec, Data: map[string]any{"lane": "light",
				"models": map[string]any{"reviewer": rev, "implementer": "sonnet"}}},
			store.Entry{TS: ts.Add(time.Minute), ID: id, Event: "report", Agent: "reviewer", From: flow.Quality, To: flow.Implementing, Round: rounds},
			store.Entry{TS: ts.Add(2 * time.Minute), ID: id, Event: "approve", From: flow.InReview, To: flow.Done},
		)
	}
	if err := e.Store.Append(all...); err != nil {
		t.Fatal(err)
	}
}

func TestStatsModelVerdict(t *testing.T) {
	env := ghEnv(t, "tracker: { adapter: local }\n", trackertest.NewMemory())
	seedModelLog(t, env, 2)

	code, out, raw := runJSON(t, env, "stats")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, raw)
	}
	mv, _ := dataOf(out)["models"].(map[string]any)
	if mv["verdict"] != "sube" {
		t.Fatalf("data.models = %v", dataOf(out)["models"])
	}
	cur, _ := mv["current"].(map[string]any)
	if tasks, _ := cur["tasks"].([]any); len(tasks) != 3 || tasks[0] != "M-6" {
		t.Errorf("current.tasks = %v", cur["tasks"])
	}

	code, text := textOf(t, env, "stats")
	if code != 0 || !strings.Contains(text, "modelos: sube") || !strings.Contains(text, "después 3/3 · antes 3/3") {
		t.Errorf("línea modelos: del texto:\n%s", text)
	}
	if strings.Contains(text, "\x1b[") {
		t.Errorf("fuera de terminal no lleva color: %q", text)
	}

	_, text = textOf(t, env, "stats", "M-5")
	if !strings.Contains(text, "modelos (start): implementer=sonnet,reviewer=opus") {
		t.Errorf("stats <ID> muestra los modelos y su origen:\n%s", text)
	}
	_, out, _ = runJSON(t, env, "stats", "M-5")
	st, _ := dataOf(out)["stats"].(map[string]any)
	if st["model_source"] != "start" {
		t.Errorf("model_source = %v", st["model_source"])
	}
}

func TestStatsModelVerdictColor(t *testing.T) {
	v := metrics.ModelVerdict{
		Verdict:  "sube",
		Current:  metrics.ModelGroup{Models: metrics.ModelSet{"reviewer": "opus"}, Tasks: []string{"M-6", "M-5", "M-4"}},
		Previous: &metrics.ModelGroup{Models: metrics.ModelSet{"reviewer": "sonnet"}, Tasks: []string{"M-3", "M-2", "M-1"}},
		Changes:  []metrics.ModelChange{{Agent: "reviewer", From: "sonnet", To: "opus"}},
		Rose:     []string{"rondas 1.7 > 1.0"},
	}
	plain := renderModelVerdict(v, false)
	colored := renderModelVerdict(v, true)
	for _, want := range []string{"sube", "rondas 1.7 > 1.0", "reviewer: opus → volver a sonnet", "cambia bflow.yaml (agents) y corre bflow render"} {
		if !strings.Contains(plain, want) {
			t.Errorf("sin color falta %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "\x1b[") {
		t.Errorf("sin color no lleva secuencias ANSI: %q", plain)
	}
	if !strings.Contains(colored, "\x1b[31m") || !strings.Contains(colored, "\x1b[0m") {
		t.Errorf("con color va en rojo: %q", colored)
	}
	for _, r := range plain + colored {
		if r > 0x2000 && r != '·' && r != '→' && r != '>' {
			t.Errorf("sin emojis: %q", r)
		}
	}
	ok := renderModelVerdict(metrics.ModelVerdict{Verdict: "sin_veredicto",
		Current:  metrics.ModelGroup{Tasks: []string{"a", "b"}},
		Previous: &metrics.ModelGroup{Tasks: []string{"c", "d", "e"}}}, true)
	if strings.Contains(ok, "\x1b[31m") || !strings.Contains(ok, "después 2/3 · antes 3/3") {
		t.Errorf("sin veredicto no va en rojo y da la cuenta: %q", ok)
	}
}

func TestDoctorModelsWarnKeepsExit(t *testing.T) {
	t.Setenv("BFLOW_AS_CLI", "1")
	clean := ghEnv(t, "tracker: { adapter: local }\n", trackertest.NewMemory())
	baseCode, _, _ := runJSON(t, clean, "doctor")
	item := func(env *Env) (int, map[string]any) {
		code, out, raw := runJSON(t, env, "doctor")
		for _, it := range dataOf(out)["checks"].([]any) {
			if m := it.(map[string]any); m["area"] == "veredicto-modelos" {
				return code, m
			}
		}
		t.Fatalf("doctor sin ítem veredicto-modelos: %s", raw)
		return 0, nil
	}
	if _, m := item(clean); m["status"] != "ok" || !strings.Contains(fmt.Sprint(m["detail"]), "sin datos de modelos todavía") {
		t.Errorf("sin datos: %v", m)
	}

	rises := ghEnv(t, "tracker: { adapter: local }\n", trackertest.NewMemory())
	seedModelLog(t, rises, 2)
	code, m := item(rises)
	if m["status"] != "warn" || !strings.Contains(fmt.Sprint(m["detail"]), "rondas") {
		t.Errorf("sube debe dar warn con el motivo: %v", m)
	}
	if code != baseCode {
		t.Errorf("el warn no cambia el código de salida: %d contra %d", code, baseCode)
	}

	same := ghEnv(t, "tracker: { adapter: local }\n", trackertest.NewMemory())
	seedModelLog(t, same, 1)
	if _, m := item(same); m["status"] != "ok" {
		t.Errorf("sin regresión es ok: %v", m)
	}
}

// stripStartModels quita data.models de los start del log, como una tarea vieja.
func stripStartModels(t *testing.T, env *Env) {
	t.Helper()
	e, err := env.Build(env.Dir)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(e.Store.Dir(), "log.jsonl")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatal(err)
		}
		if d, ok := m["data"].(map[string]any); ok {
			delete(d, "models")
		}
		b, _ := json.Marshal(m)
		out = append(out, string(b))
	}
	if err := os.WriteFile(p, []byte(strings.Join(out, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
func TestStatsModelVerdictNoData(t *testing.T) {
	env, _, _, _ := tokensEnv(t, true)
	stripStartModels(t, env)
	code, text := textOf(t, env, "stats")
	if code != 0 || !strings.Contains(text, "modelos: sin datos de modelos todavía") {
		t.Errorf("con tareas y sin datos debe decirlo (exit %d): %s", code, text)
	}
}

func TestStatsEmptyHistoryNoModelLine(t *testing.T) {
	env := ghEnv(t, "tracker: { adapter: local }\n", trackertest.NewMemory())
	code, text := textOf(t, env, "stats")
	if code != 0 || strings.TrimSpace(text) != "sin historial todavía" || strings.Contains(text, "modelos") {
		t.Errorf("sin tareas solo 'sin historial todavía' (exit %d): %q", code, text)
	}
}

func TestDoctorModelVerdictLogUnreadable(t *testing.T) {
	t.Setenv("BFLOW_AS_CLI", "1")
	clean := ghEnv(t, "tracker: { adapter: local }\n", trackertest.NewMemory())
	baseCode, _, _ := runJSON(t, clean, "doctor")

	bad := ghEnv(t, "tracker: { adapter: local }\n", trackertest.NewMemory())
	e, err := bad.Build(bad.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(e.Store.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.Store.Dir(), "log.jsonl"), []byte("esto no es json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, raw := runJSON(t, bad, "doctor")
	var found map[string]any
	for _, it := range dataOf(out)["checks"].([]any) {
		if m := it.(map[string]any); m["area"] == "veredicto-modelos" {
			found = m
		}
	}
	if found == nil {
		t.Fatalf("doctor sin ítem veredicto-modelos: %s", raw)
	}
	if found["status"] != "warn" || (!strings.HasPrefix(fmt.Sprint(found["detail"]), "no se pudo leer el log") && !strings.HasPrefix(fmt.Sprint(found["detail"]), "could not read log")) {
		t.Errorf("ítem: %v", found)
	}
	if code != baseCode {
		t.Errorf("el warn no cambia el código de salida: %d contra %d", code, baseCode)
	}
}
