package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// hook corre bflow con stdin como lo hace Claude Code y devuelve stdout, stderr y exit.
func (r *repo) hook(stdin string, args ...string) (string, string, int) {
	r.t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = r.dir
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	}
	return out.String(), errb.String(), code
}

func preToolUse(dir, tool string, input map[string]any) string {
	b, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "cwd": dir, "tool_name": tool, "tool_input": input})
	return string(b)
}

func TestGuardHook(t *testing.T) {
	r, _ := gitRepo(t, "stack: go\nvcs: { base_branch: dev }\nguard: { forbid_coauthor: true }\n")
	out, errOut, code := r.hook(preToolUse(r.dir, "Bash", map[string]any{"command": "go test ./..."}), "guard")
	if code != 0 || out != "" || errOut != "" {
		t.Fatalf("permitido debe ser silencioso: %d %q %q", code, out, errOut)
	}
	_, errOut, code = r.hook(preToolUse(r.dir, "Bash", map[string]any{"command": "git push origin dev"}), "guard")
	if code != 2 || !strings.Contains(errOut, "dev") {
		t.Fatalf("push a dev: %d %q", code, errOut)
	}
	_, errOut, code = r.hook(preToolUse(r.dir, "Write", map[string]any{"file_path": filepath.Join(r.dir, ".env")}), "guard")
	if code != 2 || !strings.Contains(errOut, ".env") {
		t.Fatalf(".env: %d %q", code, errOut)
	}

	// Prueba congelada: la tarea pasa por el contrato con una prueba escrita.
	id := r.ok("task", "add", "Demo").Data["id"].(string)
	r.ok("start", id, "--lane", "full")
	if err := os.WriteFile(filepath.Join(r.dir, "d.md"), []byte("d"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.ok("approve", id, "--file", "d.md")
	r.ok("report", id, "--agent", "spec-author", "--verdict", "READY")
	r.ok("approve", id)
	if err := os.WriteFile(filepath.Join(r.dir, "internal", "cas_test.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.ok("report", id, "--agent", "implementer", "--verdict", "CONTRACT_READY")
	r.ok("approve", id)
	edit := preToolUse(r.dir, "Edit", map[string]any{"file_path": filepath.Join(r.dir, "internal", "cas_test.go"), "old_string": "a", "new_string": "b"})
	_, errOut, code = r.hook(edit, "guard")
	if code != 2 || (!strings.Contains(errOut, "congelada") && !strings.Contains(errOut, "frozen")) {
		t.Fatalf("prueba congelada: %d %q", code, errOut)
	}
	_, _, code = r.hook(preToolUse(r.dir, "Edit", map[string]any{"file_path": filepath.Join(r.dir, "internal", "cas.go")}), "guard")
	if code != 0 {
		t.Errorf("el código sí se edita: %d", code)
	}
	_, errOut, code = r.hook(preToolUse(r.dir, "Bash", map[string]any{"command": "git checkout -b mi-rama"}), "guard")
	if code != 2 || (!strings.Contains(errOut, "la rama de la tarea la crea bflow") && !strings.Contains(errOut, "task branch is created by bflow")) {
		t.Errorf("con tarea en curso la rama la crea bflow: %d %q", code, errOut)
	}

	out, _, code = r.hook(`{"source":"startup"}`, "hook", "session-start")
	if code != 0 || !strings.Contains(out, id) || strings.Count(out, "\n") > 10 {
		t.Errorf("session-start: %d\n%s", code, out)
	}
}

// Sin hooks (otra herramienta, o un agente que se los salta), el cambio a una
// prueba congelada se detecta al reportar DONE.
func TestFrozenTestsWithoutHooks(t *testing.T) {
	r, _ := gitRepo(t, "vcs: { base_branch: dev }\nguard: { test_patterns: [\"*_test.go\"] }\n")
	id := r.ok("task", "add", "Demo").Data["id"].(string)
	r.ok("start", id, "--lane", "full")
	if err := os.WriteFile(filepath.Join(r.dir, "d.md"), []byte("d"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.ok("approve", id, "--file", "d.md")
	r.ok("report", id, "--agent", "spec-author", "--verdict", "READY")
	git(t, r.dir, "add", ".")
	git(t, r.dir, "commit", "-m", "spec")
	r.ok("approve", id)
	test := filepath.Join(r.dir, "internal", "cas_test.go")
	if err := os.WriteFile(test, []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.ok("report", id, "--agent", "implementer", "--verdict", "CONTRACT_READY")
	r.ok("approve", id)

	if err := os.WriteFile(test, []byte("package a // debilitada\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := r.run("report", id, "--agent", "implementer", "--verdict", "DONE")
	if env.exit != 2 || env.Code != "frozen_changed" {
		t.Fatalf("DONE con una prueba congelada cambiada: exit %d code %s", env.exit, env.Code)
	}

	// Aceptar el cambio lo decide una persona: el agente no puede.
	if _, _, code := r.hook(preToolUse(r.dir, "Bash", map[string]any{"command": "bflow freeze " + id}), "guard"); code != 2 {
		t.Errorf("guard debe bloquear bflow freeze desde el agente: %d", code)
	}
	if n := r.ok("freeze", id).Data["files"].(float64); n != 1 {
		t.Errorf("freeze: %v archivos", n)
	}
	if err := os.WriteFile(test, []byte("package a // debilitada\r\n"), 0o644); err != nil { // autocrlf no cuenta como cambio
		t.Fatal(err)
	}
	// Aceptado el cambio, lo que falta es commitearlo: sin commit no entraría al PR.
	if env := r.run("report", id, "--agent", "implementer", "--verdict", "DONE"); env.Code != "uncommitted" || !strings.Contains(fmt.Sprint(env.Data), "internal/cas_test.go") {
		t.Fatalf("DONE sin commit: %s %v", env.Code, env.Data)
	}
	git(t, r.dir, "add", "internal")
	git(t, r.dir, "commit", "-m", "prueba")
	r.ok("report", id, "--agent", "implementer", "--verdict", "DONE")
}

// Un subagente no responde compuertas: el guard lo niega, lo registra con el
// agente y el comando, y la sesión principal sí puede.
func TestGuardHumanOnlySubagent(t *testing.T) {
	r, _ := gitRepo(t, "stack: go\nvcs: { base_branch: dev }\n")
	id := r.ok("task", "add", "Demo").Data["id"].(string)
	r.ok("start", id, "--lane", "full")

	cmd := "bflow approve " + id + " --gate walkthrough"
	in := func(agent string) string {
		m := map[string]any{"hook_event_name": "PreToolUse", "cwd": r.dir, "tool_name": "Bash", "tool_input": map[string]any{"command": cmd}}
		if agent != "" {
			m["agent_type"] = agent
		}
		b, _ := json.Marshal(m)
		return string(b)
	}
	_, errOut, code := r.hook(in("bflow-documenter"), "guard")
	if code != 2 || !strings.Contains(errOut, "bflow report") || !strings.Contains(errOut, "approve") {
		t.Fatalf("subagente: exit %d stderr %q", code, errOut)
	}
	if _, errOut, code = r.hook(in(""), "guard"); code != 0 {
		t.Fatalf("sesión principal: exit %d stderr %q", code, errOut)
	}

	raw, _ := os.ReadFile(filepath.Join(r.dir, ".bflow", "log.jsonl"))
	found := false
	for _, line := range strings.Split(string(raw), "\n") {
		var e struct {
			Event string         `json:"event"`
			Agent string         `json:"agent"`
			Data  map[string]any `json:"data"`
		}
		if json.Unmarshal([]byte(line), &e) != nil || e.Event != "guard" {
			continue
		}
		if found {
			t.Errorf("más de un evento guard: %s", raw)
		}
		found = true
		if e.Agent != "bflow-documenter" || e.Data["rule"] != "human_only" || e.Data["command"] != cmd {
			t.Errorf("evento guard: %+v", e)
		}
	}
	if !found {
		t.Errorf("falta el evento guard en el log:\n%s", raw)
	}
}

func TestSessionStartTraeNext(t *testing.T) {
	r, _ := gitRepo(t, "stack: go\nvcs: { base_branch: dev }\n")
	out, _, code := r.hook("{}", "hook", "session-start")
	if code != 0 || strings.Contains(out, "next: ") {
		t.Fatalf("sin tarea activa no hay next: (%d) %q", code, out)
	}

	id := r.ok("task", "add", "Demo").Data["id"].(string)
	r.ok("start", id, "--lane", "full")
	out, errOut, code := r.hook("{}", "hook", "session-start")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) > 10 {
		t.Errorf("máximo 10 líneas, hay %d:\n%s", len(lines), out)
	}
	var next string
	for _, l := range lines {
		if strings.HasPrefix(l, "next: ") {
			next = strings.TrimPrefix(l, "next: ")
		}
	}
	var n map[string]any
	if err := json.Unmarshal([]byte(next), &n); err != nil {
		t.Fatalf("falta línea next: con JSON válido: %v\n%s", err, out)
	}
	if _, ok := n["display"]; ok {
		t.Error("el next compacto no lleva display")
	}
	if n["action"] == nil || n["gate"] != "discovery" {
		t.Errorf("next: %v", n)
	}
	if lines[len(lines)-1] != "Sigue con /bflow." {
		t.Errorf("última línea: %q", lines[len(lines)-1])
	}
}
