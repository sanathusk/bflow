package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"innobytes.tech/bflow/internal/config"
	"innobytes.tech/bflow/internal/engine"
	"innobytes.tech/bflow/internal/flow"
	"innobytes.tech/bflow/internal/guard"
	"innobytes.tech/bflow/internal/store"
)

const frozenRel = "internal/a_test.go"

var frozenOrig = []byte("package a\r\nfunc TestA() {}\r\n")

// frozenTask arma una tarea activa en la fase dada con frozenRel congelada
// (con su copia en frozen-tests/, como la deja bflow al congelar).
func frozenTask(t *testing.T, phase flow.Phase) (*Env, *engine.Engine, string) {
	t.Helper()
	env, e, id, _ := tokensEnv(t, true)
	if _, err := e.Store.Update(id, func(r *store.Record, _ bool) error { r.Flow.Phase = phase; return nil }); err != nil {
		t.Fatal(err)
	}
	writeFrozenFile(t, env, frozenOrig)
	sum := sha256.Sum256([]byte(strings.ReplaceAll(string(frozenOrig), "\r\n", "\n")))
	h := hex.EncodeToString(sum[:])
	m, _ := json.Marshal(map[string]string{frozenRel: h})
	if err := e.Store.WriteFile(id, engine.FrozenFile, m); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.WriteFile(id, engine.FrozenSnapshots+"/"+h, frozenOrig); err != nil {
		t.Fatal(err)
	}
	return env, e, id
}

