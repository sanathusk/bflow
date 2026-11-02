package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"innobytes.tech/bflow/internal/testutil"
)

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "bflow-bin-")
	if err != nil {
		panic(err)
	}
	binary = filepath.Join(dir, "bflow")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if out, err := exec.Command("go", "build", "-buildvcs=false", "-o", binary, ".").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "no se pudo compilar bflow: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type envelope struct {
	OK   bool           `json:"ok"`
	Code string         `json:"code"`
	Data map[string]any `json:"data"`
	Next struct {
		Action  string `json:"action"`
		Gate    string `json:"gate"`
		Skill   string `json:"skill"`
		Show    []string
		Display string `json:"display"`
		Reason  string `json:"reason"`
		Options []struct {
			ID      string `json:"id"`
			Command string `json:"command"`
		} `json:"options"`
		Agents []struct {
			Agent    string            `json:"agent"`
			Subagent string            `json:"subagent"`
			Args     map[string]string `json:"args"`
			Report   string            `json:"report"`
		} `json:"agents"`
	} `json:"next"`
	exit int
}

type repo struct {
	t   *testing.T
	dir string
}

func newRepo(t *testing.T) *repo {
	dir := testutil.TempDir(t)
	t.Setenv("BFLOW_CONFIG_HOME", testutil.TempDir(t))
	t.Setenv("BFLOW_USER", "dev")
	t.Setenv("CI", "1") // sin ventanas ni notificaciones reales al probar
	return &repo{t: t, dir: dir}
}

// noScout apaga el scout en un bflow.yaml: estas pruebas recorren el flujo sin
// él (lo cubren las pruebas del scout).
func noScout(yaml string) string {
	if strings.Contains(yaml, "flow: { ") {
		return strings.Replace(yaml, "flow: { ", "flow: { scout: false, ", 1)
	}
	return yaml + "flow: { scout: false }\n"
}

// writeConfig escribe bflow.yaml con el scout apagado.
func (r *repo) writeConfig(yaml string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, "bflow.yaml"), []byte(noScout(yaml)), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// run ejecuta bflow con --json y decodifica el envelope.
func (r *repo) run(args ...string) envelope {
	r.t.Helper()
	return r.runStdin("", args...)
}

// runStdin es run con stdin.
func (r *repo) runStdin(stdin string, args ...string) envelope {
	r.t.Helper()
	cmd := exec.Command(binary, append(args, "--json")...)
	cmd.Dir = r.dir
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	var env envelope
	if jerr := json.Unmarshal(out.Bytes(), &env); jerr != nil {
		r.t.Fatalf("bflow %s: salida no es JSON (%v)\n%s\n%s", strings.Join(args, " "), jerr, out.String(), errb.String())
	}
	if ee, ok := err.(*exec.ExitError); ok {
		env.exit = ee.ExitCode()
	}
	return env
}

func (r *repo) ok(args ...string) envelope {
	r.t.Helper()
	env := r.run(args...)
	if !env.OK || env.exit != 0 {
		r.t.Fatalf("bflow %s falló (exit %d): %v", strings.Join(args, " "), env.exit, env.Data)
	}
	return env
}

// option devuelve el comando de la opción con ese id, ya partido en args.
func option(t *testing.T, env envelope, id string, fill map[string]string) []string {
	t.Helper()
	for _, o := range env.Next.Options {
		if o.ID == id {
			cmd := o.Command
			for k, v := range fill {
				cmd = strings.ReplaceAll(cmd, k, v)
			}
			return splitArgs(cmd)[1:] // sin "bflow"
		}
	}
	t.Fatalf("no hay opción %q en %+v", id, env.Next.Options)
	return nil
}

