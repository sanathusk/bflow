package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	claudefiles "innobytes.tech/bflow/adapters/claude"
	opencodefiles "innobytes.tech/bflow/adapters/opencode"
	claudeagent "innobytes.tech/bflow/internal/adapters/agent/claude"
	opencodeagent "innobytes.tech/bflow/internal/adapters/agent/opencode"
	"innobytes.tech/bflow/internal/config"
	"innobytes.tech/bflow/internal/release"
	"innobytes.tech/bflow/internal/testutil"
	"innobytes.tech/bflow/internal/tracker/trackertest"
)

// bothTools son las herramientas que registra cmd/bflow.
func bothTools() []ToolAdapter {
	return []ToolAdapter{claudeagent.Agent{}, opencodeagent.Agent{}}
}

// toolsEnv es un repo con bflow.yaml y las dos herramientas registradas.
func toolsEnv(t *testing.T, yaml string) *Env {
	t.Helper()
	env := ghEnv(t, yaml, trackertest.NewMemory())
	env.Agent = claudeagent.Agent{}
	env.Tools = bothTools()
	return env
}

// setXDG fija XDG_CONFIG_HOME en una carpeta nueva y devuelve dónde queda el comando /bflow.
func setXDG(t *testing.T) (xdg, command string) {
	t.Helper()
	xdg = testutil.TempDir(t)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	return xdg, filepath.Join(xdg, "opencode", "commands", "bflow.md")
}

func rendered(t *testing.T, env *Env, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(env.Dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("falta %s: %v", rel, err)
	}
	return string(b)
}

func exists(env *Env, rel string) bool {
	_, err := os.Stat(filepath.Join(env.Dir, filepath.FromSlash(rel)))
	return err == nil
}

func stringsOf(v any) []string {
	var out []string
	vs, _ := v.([]any)
	for _, x := range vs {
		out = append(out, x.(string))
	}
	return out
}

func TestRenderMultiTool(t *testing.T) {
	setHome(t)
	env := toolsEnv(t, "agent: [claude, opencode]\nmodels:\n  opencode: { sonnet: anthropic/claude-sonnet-4, haiku: anthropic/claude-haiku-4, opus: anthropic/claude-opus-4 }\n")

	code, out, raw := runJSON(t, env, "render")
	d := dataOf(out)
	if code != 0 || out["code"] != "rendered" || d["total"] != float64(13) || len(stringsOf(d["changed"])) != 13 {
		t.Fatalf("5 agentes del flujo (con el scout) por cada herramienta y el plugin de OpenCode: %d %s", code, raw)
	}
	if un, _ := d["unresolved"].([]any); len(un) != 0 {
		t.Errorf("con la tabla completa no queda nada sin resolver: %v", un)
	}
	cl := rendered(t, env, ".claude/agents/bflow-implementer.md")
	if !strings.Contains(cl, "\nmodel: sonnet\n") || strings.Contains(cl, "mode: subagent") {
		t.Errorf("Claude sigue con el alias directo (R7):\n%s", cl)
	}
	oc := rendered(t, env, ".opencode/agents/bflow-implementer.md")
	for _, w := range []string{"mode: subagent", "model: anthropic/claude-sonnet-4", "edit: allow", "bash: allow"} {
		if !strings.Contains(oc, w) {
			t.Errorf("el agente de OpenCode no trae %q:\n%s", w, oc)
		}
	}
	if !strings.Contains(oc, "## Contract with bflow") && !strings.Contains(oc, "## Contrato con bflow") {
		t.Errorf("el agente de OpenCode no trae el contrato:\n%s", oc)
	}
	if doc := rendered(t, env, ".opencode/agents/bflow-documenter.md"); !strings.Contains(doc, "model: anthropic/claude-haiku-4") {
		t.Errorf("el alias haiku se traduce:\n%s", doc)
	}
	if sc := rendered(t, env, ".opencode/agents/bflow-scout.md"); !strings.Contains(sc, "bash: allow") || strings.Contains(sc, "edit: allow") {
		t.Errorf("el scout de OpenCode lee y corre bash, sin edit (R23):%s", sc)
	}
	if sa := rendered(t, env, ".opencode/agents/bflow-spec-author.md"); !strings.Contains(sa, "model: anthropic/claude-opus-4") {
		t.Errorf("el alias opus del spec-author se traduce:\n%s", sa)
	}

	code, out, raw = runJSON(t, env, "render", "--check")
	if code != 0 || out["code"] != "render_ok" {
		t.Fatalf("al día, --check pasa: %d %s", code, raw)
	}
	if err := os.Remove(filepath.Join(env.Dir, ".opencode", "agents", "bflow-reviewer.md")); err != nil {
		t.Fatal(err)
	}
	code, out, raw = runJSON(t, env, "render", "--check")
	if code == 0 || out["code"] != "render_outdated" || !strings.Contains(raw, ".opencode/agents/bflow-reviewer.md") {
		t.Errorf("--check cubre los archivos de todas las herramientas (R10): %d %s", code, raw)
	}
}

