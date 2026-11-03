package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	claudefiles "innobytes.tech/bflow/adapters/claude"
	claudeagent "innobytes.tech/bflow/internal/adapters/agent/claude"
	opencodeagent "innobytes.tech/bflow/internal/adapters/agent/opencode"
	"innobytes.tech/bflow/internal/release"
	"innobytes.tech/bflow/internal/testutil"
	"innobytes.tech/bflow/internal/tracker/trackertest"
)

// Con BFLOW_AS_CLI=1 este binario de pruebas se comporta como bflow: lo usan
// update (que ejecuta el binario nuevo) y doctor (que mide el arranque).
func TestMain(m *testing.M) {
	if os.Getenv("BFLOW_AS_CLI") == "1" {
		dir, _ := os.Getwd()
		os.Exit(Run(os.Args[1:], &Env{Stdout: os.Stdout, Stderr: os.Stderr, Stdin: os.Stdin, Version: "v9.9.9",
			Dir: dir, Agent: claudeagent.Agent{}, Tools: []ToolAdapter{claudeagent.Agent{}, opencodeagent.Agent{}}}))
	}
	os.Exit(m.Run())
}

// setHome aísla el home del usuario.
func setHome(t *testing.T) string {
	t.Helper()
	home := testutil.TempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func skillPath(home string) string {
	return filepath.Join(home, ".claude", "skills", "bflow", "SKILL.md")
}

func installEnv(t *testing.T, yaml string) *Env {
	t.Helper()
	env := ghEnv(t, yaml, trackertest.NewMemory())
	env.Agent = claudeagent.Agent{}
	return env
}

func dataOf(out map[string]any) map[string]any {
	d, _ := out["data"].(map[string]any)
	return d
}

func warningsOf(out map[string]any) string {
	ws, _ := dataOf(out)["warnings"].([]any)
	var b strings.Builder
	for _, w := range ws {
		b.WriteString(w.(string) + "\n")
	}
	return b.String()
}

func TestInstallClaude(t *testing.T) {
	home := setHome(t)
	env := installEnv(t, "agent: claude\n")

	code, out, raw := runJSON(t, env, "install", "claude")
	d := dataOf(out)
	if code != 0 || out["code"] != "installed" {
		t.Fatalf("install: %d %s", code, raw)
	}
	if b, err := os.ReadFile(skillPath(home)); err != nil || !bytes.Equal(b, claudefiles.Skill) {
		t.Errorf("la skill embebida debe quedar en %s: %v", skillPath(home), err)
	}
	settings := filepath.Join(env.Dir, ".claude", "settings.json")
	b, err := os.ReadFile(settings)
	if err != nil || !strings.Contains(string(b), "bflow guard") {
		t.Fatalf("settings.json del repo con los hooks: %v", err)
	}
	if d["skill"] != skillPath(home) || d["settings"] != settings || d["changed"] != true || d["render"] != true {
		t.Errorf("data: %v", d)
	}
	if m, _ := filepath.Glob(filepath.Join(env.Dir, ".claude", "agents", "bflow-*.md")); len(m) == 0 {
		t.Error("con agent: claude corre render y deja los agentes")
	}

	code, out, raw = runJSON(t, env, "install", "claude")
	if code != 0 || dataOf(out)["changed"] != false {
		t.Errorf("la segunda vez no cambia nada: %d %s", code, raw)
	}
	if again, _ := os.ReadFile(settings); !bytes.Equal(b, again) {
		t.Error("settings.json cambió en la segunda corrida")
	}
	if text := env.Stdout.(*bytes.Buffer).String(); strings.ContainsAny(text, "✅⚠❌") {
		t.Errorf("sin emojis: %q", text)
	}
}

func TestInstallOutsideRepo(t *testing.T) {
	home := setHome(t)
	dir := testutil.TempDir(t)
	env := &Env{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Dir: dir, Agent: claudeagent.Agent{}}

	code, out, raw := runJSON(t, env, "install", "claude")
	if code != 0 || out["code"] != "installed" {
		t.Fatalf("install fuera de un repo: %d %s", code, raw)
	}
	if _, err := os.Stat(skillPath(home)); err != nil {
		t.Errorf("la skill se instala igual: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude")); err == nil {
		t.Error("fuera de un repo no se escribe .claude")
	}
	d := dataOf(out)
	if d["settings"] != "" || d["render"] != false {
		t.Errorf("data: %v", d)
	}
	if w := warningsOf(out); !strings.Contains(w, "repo") || !strings.Contains(w, "install claude") {
		t.Errorf("avisa que los hooks se instalan por repo: %q", w)
	}
}

func TestInstallOtherAgentNoRender(t *testing.T) {
	setHome(t)
	env := installEnv(t, "tracker: { adapter: local }\n") // agent vacío

	code, out, raw := runJSON(t, env, "install", "claude")
	if code != 0 {
		t.Fatalf("install: %d %s", code, raw)
	}
	if _, err := os.Stat(filepath.Join(env.Dir, ".claude", "settings.json")); err != nil {
		t.Errorf("instala los hooks: %v", err)
	}
	if _, err := os.Stat(filepath.Join(env.Dir, ".claude", "agents")); err == nil {
		t.Error("sin agent: claude no corre render")
	}
	if dataOf(out)["render"] != false || !strings.Contains(warningsOf(out), "agent:") {
		t.Errorf("avisa que agent: no incluye claude: %v", dataOf(out))
	}
}

func TestInstallSkillOnly(t *testing.T) {
	home := setHome(t)
	env := installEnv(t, "agent: claude\n")

	code, out, raw := runJSON(t, env, "install", "claude", "--skill-only")
	if code != 0 {
		t.Fatalf("install: %d %s", code, raw)
	}
	if _, err := os.Stat(skillPath(home)); err != nil {
		t.Errorf("instala la skill: %v", err)
	}
	if _, err := os.Stat(filepath.Join(env.Dir, ".claude")); err == nil {
		t.Error("--skill-only no toca el repo")
	}
	if d := dataOf(out); d["settings"] != "" || d["render"] != false {
		t.Errorf("data: %v", d)
	}
}

func TestInstallOpencodeTool(t *testing.T) {
	home := setHome(t)
	_, command := setXDG(t)
	env := toolsEnv(t, "agent: claude\n")

	code, out, raw := runJSON(t, env, "install", "opencode")
	if code != 0 || out["code"] != "installed" || dataOf(out)["skill"] != command {
		t.Errorf("opencode ya tiene adaptador: %d %s", code, raw)
	}
	if _, err := os.Stat(command); err != nil {
		t.Errorf("instala el comando: %v", err)
	}
	if _, err := os.Stat(skillPath(home)); err == nil {
		t.Error("no instala la skill de Claude")
	}
}

func TestInstallUnknownTool(t *testing.T) {
	home := setHome(t)
	for _, args := range [][]string{{"install", "vim"}, {"install"}} {
		env := installEnv(t, "agent: claude\n")
		code, out, raw := runJSON(t, env, args...)
		if code != 1 || out["code"] != "usage" || !strings.Contains(raw, "claude") || !strings.Contains(raw, "opencode") {
			t.Errorf("%v: debe listar las herramientas disponibles: %d %s", args, code, raw)
		}
	}
	if _, err := os.Stat(skillPath(home)); err == nil {
		t.Error("no instala nada")
	}
}

// releaseServer publica v9.9.9 con bin como binario de esta plataforma.
func releaseServer(t *testing.T, bin []byte) {
	t.Helper()
	dir := testutil.TempDir(t)
	src := filepath.Join(dir, release.BinaryName(runtime.GOOS))
	if err := os.WriteFile(src, bin, 0o755); err != nil {
		t.Fatal(err)
	}
	name := release.AssetName("v9.9.9", runtime.GOOS, runtime.GOARCH)
	archive := filepath.Join(dir, name)
	if err := release.Archive(archive, src, runtime.GOOS); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	sums := release.FormatChecksums(map[string]string{name: release.SHA256(data)}, []string{name})
	files := map[string][]byte{"/" + name: data, "/" + release.Checksums: []byte(sums)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	files["/releases/latest"], _ = json.Marshal(map[string]any{"tag_name": "v9.9.9", "assets": []map[string]string{
		{"name": name, "browser_download_url": srv.URL + "/" + name},
		{"name": release.Checksums, "browser_download_url": srv.URL + "/" + release.Checksums}}})
	t.Setenv("BFLOW_RELEASES_URL", srv.URL)
}

func TestUpdateRefreshesSkill(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	newBin, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("BFLOW_AS_CLI", "1")
	update := func(t *testing.T, bin []byte, asText bool) (map[string]any, string) {
		t.Helper()
		releaseServer(t, bin)
		exe := filepath.Join(testutil.TempDir(t), release.BinaryName(runtime.GOOS))
		if err := os.WriteFile(exe, []byte("viejo"), 0o755); err != nil {
			t.Fatal(err)
		}
		old := executable
		executable = func() (string, error) { return exe, nil }
		t.Cleanup(func() { executable = old })
		env := &Env{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Version: "v0.0.1", Dir: testutil.TempDir(t), Agent: claudeagent.Agent{}}
		if asText {
			code := Run([]string{"update"}, env)
			text := env.Stdout.(*bytes.Buffer).String()
			if code != 0 {
				t.Fatalf("update debe terminar OK aunque falle el refresco: %d %s", code, text)
			}
			return nil, text
		}
		code, out, raw := runJSON(t, env, "update")
		if code != 0 || out["code"] != "updated" {
			t.Fatalf("update: %d %s", code, raw)
		}
		return dataOf(out), ""
	}

	t.Run("instalada: se refresca", func(t *testing.T) {
		home := setHome(t)
		if err := os.MkdirAll(filepath.Dir(skillPath(home)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(skillPath(home), []byte("vieja"), 0o644); err != nil {
			t.Fatal(err)
		}
		d, _ := update(t, newBin, false)
		if d["skill"] != "updated" {
			t.Errorf("data: %v", d)
		}
		if b, _ := os.ReadFile(skillPath(home)); !bytes.Equal(b, claudefiles.Skill) {
			t.Error("la skill quedó con la versión del binario nuevo")
		}
	})
	t.Run("no instalada: no se instala", func(t *testing.T) {
		home := setHome(t)
		d, _ := update(t, newBin, false)
		if d["skill"] != "skipped" {
			t.Errorf("data: %v", d)
		}
		if _, err := os.Stat(skillPath(home)); err == nil {
			t.Error("update no instala una skill que no existía")
		}
	})
	t.Run("falla el refresco: update sigue OK", func(t *testing.T) {
		home := setHome(t)
		if err := os.MkdirAll(filepath.Dir(skillPath(home)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(skillPath(home), []byte("vieja"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, text := update(t, []byte("esto no es un ejecutable"), true)
		if !strings.Contains(text, "bflow install claude") {
			t.Errorf("avisa cómo refrescarla: %q", text)
		}
		if !strings.Contains(text, "reinicia los paneles abiertos") && !strings.Contains(text, "restart open dashboards") {
			t.Errorf("update avisa que hay que reiniciar los paneles: %q", text)
		}
		if b, _ := os.ReadFile(skillPath(home)); string(b) != "vieja" {
			t.Error("la skill no cambia si el refresco falla")
		}
	})
}

// doctorChecks corre bflow doctor y devuelve los ítems por área.
func doctorChecks(t *testing.T, env *Env) map[string][]map[string]any {
	t.Helper()
	t.Setenv("BFLOW_AS_CLI", "1") // doctor mide el arranque ejecutando este binario
	_, out, raw := runJSON(t, env, "doctor")
	items, _ := dataOf(out)["checks"].([]any)
	if len(items) == 0 {
		t.Fatalf("doctor sin ítems: %s", raw)
	}
	by := map[string][]map[string]any{}
	for _, it := range items {
		m := it.(map[string]any)
		by[m["area"].(string)] = append(by[m["area"].(string)], m)
	}
	return by
}

func TestDoctorSkillStale(t *testing.T) {
	home := setHome(t)
	env := installEnv(t, "agent: claude\n")
	skill := func() map[string]any {
		c := doctorChecks(t, env)["skill"]
		if len(c) != 1 {
			t.Fatalf("doctor da un ítem skill: %v", c)
		}
		return c[0]
	}

	if s := skill(); s["status"] != "warn" || !strings.Contains(s["detail"].(string), "bflow install claude") {
		t.Errorf("sin skill: %v", s)
	}
	if err := os.MkdirAll(filepath.Dir(skillPath(home)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath(home), []byte("vieja"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s := skill(); s["status"] != "warn" || !strings.Contains(s["detail"].(string), "bflow install claude") {
		t.Errorf("skill distinta: %v", s)
	}
	crlf := bytes.ReplaceAll(claudefiles.Skill, []byte("\n"), []byte("\r\n"))
	if err := os.WriteFile(skillPath(home), crlf, 0o644); err != nil {
		t.Fatal(err)
	}
	if s := skill(); s["status"] != "ok" {
		t.Errorf("igual tras normalizar CRLF: %v", s)
	}

	agent := doctorChecks(t, env)["agente"]
	for _, a := range agent {
		if d := a["detail"].(string); strings.Contains(d, "adapters/claude/settings.json") {
			t.Errorf("los avisos de hooks sugieren bflow install claude, no copiar a mano: %q", d)
		}
	}
	found := false
	for _, a := range agent {
		found = found || strings.Contains(a["detail"].(string), "bflow install claude")
	}
	if !found {
		t.Errorf("falta el aviso de hooks con bflow install claude: %v", agent)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

func gitCommit(t *testing.T, dir, email string) {
	t.Helper()
	git(t, dir, "-c", "user.email="+email, "-c", "user.name=x", "commit", "-q", "--allow-empty", "-m", "x")
}

func TestDoctorLocalTrackerManyAuthors(t *testing.T) {
	setHome(t)
	sharedWarn := func(env *Env) (string, bool) {
		for _, it := range doctorChecks(t, env)["tracker"] {
			if it["status"] == "warn" {
				return it["detail"].(string), true
			}
		}
		return "", false
	}

	env := installEnv(t, "tracker: { adapter: local }\n")
	git(t, env.Dir, "init", "-q")
	gitCommit(t, env.Dir, "uno@example.com")
	if _, warn := sharedWarn(env); warn {
		t.Error("un solo autor no avisa")
	}
	gitCommit(t, env.Dir, "dos@example.com")
	detail, warn := sharedWarn(env)
	if !warn || !strings.Contains(detail, ".bflow") || !strings.Contains(detail, "github") || !strings.Contains(detail, "plane") {
		t.Fatalf("dos autores: .bflow no se comparte, sugiere github o plane: %q", detail)
	}
	if strings.Contains(detail, "example.com") {
		t.Errorf("no imprime emails: %q", detail)
	}
}
