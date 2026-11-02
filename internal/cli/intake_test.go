package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"innobytes.tech/bflow/internal/flow"
	"innobytes.tech/bflow/internal/tracker"
	"innobytes.tech/bflow/internal/tracker/trackertest"
)

func runJSON(t *testing.T, env *Env, args ...string) (int, map[string]any, string) {
	t.Helper()
	buf := &bytes.Buffer{}
	env.Stdout = buf
	code := Run(append(args, "--json"), env)
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("no es JSON: %v\n%s", err, buf.String())
	}
	return code, out, buf.String()
}

func TestStatusWithoutTasksAsksIntake(t *testing.T) {
	tr := trackertest.NewMemory()
	env := ghEnv(t, "tracker: { adapter: local }\n", tr)

	code, out, _ := runJSON(t, env, "status")
	next, _ := out["next"].(map[string]any)
	if code != 0 || out["code"] != "status_list" || next["gate"] != "intake" || next["action"] != "ask" {
		t.Fatalf("sin tareas debe preguntar el intake: code %d %v", code, out)
	}

	env.Stdout = &bytes.Buffer{}
	Run([]string{"status"}, env)
	if got := env.Stdout.(*bytes.Buffer).String(); !strings.Contains(got, "sin tareas en curso") || (!strings.Contains(got, "¿Creamos una tarea nueva") && !strings.Contains(got, "Create a new task")) {
		t.Errorf("texto: %q", got)
	}

	env.Stdout = &bytes.Buffer{}
	Run([]string{"status", "--brief"}, env)
	if got := env.Stdout.(*bytes.Buffer).String(); strings.TrimSpace(got) != "bflow: sin tareas en curso" {
		t.Errorf("--brief no cambia el texto: %q", got)
	}

	task, _ := tr.Create(context.Background(), "Algo", "")
	if c, _, _ := runJSON(t, env, "start", task.ID, "--lane", "light"); c != 0 {
		t.Fatalf("start: %d", c)
	}
	_, out, _ = runJSON(t, env, "status")
	if n, _ := out["next"].(map[string]any); n["gate"] == "intake" {
		t.Errorf("con una tarea en curso no hay intake: %v", out)
	}
}

func TestTaskListOnlyOpen(t *testing.T) {
	tr := trackertest.NewMemory()
	open, _ := tr.Create(context.Background(), "Abierta", "desc")
	closed, _ := tr.Create(context.Background(), "Cerrada", "")
	if err := tr.Transition(context.Background(), closed.ID, flow.Done, tracker.Patch{}); err != nil {
		t.Fatal(err)
	}
	env := ghEnv(t, "tracker: { adapter: local }\n", tr)
	code, out, raw := runJSON(t, env, "task", "list")
	if code != 0 || out["code"] != "task_list" {
		t.Fatalf("code %d: %s", code, raw)
	}
	tasks := out["data"].(map[string]any)["tasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("solo abiertas: %v", tasks)
	}
	row := tasks[0].(map[string]any)
	if row["id"] != open.ID || row["title"] != "Abierta" || len(row) != 3 {
		t.Errorf("fila: %v", row)
	}
	env.Stdout = &bytes.Buffer{}
	Run([]string{"task", "list"}, env)
	if got := env.Stdout.(*bytes.Buffer).String(); strings.TrimSpace(got) != open.ID+" · backlog · Abierta" {
		t.Errorf("texto: %q", got)
	}
}

func TestNewCommandUsageAndFile(t *testing.T) {
	tr := trackertest.NewMemory()
	env := ghEnv(t, "tracker: { adapter: local }\n", tr)
	for _, args := range [][]string{{"new", "--title", "x"}, {"new", "--lane", "light", "--title", "  "}} {
		if code, out, _ := runJSON(t, env, args...); code != 1 || out["code"] != "usage" {
			t.Errorf("%v: code %d %v", args, code, out)
		}
	}
	if code, _, _ := runJSON(t, env, "new", "--lane", "zzz", "--title", "x"); code != 2 {
		t.Errorf("carril desconocido es rechazo (exit 2), got %d", code)
	}
	if code, _, _ := runJSON(t, env, "new", "--lane", "light", "--title", "x", "--file", "no-existe.md"); code == 0 {
		t.Error("archivo ilegible debe fallar")
	}
	if l, _ := tr.List(context.Background(), tracker.Filter{}); len(l) != 0 {
		t.Errorf("ninguno de los fallos crea tarea: %v", l)
	}

	if err := os.WriteFile(filepath.Join(env.Dir, "idea.md"), []byte("La idea"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, raw := runJSON(t, env, "new", "--lane", "light", "--title", "Mi idea", "--file", "idea.md")
	if code != 0 {
		t.Fatalf("new: %d %s", code, raw)
	}
	data := out["data"].(map[string]any)
	if data["id"] != "MEM-1" || data["title"] != "Mi idea" {
		t.Errorf("data: %v", data)
	}
	if got, _ := tr.Get(context.Background(), "MEM-1"); got.Description != "La idea" {
		t.Errorf("la idea va de descripción: %q", got.Description)
	}
}