func TestRenderStaleInactiveTool(t *testing.T) {
	setHome(t)
	env := toolsEnv(t, "agent: [claude, opencode]\n")
	if code, _, raw := runJSON(t, env, "render"); code != 0 {
		t.Fatalf("render: %s", raw)
	}
	mine := filepath.Join(env.Dir, ".opencode", "agents", "bflow-mio.md")
	if err := os.WriteFile(mine, []byte("---\ndescription: mío\n---\nmío\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	setAgent := func(v string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(env.Dir, "bflow.yaml"), []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	setAgent("agent: claude\n")
	code, out, raw := runJSON(t, env, "render", "--check")
	if code == 0 || out["code"] != "render_outdated" || len(stringsOf(dataOf(out)["stale"])) != 8 {
		t.Fatalf("lo generado de una herramienta que ya no está en agent: sale stale: %d %s", code, raw)
	}
	code, out, raw = runJSON(t, env, "render")
	if code != 0 || out["code"] != "rendered" || len(stringsOf(dataOf(out)["stale"])) != 8 {
		t.Fatalf("render borra lo stale: %d %s", code, raw)
	}
	if exists(env, ".opencode/agents/bflow-implementer.md") {
		t.Error("el agente generado de OpenCode debe borrarse")
	}
	if !exists(env, ".claude/agents/bflow-implementer.md") {
		t.Error("lo de Claude no se toca")
	}
	if b, err := os.ReadFile(mine); err != nil || !strings.Contains(string(b), "mío") {
		t.Error("un archivo sin la marca nunca se borra")
	}

	setAgent("agent: [claude, opencode]\n")
	handmade := filepath.Join(env.Dir, ".opencode", "agents", "bflow-implementer.md")
	if err := os.WriteFile(handmade, []byte("lo escribí yo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, raw = runJSON(t, env, "render")
	if code != 1 || out["code"] != "render_conflict" || !strings.Contains(raw, ".opencode/agents/bflow-implementer.md") {
		t.Errorf("un archivo sin marca en una ruta que render escribiría es conflicto: %d %s", code, raw)
	}
	if b, _ := os.ReadFile(handmade); string(b) != "lo escribí yo\n" {
		t.Error("no pisa el archivo de la persona")
	}
}

func TestRenderUnresolved(t *testing.T) {
	setHome(t)
	env := toolsEnv(t, "agent: [claude, opencode]\n")

	code, out, raw := runJSON(t, env, "render")
	if code != 0 {
		t.Fatalf("un alias sin resolver no es un fallo: %s", raw)
	}
	b, _ := json.Marshal(dataOf(out)["unresolved"])
	var un []struct {
		Tool, Alias string
		Agents      []string
	}
	if err := json.Unmarshal(b, &un); err != nil || len(un) != 3 {
		t.Fatalf("data.unresolved: %s", b)
	}
	if un[0].Tool != "opencode" || un[0].Alias != "haiku" || !slices.Equal(un[0].Agents, []string{"bflow-documenter", "bflow-scout"}) ||
		un[1].Tool != "opencode" || un[1].Alias != "opus" || !slices.Equal(un[1].Agents, []string{"bflow-reviewer", "bflow-spec-author"}) ||
		un[2].Tool != "opencode" || un[2].Alias != "sonnet" || !slices.Equal(un[2].Agents, []string{"bflow-implementer"}) {
		t.Errorf("por herramienta, ordenado por alias, con sus agentes ordenados: %+v", un)
	}
	if oc := rendered(t, env, ".opencode/agents/bflow-implementer.md"); strings.Contains(oc, "model:") {
		t.Errorf("sin equivalente se omite model:\n%s", oc)
	}

	env.Stdout = &bytes.Buffer{}
	Run([]string{"render"}, env)
	text := env.Stdout.(*bytes.Buffer).String()
	if !strings.Contains(text, "aviso:") || !strings.Contains(text, "haiku") || !strings.Contains(text, "sonnet") || !strings.Contains(text, "bflow-documenter") {
		t.Errorf("el texto avisa qué alias y qué agentes: %q", text)
	}

	if err := os.WriteFile(filepath.Join(env.Dir, "bflow.yaml"), []byte("agent: [claude, opencode]\nmodels:\n  opencode: { sonnet: a/s }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, out, raw = runJSON(t, env, "render")
	b, _ = json.Marshal(dataOf(out)["unresolved"])
	if strings.Contains(string(b), "sonnet") || !strings.Contains(string(b), "haiku") {
		t.Errorf("solo queda haiku sin resolver: %s", raw)
	}
}

func TestRenderEmptyAgentClaudeOnly(t *testing.T) {
	setHome(t)
	for _, yaml := range []string{"stack: go\n", "agent: \"\"\n", "agent: claude\n"} {
		env := toolsEnv(t, yaml)
		code, out, raw := runJSON(t, env, "render")
		if code != 0 || out["code"] != "rendered" {
			t.Fatalf("%q: render: %d %s", yaml, code, raw)
		}
		if !exists(env, ".claude/agents/bflow-implementer.md") {
			t.Errorf("%q: genera los de Claude", yaml)
		}
		if exists(env, ".opencode") {
			t.Errorf("%q: sin opencode en agent: no se escribe .opencode (R9)", yaml)
		}
	}
}

func TestInstallOpenCode(t *testing.T) {
	home := setHome(t)
	_, command := setXDG(t)

	t.Run("instala el comando y corre render", func(t *testing.T) {
		env := toolsEnv(t, "agent: [claude, opencode]\n")
		code, out, raw := runJSON(t, env, "install", "opencode")
		d := dataOf(out)
		if code != 0 || out["code"] != "installed" {
			t.Fatalf("install opencode: %d %s", code, raw)
		}
		if d["skill"] != command || d["settings"] != "" || d["render"] != true {
			t.Errorf("data: %v", d)
		}
		if b, err := os.ReadFile(command); err != nil || len(b) == 0 || !bytes.Equal(b, opencodefiles.Command) {
			t.Errorf("el comando embebido queda en %s: %v", command, err)
		}
		if !exists(env, ".opencode/agents/bflow-implementer.md") {
			t.Error("con opencode en agent: corre render")
		}
		if exists(env, ".claude/settings.json") {
			t.Error("install opencode no toca los hooks de Claude")
		}
		if _, err := os.Stat(skillPath(home)); err == nil {
			t.Error("install opencode no instala la skill de Claude")
		}
	})
	t.Run("--skill-only solo escribe el comando", func(t *testing.T) {
		env := toolsEnv(t, "agent: [claude, opencode]\n")
		code, out, raw := runJSON(t, env, "install", "opencode", "--skill-only")
		if code != 0 || dataOf(out)["render"] != false || dataOf(out)["skill"] != command {
			t.Fatalf("%d %s", code, raw)
		}
		if exists(env, ".opencode") {
			t.Error("--skill-only no toca el repo")
		}
	})
	t.Run("sin bflow.yaml avisa que los agentes son por repo", func(t *testing.T) {
		env := &Env{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Dir: testutil.TempDir(t), Agent: claudeagent.Agent{}, Tools: bothTools()}
		code, out, raw := runJSON(t, env, "install", "opencode")
		if code != 0 || out["code"] != "installed" || dataOf(out)["render"] != false {
			t.Fatalf("%d %s", code, raw)
		}
		if w := warningsOf(out); !strings.Contains(w, "por repo") && !strings.Contains(w, "per repo") {
			t.Errorf("avisa que los agentes se generan por repo: %q", w)
		}
		if exists(env, ".opencode") {
			t.Error("fuera de un repo no se escribe .opencode")
		}
	})
	t.Run("sin XDG_CONFIG_HOME usa ~/.config", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "")
		env := toolsEnv(t, "agent: opencode\n")
		code, out, raw := runJSON(t, env, "install", "opencode", "--skill-only")
		want := filepath.Join(home, ".config", "opencode", "commands", "bflow.md")
		if code != 0 || dataOf(out)["skill"] != want {
			t.Errorf("%d %s", code, raw)
		}
		if _, err := os.Stat(want); err != nil {
			t.Error(err)
		}
	})
	t.Run("XDG_CONFIG_HOME relativa falla", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "relativa")
		env := toolsEnv(t, "agent: opencode\n")
		code, out, raw := runJSON(t, env, "install", "opencode")
		if code != 1 || out["code"] != "install" || !strings.Contains(raw, "XDG_CONFIG_HOME") {
			t.Errorf("%d %s", code, raw)
		}
	})
}

func TestInstallOpenCodeNotInAgent(t *testing.T) {
	setHome(t)
	_, command := setXDG(t)
	for _, yaml := range []string{"agent: claude\n", "tracker: { adapter: local }\n"} {
		env := toolsEnv(t, yaml)
		code, out, raw := runJSON(t, env, "install", "opencode")
		if code != 0 || out["code"] != "installed" {
			t.Fatalf("%q: %d %s", yaml, code, raw)
		}
		if dataOf(out)["render"] != false || exists(env, ".opencode") {
			t.Errorf("%q: sin opencode en agent: no corre render: %v", yaml, dataOf(out))
		}
		if w := warningsOf(out); !strings.Contains(w, "agent: [claude, opencode]") || !strings.Contains(w, "bflow render") {
			t.Errorf("%q: avisa cómo agregarlo: %q", yaml, w)
		}
		if _, err := os.Stat(command); err != nil {
			t.Errorf("%q: el comando se instala igual: %v", yaml, err)
		}
	}
}

// updateRun corre bflow update con bin como binario nuevo y devuelve data y texto.
func updateRun(t *testing.T, bin []byte) (map[string]any, string) {
	t.Helper()
	t.Setenv("BFLOW_AS_CLI", "1")
	releaseServer(t, bin)
	exe := filepath.Join(testutil.TempDir(t), release.BinaryName(runtime.GOOS))
	if err := os.WriteFile(exe, []byte("viejo"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := executable
	executable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { executable = old })
	env := &Env{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Version: "v0.0.1", Dir: testutil.TempDir(t), Agent: claudeagent.Agent{}, Tools: bothTools()}
	code, out, raw := runJSON(t, env, "update")
	if code != 0 || out["code"] != "updated" {
		t.Fatalf("update debe terminar OK aunque falle el refresco: %d %s", code, raw)
	}

	textBuf := &bytes.Buffer{}
	textEnv := &Env{Stdout: textBuf, Stderr: &bytes.Buffer{}, Version: "v0.0.1", Dir: env.Dir, Agent: claudeagent.Agent{}, Tools: bothTools()}
	if code := Run([]string{"update"}, textEnv); code != 0 {
		t.Fatalf("update (texto) debe terminar OK aunque falle el refresco: %d %s", code, textBuf.String())
	}
	return dataOf(out), textBuf.String()
}

func TestUpdateRefreshesIntegrations(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	newBin, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	put := func(t *testing.T, path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	integrations := func(t *testing.T, d map[string]any) map[string]any {
		t.Helper()
		m, _ := d["integrations"].(map[string]any)
		if m == nil {
			t.Fatalf("data.integrations: %v", d)
		}
		return m
	}

	t.Run("las dos instaladas: se refrescan", func(t *testing.T) {
		home := setHome(t)
		_, command := setXDG(t)
		put(t, skillPath(home), "vieja")
		put(t, command, "vieja")
		d, _ := updateRun(t, newBin)
		if in := integrations(t, d); in["claude"] != "updated" || in["opencode"] != "updated" || d["skill"] != "updated" {
			t.Errorf("data: %v", d)
		}
		if b, _ := os.ReadFile(command); !bytes.Equal(b, opencodefiles.Command) {
			t.Error("el comando quedó con la versión del binario nuevo")
		}
		if b, _ := os.ReadFile(skillPath(home)); !bytes.Equal(b, claudefiles.Skill) {
			t.Error("la skill quedó con la versión del binario nuevo")
		}
	})
	t.Run("solo opencode instalada", func(t *testing.T) {
		home := setHome(t)
		_, command := setXDG(t)
		put(t, command, "vieja")
		d, _ := updateRun(t, newBin)
		if in := integrations(t, d); in["claude"] != "skipped" || in["opencode"] != "updated" || d["skill"] != "skipped" {
			t.Errorf("data: %v", d)
		}
		if _, err := os.Stat(skillPath(home)); err == nil {
			t.Error("update no instala una skill que no existía")
		}
	})
	t.Run("ninguna instalada", func(t *testing.T) {
		setHome(t)
		_, command := setXDG(t)
		d, _ := updateRun(t, newBin)
		if in := integrations(t, d); in["claude"] != "skipped" || in["opencode"] != "skipped" || d["skill"] != "skipped" {
			t.Errorf("data: %v", d)
		}
		if _, err := os.Stat(command); err == nil {
			t.Error("update no instala un comando que no existía")
		}
	})
	t.Run("falla el refresco: update sigue OK y dice qué correr", func(t *testing.T) {
		home := setHome(t)
		_, command := setXDG(t)
		put(t, skillPath(home), "vieja")
		put(t, command, "vieja")
		d, text := updateRun(t, []byte("esto no es un ejecutable"))
		if in := integrations(t, d); in["claude"] != "failed" || in["opencode"] != "failed" || d["skill"] != "failed" {
			t.Errorf("data: %v", d)
		}
		if (!strings.Contains(text, "corre bflow install claude") && !strings.Contains(text, "run bflow install claude")) ||
			(!strings.Contains(text, "corre bflow install opencode") && !strings.Contains(text, "run bflow install opencode")) {
			t.Errorf("avisa cómo refrescar cada una: %q", text)
		}
		if b, _ := os.ReadFile(command); string(b) != "vieja" {
			t.Error("el comando no cambia si el refresco falla")
		}
	})
}

func TestInitDetectsOpenCode(t *testing.T) {
	cases := []struct {
		name   string
		make   []string // rutas a crear (con / final: carpeta)
		want   string   // línea agent: esperada; "" = ninguna
		opencd bool     // el texto "siguiente" nombra bflow install opencode
	}{
		{"carpeta .opencode", []string{".opencode/"}, "agent: opencode", true},
		{"opencode.json", []string{"opencode.json"}, "agent: opencode", true},
		{"opencode.jsonc", []string{"opencode.jsonc"}, "agent: opencode", true},
		{"carpeta .claude", []string{".claude/"}, "agent: claude", false},
		{"CLAUDE.md", []string{"CLAUDE.md"}, "agent: claude", false},
		{"las dos", []string{"CLAUDE.md", ".opencode/"}, "agent: [claude, opencode]", true},
		{"ninguna", nil, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env, _ := initEnv(t, "", nil, nil)
			env.Agent, env.Tools = claudeagent.Agent{}, bothTools()
			for _, p := range c.make {
				abs := filepath.Join(env.Dir, p)
				if strings.HasSuffix(p, "/") {
					if err := os.MkdirAll(abs, 0o755); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(abs, []byte("x\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if code := Run([]string{"init", "--yes"}, env); code != 0 {
				t.Fatalf("exit %d: %s", code, env.Stdout.(*bytes.Buffer))
			}
			var line string
			for _, l := range strings.Split(initYAML(t, env), "\n") {
				if strings.HasPrefix(l, "agent:") {
					line = l
				}
			}
			if line != c.want {
				t.Errorf("agent: %q, quiero %q", line, c.want)
			}
			out := env.Stdout.(*bytes.Buffer).String()
			if has := strings.Contains(out, "bflow install opencode"); has != c.opencd {
				t.Errorf("el siguiente paso de opencode (R20): %v\n%s", has, out)
			}
			if c.opencd && !strings.Contains(out, "models.opencode") {
				t.Errorf("dice dónde definir models.opencode:\n%s", out)
			}
			if strings.Contains(c.want, "claude") && !strings.Contains(out, "bflow render (genera los agentes en .claude/agents") {
				t.Errorf("conserva la línea de claude:\n%s", out)
			}
		})
	}
}

func TestInitAgentFlagList(t *testing.T) {
	ok := map[string]string{
		"claude,opencode": "agent: [claude, opencode]",
		"opencode":        "agent: opencode",
		"claude":          "agent: claude",
	}
	for flagValue, want := range ok {
		env, _ := initEnv(t, "", nil, nil)
		env.Agent, env.Tools = claudeagent.Agent{}, bothTools()
		if code := Run([]string{"init", "--yes", "--agent", flagValue}, env); code != 0 {
			t.Fatalf("--agent %s: exit %d: %s", flagValue, code, env.Stdout.(*bytes.Buffer))
		}
		if y := initYAML(t, env); !strings.Contains(y, want+"\n") {
			t.Errorf("--agent %s: falta %q:\n%s", flagValue, want, y)
		}
	}
	for _, flagValue := range []string{"claude,vim", "vim", "claude,claude", "opencode,claude,opencode"} {
		env, _ := initEnv(t, "", nil, nil)
		env.Agent, env.Tools = claudeagent.Agent{}, bothTools()
		code, out, raw := runJSON(t, env, "init", "--yes", "--agent", flagValue)
		if code != 1 || out["code"] != "usage" {
			t.Errorf("--agent %s debe fallar con usage: %d %s", flagValue, code, raw)
		}
		if _, err := os.Stat(filepath.Join(env.Dir, "bflow.yaml")); err == nil {
			t.Errorf("--agent %s: no escribe bflow.yaml", flagValue)
		}
	}
}

func TestDoctorOpenCode(t *testing.T) {
	setHome(t)
	_, command := setXDG(t)
	env := toolsEnv(t, "agent: [claude, opencode]\n")

	checks := doctorChecks(t, env)
	if n := len(checks["agentes"]); n != 1 {
		t.Errorf("el chequeo agentes corre una sola vez y cubre todas las herramientas: %d", n)
	}
	hasVersion := false
	for _, it := range checks["agente"] {
		d := it["detail"].(string)
		hasVersion = hasVersion || (strings.HasPrefix(d, "opencode:") && (strings.Contains(d, "versión") || strings.Contains(d, "version")))
	}
	if !hasVersion {
		t.Errorf("reporta la versión de opencode (ok o warn si no corre): %v", checks["agente"])
	}
	cmd := checks["comando"]
	if len(cmd) != 1 || cmd[0]["status"] != "warn" || !strings.Contains(cmd[0]["detail"].(string), "bflow install opencode") {
		t.Errorf("comando /bflow faltante: %v", cmd)
	}
	mod := checks["modelos"]
	if len(mod) != 1 || mod[0]["status"] != "warn" {
		t.Fatalf("alias sin resolver: %v", mod)
	}
	for _, w := range []string{"haiku", "sonnet", "bflow-documenter", "bflow-implementer", config.GlobalPath()} {
		if !strings.Contains(mod[0]["detail"].(string), w) {
			t.Errorf("modelos: falta %q en %q", w, mod[0]["detail"])
		}
	}
	if g := checks["guard"]; len(g) != 0 {
		t.Errorf("ya no hay aviso de que el guard llega con GH-14: %v", g)
	}

	if err := os.MkdirAll(filepath.Dir(command), 0o755); err != nil {
		t.Fatal(err)
	}
	crlf := bytes.ReplaceAll(opencodefiles.Command, []byte("\n"), []byte("\r\n"))
	if err := os.WriteFile(command, crlf, 0o644); err != nil {
		t.Fatal(err)
	}
	if cmd := doctorChecks(t, env)["comando"]; len(cmd) != 1 || cmd[0]["status"] != "ok" {
		t.Errorf("comando al día (normalizando CRLF): %v", cmd)
	}
	if err := os.WriteFile(command, []byte("vieja"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cmd := doctorChecks(t, env)["comando"]; len(cmd) != 1 || cmd[0]["status"] != "warn" || !strings.Contains(cmd[0]["detail"].(string), "bflow install opencode") {
		t.Errorf("comando distinto: %v", cmd)
	}

	full := toolsEnv(t, "agent: [claude, opencode]\nmodels:\n  opencode: { sonnet: a/s, haiku: a/h, opus: a/o }\n")
	for _, it := range doctorChecks(t, full)["modelos"] {
		if it["status"] == "warn" {
			t.Errorf("con la tabla completa no hay aviso de modelos: %v", it)
		}
	}
}

func TestDoctorOpenCodeOnly(t *testing.T) {
	setHome(t)
	setXDG(t)
	env := toolsEnv(t, "agent: opencode\n")
	checks := doctorChecks(t, env)
	if n := len(checks["agentes"]); n != 1 {
		t.Errorf("agentes corre una vez con solo opencode: %d", n)
	}
	if len(checks["skill"]) != 0 {
		t.Errorf("los chequeos de Claude solo corren con claude en agent: %v", checks["skill"])
	}
	for _, it := range checks["agente"] {
		if strings.Contains(it["detail"].(string), "claude") {
			t.Errorf("sin claude en agent: no se habla de claude: %v", it)
		}
	}
	if len(checks["comando"]) != 1 {
		t.Errorf("el bloque de opencode corre: %v", checks)
	}
}