func writeFrozenFile(t *testing.T, env *Env, b []byte) {
	t.Helper()
	p := filepath.Join(env.Dir, filepath.FromSlash(frozenRel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func frozenOnDisk(t *testing.T, env *Env) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(env.Dir, filepath.FromSlash(frozenRel)))
	if err != nil {
		return "<ausente>"
	}
	return string(b)
}

// claudePost es la entrada de un hook PostToolUse de Claude Code.
func claudePost(env *Env, tool string, input map[string]any) string {
	b, _ := json.Marshal(map[string]any{"hook_event_name": "PostToolUse", "cwd": env.Dir, "tool_name": tool, "tool_input": input,
		"tool_response": map[string]any{"stdout": "", "stderr": ""}})
	return string(b)
}

func eventsOf(t *testing.T, e *engine.Engine, id, event string) []store.Entry {
	t.Helper()
	log, err := e.Store.Log(id)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.Entry
	for _, en := range log {
		if en.Event == event {
			out = append(out, en)
		}
	}
	return out
}

// R13, R15, R17, R18: el Bash cambió una prueba congelada → se restaura y sale 2.
func TestGuardPostRestores(t *testing.T) {
	env, e, id := frozenTask(t, flow.Implementing)
	in := claudePost(env, "Bash", map[string]any{"command": "python - <<'EOF'\nopen('internal/a_test.go','w').write('x')\nEOF"})

	if code, out, errOut := hookRun(t, env, in, "guard", "--post"); code != 0 || out != "" || errOut != "" {
		t.Fatalf("sin cambios es silencioso: %d %q %q", code, out, errOut)
	}
	writeFrozenFile(t, env, []byte("package a\nfunc TestA() { t.Skip() }\n"))
	code, out, errOut := hookRun(t, env, in, "guard", "--post", "--json")
	if code != 2 {
		t.Fatalf("restaurar sale 2: %d %q %q", code, out, errOut)
	}
	if !strings.Contains(out, `"frozen_test"`) {
		t.Errorf("el rechazo lleva el código frozen_test: %q", out)
	}
	for _, w := range []string{"bflow guard:", frozenRel, "NEEDS_DECISION", "freeze --allow"} {
		if !strings.Contains(errOut, w) {
			t.Errorf("stderr no dice %q: %q", w, errOut)
		}
	}
	if got := frozenOnDisk(t, env); got != string(frozenOrig) {
		t.Errorf("la prueba vuelve con sus bytes (CRLF): %q", got)
	}
	if n := len(eventsOf(t, e, id, "frozen_restore")); n != 1 {
		t.Errorf("un evento frozen_restore: %d", n)
	}
	g := eventsOf(t, e, id, "guard")
	if len(g) != 1 || g[0].Data["rule"] != "frozen_test" {
		t.Errorf("el rechazo se registra como el guard: %+v", g)
	}
	if code, out, errOut := hookRun(t, env, in, "guard", "--post"); code != 0 || out != "" || errOut != "" {
		t.Errorf("ya restaurada: silencio: %d %q %q", code, out, errOut)
	}
}

// R19, R20: OpenCode manda el mismo cuerpo que el guard previo.
func TestGuardPostOpenCode(t *testing.T) {
	env, _, _ := frozenTask(t, flow.Implementing)
	body := func(tool string) string {
		return guardIn(env, tool, map[string]any{"command": "sed -i s/a/b/ internal/a_test.go"}, true, "bflow-implementer")
	}
	writeFrozenFile(t, env, []byte("cambiada\n"))
	code, _, errOut := hookRun(t, env, body("bash"), "guard", "--tool", "opencode", "--post")
	if code != 2 || !strings.Contains(errOut, frozenRel) {
		t.Fatalf("OpenCode restaura y sale 2: %d %q", code, errOut)
	}
	if got := frozenOnDisk(t, env); got != string(frozenOrig) {
		t.Errorf("restaurada: %q", got)
	}
	writeFrozenFile(t, env, []byte("cambiada\n"))
	if code, _, errOut := hookRun(t, env, `{"tool":"bash","args":{}}`, "guard", "--tool", "opencode", "--post"); code != 2 {
		t.Errorf("after no trae los argumentos (args vacíos): igual restaura: %d %q", code, errOut)
	}
}

// R16: sin tarea activa no hace nada.
func TestGuardPostNoTask(t *testing.T) {
	env, _, _, _ := tokensEnv(t, false)
	writeFrozenFile(t, env, []byte("cambiada\n"))
	code, out, errOut := hookRun(t, env, claudePost(env, "Bash", map[string]any{"command": "ls"}), "guard", "--post")
	if code != 0 || out != "" || errOut != "" {
		t.Errorf("sin tarea: %d %q %q", code, out, errOut)
	}
	if got := frozenOnDisk(t, env); got != "cambiada\n" {
		t.Errorf("no se toca nada: %q", got)
	}
}

// R16: fuera de las fases congeladas no restaura.
func TestGuardPostOutsidePhase(t *testing.T) {
	for _, p := range []flow.Phase{flow.Contract, flow.Spec, flow.InReview} {
		env, _, _ := frozenTask(t, p)
		writeFrozenFile(t, env, []byte("cambiada\n"))
		code, out, errOut := hookRun(t, env, claudePost(env, "Bash", map[string]any{"command": "ls"}), "guard", "--post")
		if code != 0 || out != "" || errOut != "" {
			t.Errorf("fase %s: %d %q %q", p, code, out, errOut)
		}
		if got := frozenOnDisk(t, env); got != "cambiada\n" {
			t.Errorf("fase %s: no se toca: %q", p, got)
		}
	}
}

// R16: solo Bash se revisa después de ejecutar.
func TestGuardPostNotBash(t *testing.T) {
	env, _, _ := frozenTask(t, flow.Implementing)
	writeFrozenFile(t, env, []byte("cambiada\n"))
	for _, tool := range []string{"Edit", "Write", "Read"} {
		code, out, errOut := hookRun(t, env, claudePost(env, tool, map[string]any{"file_path": filepath.Join(env.Dir, "x.go")}), "guard", "--post")
		if code != 0 || out != "" || errOut != "" {
			t.Errorf("%s: %d %q %q", tool, code, out, errOut)
		}
	}
	if code, _, _ := hookRun(t, env, guardIn(env, "write", map[string]any{"filePath": "x.go"}, false, ""), "guard", "--tool", "opencode", "--post"); code != 0 {
		t.Errorf("write de OpenCode: %d", code)
	}
	if got := frozenOnDisk(t, env); got != "cambiada\n" {
		t.Errorf("no se toca: %q", got)
	}
}

// R16: entrada ilegible, herramienta desconocida o config rota → 0 sin salida ni cambios.
func TestGuardPostBadInput(t *testing.T) {
	env, _, _ := frozenTask(t, flow.Implementing)
	writeFrozenFile(t, env, []byte("cambiada\n"))
	for name, args := range map[string][]string{"claude": {"guard", "--post"}, "opencode": {"guard", "--tool", "opencode", "--post"},
		"desconocida": {"guard", "--tool", "nada", "--post"}} {
		for _, in := range []string{"", "no es json", `{"tool_name": 3}`, `[]`} {
			code, out, errOut := hookRun(t, env, in, args...)
			if code != 0 || out != "" || errOut != "" {
				t.Errorf("%s con %q: %d %q %q", name, in, code, out, errOut)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(env.Dir, "bflow.yaml"), []byte("agent: [\n  : roto"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := hookRun(t, env, claudePost(env, "Bash", map[string]any{"command": "ls"}), "guard", "--post")
	if code != 0 || out != "" || errOut != "" {
		t.Errorf("config rota: %d %q %q", code, out, errOut)
	}
	if got := frozenOnDisk(t, env); got != "cambiada\n" {
		t.Errorf("no se toca: %q", got)
	}
}

// R11: el guard previo carga la fase y las congeladas para comandos que pueden escribir.
func TestNeedsTaskMayWrite(t *testing.T) {
	env, _, _ := frozenTask(t, flow.Implementing)
	cfg, err := config.Load(env.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for cmd, want := range map[string]bool{
		"sed -i s/a/b/ internal/a_test.go": true, "echo x > internal/a_test.go": true, "python -c 'x'": true,
		"tee internal/a_test.go": true, "git apply x.diff": true,
		"go test ./...": false, "cat internal/a_test.go": false, "git status": false,
	} {
		if got := needsTask(guard.Action{Tool: guard.Bash, Command: cmd}, cfg); got != want {
			t.Errorf("needsTask(%q) = %v", cmd, got)
		}
	}
	// De punta a punta: el hook previo niega la escritura con frozen_test.
	code, _, errOut := hookRun(t, env, claudeTool(env, "Bash", "bflow-implementer", map[string]any{"command": "sed -i s/a/b/ internal/a_test.go"}), "guard")
	if code != 2 || !strings.Contains(errOut, frozenRel) {
		t.Errorf("sed -i sobre la congelada: %d %q", code, errOut)
	}
	code, _, errOut = hookRun(t, env, claudeTool(env, "Bash", "bflow-implementer", map[string]any{"command": "cat internal/a_test.go"}), "guard")
	if code != 0 {
		t.Errorf("cat se permite: %d %q", code, errOut)
	}
}

// adviceIn es el PostToolUse de Claude para un Bash con sesión.
func adviceIn(env *Env, cmd, session string) string {
	b, _ := json.Marshal(map[string]any{"hook_event_name": "PostToolUse", "cwd": env.Dir, "session_id": session,
		"tool_name": "Bash", "tool_input": map[string]any{"command": cmd}, "tool_response": map[string]any{}})
	return string(b)
}

func withQuick(t *testing.T, env *Env) {
	t.Helper()
	p := filepath.Join(env.Dir, "bflow.yaml")
	b, _ := os.ReadFile(p)
	if err := os.WriteFile(p, append(b, []byte("\ncheck:\n  quick: [\"go test {pkg}\"]\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
}

// GH-17: go test directo con check.quick → sale 2 con el aviso, en Claude y OpenCode.
func TestGuardPostAdvice(t *testing.T) {
	env, _, _, _ := tokensEnv(t, false)
	withQuick(t, env)
	code, _, errOut := hookRun(t, env, adviceIn(env, "go test ./...", "s1"), "guard", "--post")
	if code != 2 || !strings.Contains(errOut, "bflow guard:") || !strings.Contains(errOut, "bflow check --quick") || (!strings.Contains(errOut, "sí corrió") && !strings.Contains(errOut, "ran")) {
		t.Fatalf("claude: %d %q", code, errOut)
	}
	code, _, errOut = hookRun(t, env, guardIn(env, "bash", map[string]any{"command": "npm test"}, false, ""), "guard", "--tool", "opencode", "--post")
	if code != 2 || !strings.Contains(errOut, "bflow check --quick") {
		t.Fatalf("opencode: %d %q", code, errOut)
	}
	if code, out, errOut := hookRun(t, env, adviceIn(env, "echo go test", "s9"), "guard", "--post"); code != 0 || out != "" || errOut != "" {
		t.Errorf("echo no avisa: %d %q %q", code, out, errOut)
	}
}

func TestGuardPostAdviceOncePerSession(t *testing.T) {
	env, _, _, _ := tokensEnv(t, false)
	withQuick(t, env)
	if code, _, _ := hookRun(t, env, adviceIn(env, "go test ./a", "s1"), "guard", "--post"); code != 2 {
		t.Fatalf("la primera avisa: %d", code)
	}
	if code, out, errOut := hookRun(t, env, adviceIn(env, "go test ./b", "s1"), "guard", "--post"); code != 0 || out != "" || errOut != "" {
		t.Errorf("la segunda de la misma sesión calla: %d %q %q", code, out, errOut)
	}
	if code, _, _ := hookRun(t, env, adviceIn(env, "go test ./b", "s2"), "guard", "--post"); code != 2 {
		t.Errorf("otra sesión avisa: %d", code)
	}
	for i := 0; i < 2; i++ {
		if code, _, _ := hookRun(t, env, adviceIn(env, "go test ./b", ""), "guard", "--post"); code != 2 {
			t.Errorf("sin sesión avisa cada vez (%d): %d", i, code)
		}
	}
}

func TestGuardPostAdviceNoQuick(t *testing.T) {
	env, _, _, _ := tokensEnv(t, false)
	if code, out, errOut := hookRun(t, env, adviceIn(env, "go test ./...", "s1"), "guard", "--post"); code != 0 || out != "" || errOut != "" {
		t.Errorf("sin check.quick: %d %q %q", code, out, errOut)
	}
}

// GH-17 [N5]: si el mismo Bash restauró pruebas congeladas, solo sale ese aviso.
func TestGuardPostFrozenWins(t *testing.T) {
	env, _, _ := frozenTask(t, flow.Implementing)
	withQuick(t, env)
	writeFrozenFile(t, env, []byte("cambiada\n"))
	code, _, errOut := hookRun(t, env, adviceIn(env, "go test ./...", "s1"), "guard", "--post")
	if code != 2 || !strings.Contains(errOut, frozenRel) || strings.Contains(errOut, "check --quick") {
		t.Fatalf("gana el aviso congelado: %d %q", code, errOut)
	}
}