// splitArgs parte respetando comillas dobles.
func splitArgs(s string) []string {
	var out []string
	var cur strings.Builder
	quoted := false
	for _, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
		case r == ' ' && !quoted:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func expectNext(t *testing.T, env envelope, action, gate string) {
	t.Helper()
	if env.Next.Action != action || env.Next.Gate != gate {
		t.Fatalf("next = %s/%s, want %s/%s (data %v)", env.Next.Action, env.Next.Gate, action, gate, env.Data)
	}
}

func spawned(t *testing.T, env envelope, agents ...string) {
	t.Helper()
	var got []string
	for _, a := range env.Next.Agents {
		got = append(got, a.Agent)
		if a.Subagent != "bflow-"+a.Agent {
			t.Errorf("spawn de %s debe nombrar su subagente generado: %q", a.Agent, a.Subagent)
		}
	}
	if env.Next.Action != "spawn" || strings.Join(got, ",") != strings.Join(agents, ",") {
		t.Fatalf("spawn %v, want %v (next %+v)", got, agents, env.Next)
	}
}

// TestFullFeatureWithLocalTracker recorre una feature completa en carril full
// usando solo lo que dice next, como lo hará la skill del leader.
func TestFullFeatureWithLocalTracker(t *testing.T) {
	r := newRepo(t)
	// Con el auditor de seguridad aparte: quality con dos revisores en paralelo.
	r.writeConfig("flow: { security_audit: true }\n")
	env := r.ok("task", "add", "Borrador pre-folio de cotización")
	id := env.Data["id"].(string)
	if id != "LOCAL-1" {
		t.Fatalf("id %s", id)
	}
	expectNext(t, env, "ask", "lane")

	env = r.ok(option(t, env, "full", nil)...)
	expectNext(t, env, "ask", "discovery")

	env = r.runStdin("# Discovery\n- entra: borrador compartido\n- no entra: folio definitivo\n", option(t, env, "approve", nil)...)
	if !env.OK {
		t.Fatalf("approve --stdin: %v", env.Data)
	}
	if _, err := os.Stat(filepath.Join(r.dir, ".bflow", "tasks", id, "discovery.md")); err == nil {
		t.Error("no debe crearse discovery.md")
	}
	spawned(t, env, "spec-author")
	spec := env.Next.Agents[0].Args["spec"]
	if _, err := os.Stat(filepath.Join(r.dir, spec)); err != nil {
		t.Fatalf("spec no creado en %s", spec)
	}
	// Lo que escribe el spec-author es lo que el humano ve en la gate, sin correr show.
	specAbs := filepath.Join(r.dir, spec)
	sb, _ := os.ReadFile(specAbs)
	if err := os.WriteFile(specAbs, []byte(strings.Replace(string(sb), "## Brief\n", "## Brief\n\n**Objetivo:** borrador compartido de cotización.\n", 1)), 0o644); err != nil {
		t.Fatal(err)
	}

	env = r.ok("report", id, "--agent", "spec-author", "--verdict", "READY")
	expectNext(t, env, "ask", "spec")
	if !strings.Contains(env.Next.Display, "borrador compartido de cotización") {
		t.Errorf("la gate de spec trae el brief en display: %q", env.Next.Display)
	}
	if len(env.Next.Show) != 1 {
		t.Fatalf("el gate spec debe pedir mostrar el brief: %+v", env.Next)
	}
	shown := r.ok(splitArgs(env.Next.Show[0])[1:]...)
	if _, ok := shown.Data["content"]; !ok {
		t.Fatal("show brief sin contenido")
	}

	// Rechazo con cambios puntuales y vuelta a aprobar.
	env = r.ok(option(t, env, "changes", map[string]string{"<motivo>": "falta el caso de concurrencia"})...)
	spawned(t, env, "spec-author")
	if env.Next.Agents[0].Args["note"] != "falta el caso de concurrencia" {
		t.Errorf("la nota del rechazo debe llegar al agente: %v", env.Next.Agents[0].Args)
	}
	env = r.ok("report", id, "--agent", "spec-author", "--verdict", "READY")
	env = r.ok(option(t, env, "approve", nil)...)
	spawned(t, env, "implementer")
	if env.Data["branch"] != "feature/LOCAL-1-borrador-pre-folio-de-cotizacion" {
		t.Errorf("rama: %v", env.Data["branch"])
	}

	env = r.ok("report", id, "--agent", "implementer", "--verdict", "CONTRACT_READY")
	expectNext(t, env, "ask", "contract")
	if err := os.MkdirAll(filepath.Join(r.dir, ".bflow", "tasks", id), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.dir, ".bflow", "tasks", id, "contract.md"), []byte("type Draft struct{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := r.ok("show", id, "contract"); c.Data["content"] != "type Draft struct{}" {
		t.Errorf("show contract: %v", c.Data)
	}
	env = r.ok(option(t, env, "approve", nil)...)
	spawned(t, env, "implementer")

	// Decisión en vuelo.
	env = r.ok("report", id, "--agent", "implementer", "--verdict", "NEEDS_DECISION",
		"--note", "¿El 409 congela el formulario?", "--option", "Congelar", "--option", "Avisar y seguir")
	expectNext(t, env, "ask", "decision")
	env = r.ok(option(t, env, "opt1", nil)...)
	spawned(t, env, "implementer")
	if env.Next.Agents[0].Args["decision"] != "Congelar" {
		t.Errorf("decisión al agente: %v", env.Next.Agents[0].Args)
	}

	// Sin pedirla, el DONE no abre gate; con `bflow pause` para en la gate pause.
	r.ok("pause", id)
	r.ok("pause", id) // repetirlo no es error
	env = r.ok("report", id, "--agent", "implementer", "--verdict", "DONE")
	expectNext(t, env, "ask", "pause")
	env = r.ok(option(t, env, "approve", nil)...)
	spawned(t, env, "reviewer", "security-auditor")

	// Ronda 1 rechazada, ronda 2 aprobada.
	r.ok("report", id, "--agent", "reviewer", "--verdict", "APPROVED")
	env = r.ok("report", id, "--agent", "security-auditor", "--verdict", "REJECTED", "--file", ".bflow/tasks/"+id+"/reports/audit.md")
	spawned(t, env, "implementer")
	if env.Data["round"].(float64) != 1 {
		t.Errorf("ronda: %v", env.Data["round"])
	}
	env = r.ok("report", id, "--agent", "implementer", "--verdict", "DONE")
	spawned(t, env, "reviewer", "security-auditor")
	if late := r.run("pause", id); late.OK {
		t.Error("pause en quality debe rechazarse")
	}
	r.ok("report", id, "--agent", "security-auditor", "--verdict", "APPROVED")
	env = r.ok("report", id, "--agent", "reviewer", "--verdict", "APPROVED")
	spawned(t, env, "documenter")
	if err := os.MkdirAll(filepath.Join(r.dir, ".bflow", "tasks", id, "reports"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.dir, ".bflow", "tasks", id, "reports", "review-map.md"),
		[]byte("## 🔴 Decisión\n- folio: se reserva al confirmar\n\n## Preguntas de producto\n1. ¿El borrador consume folio?\n   el código responde: no (quotation.go:10)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env = r.ok("report", id, "--agent", "documenter", "--verdict", "DONE")
	expectNext(t, env, "ask", "questions")
	if env.Next.Skill != "walkthrough" || !strings.Contains(env.Next.Display, "¿El borrador consume folio?") || strings.Contains(env.Next.Display, "el código responde") {
		t.Errorf("preguntas sin las respuestas del código: %+v", env.Next)
	}
	env = r.ok(option(t, env, "answer", map[string]string{"<respuestas>": "no debe consumir folio"})...)
	expectNext(t, env, "ask", "walkthrough")
	if !strings.Contains(env.Next.Display, "no debe consumir folio") || !strings.Contains(env.Next.Display, "el código responde: no") {
		t.Errorf("el recorrido compara sus respuestas con el review-map: %q", env.Next.Display)
	}
	env = r.ok(option(t, env, "approve", nil)...)
	expectNext(t, env, "wait", "")

	st := r.ok("status", id)
	if st.Data["task"].(map[string]any)["phase"] != "in_review" {
		t.Errorf("status: %v", st.Data)
	}
	// El tracker local refleja la fase y los comentarios.
	b, _ := os.ReadFile(filepath.Join(r.dir, ".bflow", "local", id+".json"))
	for _, want := range []string{`"phase": "in_review"`, "borrador compartido", "falta el caso de concurrencia", "Compuerta de calidad rechazada (ronda 1)", `"start"`} {
		if !bytes.Contains(b, []byte(want)) {
			t.Errorf("el tracker local no tiene %q", want)
		}
	}
	log, _ := os.ReadFile(filepath.Join(r.dir, ".bflow", "log.jsonl"))
	if n := bytes.Count(log, []byte("\n")); n < 20 {
		t.Errorf("log.jsonl con %d líneas", n)
	}
}

func TestHotfixWithLocalTracker(t *testing.T) {
	r := newRepo(t)
	r.writeConfig("")
	id := r.ok("task", "add", "Servidor no arranca: migración 124 mal numerada").Data["id"].(string)
	env := r.ok("start", id, "--lane", "hotfix")
	spawned(t, env, "implementer")
	if env.Data["branch"] != "hotfix/LOCAL-1-servidor-no-arranca-migracion-124-mal-numerada" {
		t.Errorf("rama: %v", env.Data["branch"])
	}
	env = r.ok("report", "--agent", "implementer", "--verdict", "DONE") // sin ID: tarea activa
	// Solo el reviewer: revisa también la seguridad.
	spawned(t, env, "reviewer")
	env = r.ok("report", "--agent", "reviewer", "--verdict", "APPROVED")
	spawned(t, env, "documenter")
	env = r.ok("report", "--agent", "documenter", "--verdict", "DONE")
	expectNext(t, env, "ask", "questions")
	env = r.ok(option(t, env, "approve", nil)...) // saltar las preguntas
	expectNext(t, env, "ask", "walkthrough")
	for _, o := range env.Next.Options {
		if o.ID == "spec" {
			t.Error("hotfix no tiene spec: no puede ofrecer volver a spec")
		}
	}
	env = r.ok(option(t, env, "approve", nil)...)
	expectNext(t, env, "wait", "")
	if _, err := os.Stat(filepath.Join(r.dir, "specs")); !os.IsNotExist(err) {
		t.Error("hotfix no debe crear spec")
	}
}

func TestRejectionsExitTwo(t *testing.T) {
	r := newRepo(t)
	r.writeConfig("")
	id := r.ok("task", "add", "Demo").Data["id"].(string)
	env := r.run("approve", id)
	if env.exit != 2 || env.Code != "not_started" {
		t.Errorf("aprobar sin empezar: exit %d code %s", env.exit, env.Code)
	}
	r.ok("start", id, "--lane", "light")
	env = r.run("report", id, "--agent", "reviewer", "--verdict", "APPROVED")
	if env.exit != 2 || env.Code != "unexpected_agent" {
		t.Errorf("agente fuera de fase: exit %d code %s", env.exit, env.Code)
	}
	env = r.run("start", "LOCAL-99", "--lane", "full")
	if env.exit != 1 {
		t.Errorf("tarea inexistente es error (exit 1): %d", env.exit)
	}
}

func TestBrokenConfigIsReadable(t *testing.T) {
	r := newRepo(t)
	if err := os.WriteFile(filepath.Join(r.dir, "bflow.yaml"), []byte("tracker: { adapter: plane, api_key: x }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := r.run("status")
	if env.exit != 1 || !strings.Contains(fmt.Sprint(env.Data["error"]), "llavero") {
		t.Errorf("config con secreto: exit %d %v", env.exit, env.Data)
	}
}

func TestRejectBackToImplementingWithoutGate(t *testing.T) {
	r := newRepo(t)
	r.writeConfig("")
	id := r.ok("task", "add", "Defecto tardio").Data["id"].(string)
	r.ok("start", id, "--lane", "hotfix")
	r.ok("report", "--agent", "implementer", "--verdict", "DONE")
	env := r.ok("report", "--agent", "reviewer", "--verdict", "APPROVED")
	spawned(t, env, "documenter")
	if env := r.run("reject", id, "--to", "implementing"); env.exit != 2 || env.Code != "note_required" {
		t.Errorf("sin --note: exit %d code %s", env.exit, env.Code)
	}
	env = r.ok("reject", id, "--to", "implementing", "--note", "x")
	spawned(t, env, "implementer")
	st := r.ok("status", id)
	if st.Data["task"].(map[string]any)["phase"] != "implementing" {
		t.Errorf("status: %v", st.Data)
	}
}
