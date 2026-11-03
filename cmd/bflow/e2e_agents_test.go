package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func subagentStop(r *repo, subagent string) (map[string]any, string) {
	out, _, code := r.hook(fmt.Sprintf(`{"hook_event_name":"SubagentStop","agent_type":%q,"cwd":%q,"stop_hook_active":false}`, subagent, r.dir), "hook", "subagent-stop")
	if code != 0 {
		r.t.Fatalf("hook subagent-stop nunca falla: exit %d", code)
	}
	if strings.TrimSpace(out) == "" {
		return nil, ""
	}
	var m struct {
		H map[string]any `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		r.t.Fatalf("salida no es JSON: %q", out)
	}
	return m.H, m.H["additionalContext"].(string)
}

func TestSubagentStopKeepsAgentWorking(t *testing.T) {
	r := newRepo(t)
	r.writeConfig("")
	id := r.ok("task", "add", "Alta de clientes").Data["id"].(string)
	r.ok("start", id, "--lane", "light")
	r.ok("report", id, "--agent", "spec-author", "--verdict", "READY")
	r.ok("approve", id)
	spec, _ := filepath.Glob(filepath.Join(r.dir, "specs", id+"-*", "spec.md"))
	b, _ := os.ReadFile(spec[0])
	doc := strings.Replace(string(b), "## Tasks\n", "## Tasks\n\n- [x] T1 modelo\n- [ ] T2 validar RFC\n- [ ] T3 endpoint\n", 1)
	if err := os.WriteFile(spec[0], []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	// Agentes ajenos a bflow, o de otra fase, terminan sin más.
	if h, _ := subagentStop(r, "Explore"); h != nil {
		t.Errorf("agente ajeno: %v", h)
	}
	if h, _ := subagentStop(r, "bflow-reviewer"); h != nil {
		t.Errorf("agente de otra fase: %v", h)
	}

	h, reason := subagentStop(r, "bflow-implementer")
	if h["hookEventName"] != "SubagentStop" || !strings.Contains(reason, "aviso 1 de 2") ||
		!strings.Contains(reason, "bflow report "+id+" --agent implementer --verdict DONE|NEEDS_DECISION|BLOCKED") ||
		!strings.Contains(reason, "- [ ] T2 validar RFC") || strings.Contains(reason, "T1 modelo") {
		t.Errorf("primer aviso:\n%s", reason)
	}
	if _, reason = subagentStop(r, "bflow-implementer"); !strings.Contains(reason, "aviso 2 de 2") {
		t.Errorf("segundo aviso:\n%s", reason)
	}
	// Tercera vez: lo deja terminar y bloquea la tarea para que decida una persona.
	if h, _ := subagentStop(r, "bflow-implementer"); h != nil {
		t.Errorf("después de los avisos no se insiste: %v", h)
	}
	st := r.ok("status", id).Data["task"].(map[string]any)
	if st["phase"] != "blocked" || !strings.Contains(st["blocked"].(string), "3 veces sin reportar") {
		t.Fatalf("la tarea debe quedar bloqueada: %v", st)
	}
	if n := r.ok("stats", id).Data["stats"].(map[string]any)["nudged"].(float64); n != 3 {
		t.Errorf("fricción por fines sin reporte: %v", n)
	}

	// Al desbloquear, el agente vuelve a tener sus avisos.
	r.ok("unblock", id)
	if _, reason := subagentStop(r, "bflow-implementer"); !strings.Contains(reason, "aviso 1 de 2") {
		t.Errorf("después de unblock los avisos empiezan de nuevo:\n%s", reason)
	}
}

func TestRenderAgentsMDAndConflicts(t *testing.T) {
	r := newRepo(t)
	if err := os.WriteFile(filepath.Join(r.dir, "bflow.yaml"), []byte("stack: go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	agentsMD := filepath.Join(r.dir, "AGENTS.md")
	if err := os.WriteFile(agentsMD, []byte("# Reglas\n\nUsa tabs.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.ok("render")
	b, _ := os.ReadFile(agentsMD)
	if !strings.HasPrefix(string(b), "# Reglas\n\nUsa tabs.\n\n<!-- bflow:inicio") || !strings.Contains(string(b), "bflow status --json") {
		t.Errorf("AGENTS.md:\n%s", b)
	}
	r.ok("render", "--check")
	if err := os.WriteFile(agentsMD, []byte(strings.Replace(string(b), "Usa tabs.", "Usa espacios.", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	r.ok("render", "--check") // editar fuera del bloque no desactualiza nada

	// Sin AGENTS.md, render no lo crea.
	r2 := newRepo(t)
	r2.ok("render")
	if _, err := os.Stat(filepath.Join(r2.dir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Error("render no crea AGENTS.md")
	}

	// Un archivo del usuario con el nombre de un agente de bflow no se pisa.
	if err := os.WriteFile(filepath.Join(r2.dir, ".claude", "agents", "bflow-reviewer.md"), []byte("---\nname: bflow-reviewer\n---\nmío\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if env := r2.run("render"); env.exit != 1 || env.Code != "render_conflict" {
		t.Errorf("conflicto: exit %d code %s", env.exit, env.Code)
	}
	if b, _ := os.ReadFile(filepath.Join(r2.dir, ".claude", "agents", "bflow-reviewer.md")); !strings.Contains(string(b), "mío") {
		t.Error("el archivo del usuario no se toca")
	}
}

func TestDoctorAgentsAndSkills(t *testing.T) {
	r := newRepo(t)
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(r.dir, "bflow.yaml"), []byte("stack: go\nagent: claude\ndoctor: { ignore_skills: [sdd-walkthrough] }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(r.dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	// settings.json de antes de 3b: sin el hook de fin de subagente.
	if err := os.WriteFile(filepath.Join(r.dir, ".claude", "settings.json"), []byte(`{"hooks":{"PreToolUse":[{"hooks":[{"command":"bflow guard"}]}],"SessionStart":[{"hooks":[{"command":"bflow hook session-start"}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, desc := range map[string]string{
		"sdd":             "Orquesta el flujo SDD: avanzar una feature, qué sigue, aprobar la spec.",
		"sdd-walkthrough": "Walkthrough narrado previo al PR para una feature.",
		"go-handlers":     "Convenciones de handlers HTTP en Go.",
	} {
		if err := os.MkdirAll(filepath.Join(r.dir, ".claude", "skills", name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(r.dir, ".claude", "skills", name, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: "+desc+"\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	text, _, _ := r.hook("", "doctor")
	for _, want := range []string{"sdd (", "doctor.ignore_skills", "bflow hook subagent-stop"} {
		if !strings.Contains(text, want) {
			t.Errorf("doctor sin %q:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "desactualizados") && !strings.Contains(text, "outdated") {
		t.Errorf("doctor sin desactualizados/outdated:\n%s", text)
	}
	if strings.Contains(text, "sdd-walkthrough (") || strings.Contains(text, "go-handlers") {
		t.Errorf("doctor avisa de skills ignoradas o de oficio:\n%s", text)
	}
	r.ok("render")
	if text, _, _ := r.hook("", "doctor"); !strings.Contains(text, "archivos generados y al día") && !strings.Contains(text, "files generated and up to date") {
		t.Errorf("doctor después de render:\n%s", text)
	}
}
