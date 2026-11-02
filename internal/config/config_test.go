package config

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"innobytes.tech/bflow/internal/flow"
	"innobytes.tech/bflow/internal/review"
)

// fixture crea un repo y un directorio de config global aislados.
func fixture(t *testing.T, global, repo string) (repoDir string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("BFLOW_CONFIG_HOME", home)
	repoDir = t.TempDir()
	if global != "" {
		write(t, filepath.Join(home, "config.yaml"), global)
	}
	if repo != "" {
		write(t, filepath.Join(repoDir, "bflow.yaml"), repo)
	}
	return repoDir
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.TrimLeft(content, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultsWithoutFiles(t *testing.T) {
	c, err := Load(fixture(t, "", ""))
	if err != nil {
		t.Fatal(err)
	}
	if c.Tracker.Adapter != "local" {
		t.Errorf("tracker por defecto %q, want local", c.Tracker.Adapter)
	}
	if c.VCS.Host != "" {
		t.Errorf("sin host remoto por defecto, got %q", c.VCS.Host)
	}
	if c.VCS.Remote != "origin" || c.VCS.BranchPrefix.Feature != "feature/" || c.VCS.BranchPrefix.Hotfix != "hotfix/" {
		t.Errorf("vcs por defecto: %+v", c.VCS)
	}
	if c.Flow.MaxQualityRounds != 2 || c.Flow.SLAHours != 24 {
		t.Errorf("flow por defecto: %+v", c.Flow)
	}
	if c.Sources.Repo != "" || c.Sources.Global != "" {
		t.Errorf("no debería haber fuentes: %+v", c.Sources)
	}
	if err := c.Flow.Core().Validate(); err != nil {
		t.Errorf("flow por defecto inválido: %v", err)
	}
}

const globalMS = `
profiles:
  acme:
    tracker: { adapter: plane, url: https://plane.example.com, workspace: acme-dev }
    vcs: { host: github, base_branch: dev }
    agent: claude
    check:
      steps:
        - { name: vet, run: "go vet ./..." }
  otro:
    vcs: { base_branch: main }
`

func TestThreeLayersMerge(t *testing.T) {
	repo := fixture(t, globalMS, `
profile: acme
stack: go
tracker: { project: API }
vcs: { repo: acme/acme-api }
check:
  steps:
    - { name: test, run: "go test ./..." }
`)
	c, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"tracker.adapter":   c.Tracker.Adapter,
		"tracker.url":       c.Tracker.URL,
		"tracker.workspace": c.Tracker.Workspace,
		"tracker.project":   c.Tracker.Project,
		"vcs.host":          c.VCS.Host,
		"vcs.base_branch":   c.VCS.BaseBranch,
		"vcs.repo":          c.VCS.Repo,
		"vcs.remote":        c.VCS.Remote, // default que sobrevive al merge
		"agent":             strings.Join(c.Agent, ","),
	}
	expect := map[string]string{
		"tracker.adapter": "plane", "tracker.url": "https://plane.example.com", "tracker.workspace": "acme-dev",
		"tracker.project": "API", "vcs.host": "github", "vcs.base_branch": "dev",
		"vcs.repo": "acme/acme-api", "vcs.remote": "origin", "agent": "claude",
	}
	for k, v := range expect {
		if want[k] != v {
			t.Errorf("%s = %q, want %q", k, want[k], v)
		}
	}
	// Las listas se reemplazan, no se concatenan: el repo manda.
	if len(c.Check.Steps) != 1 || c.Check.Steps[0].Name != "test" {
		t.Errorf("check.steps: %+v", c.Check.Steps)
	}
	if c.Sources.Profile != "acme" || c.Sources.Repo == "" || c.Sources.Global == "" {
		t.Errorf("fuentes: %+v", c.Sources)
	}
}

func TestRepoOverridesProfileScalar(t *testing.T) {
	c, err := Load(fixture(t, globalMS, "profile: acme\nvcs: { base_branch: main }\ntracker: { project: X }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.VCS.BaseBranch != "main" || c.VCS.Host != "github" {
		t.Errorf("vcs: %+v", c.VCS)
	}
}

func TestProfileEnvOverride(t *testing.T) {
	repo := fixture(t, globalMS, "tracker: { project: API }\n")
	t.Setenv("BFLOW_PROFILE", "otro")
	c, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if c.VCS.BaseBranch != "main" || c.Sources.Profile != "otro" {
		t.Errorf("BFLOW_PROFILE no se aplicó: %+v %+v", c.VCS, c.Sources)
	}
}

func TestErrors(t *testing.T) {
	cases := []struct {
		name, global, repo string
		want               []string // fragmentos que el mensaje debe contener
	}{
		{"perfil inexistente", globalMS, "profile: nope\n", []string{`perfil "nope"`, "acme", "otro"}},
		{"campo desconocido", "", "trackr: { adapter: local }\n", []string{"bflow.yaml", "línea 1: campo desconocido «trackr»"}},
		{"campo desconocido global", "profiles:\n  x:\n    vsc: {}\n", "", []string{"config.yaml", "vsc"}},
		{"plane sin url", "", "tracker: { adapter: plane, workspace: w, project: P }\n", []string{"tracker.url"}},
		{"plane sin proyecto", "", "tracker: { adapter: plane, url: https://x, workspace: w }\n", []string{"tracker.project"}},
		{"proyecto como uuid", "", "tracker: { adapter: plane, url: https://x, workspace: w, project: 00000000-0000-4000-8000-000000000001 }\n",
			[]string{"identificador legible", "API"}},
		{"secreto en yaml", "", "tracker: { adapter: plane, url: https://x, workspace: w, project: P, api_key: abc }\n",
			[]string{"api_key", "llavero", "bflow connect"}},
		{"secreto en perfil", "profiles:\n  p:\n    vcs: { token: abc }\n", "profile: p\n", []string{"token", "llavero"}},
		{"adaptador desconocido", "", "tracker: { adapter: trello }\n", []string{"tracker.adapter", "trello", "local", "plane"}},
		{"carril inválido", "", "flow:\n  lanes:\n    hotfix: [implementing, documenting, walkthrough, in_review, done]\n", []string{"hotfix", "quality"}},
		{"fase desconocida", "", "flow:\n  lanes:\n    light: [spec, coding, quality, walkthrough, in_review, done]\n", []string{"coding"}},
		{"paso sin run", "", "check:\n  steps:\n    - { name: vet }\n", []string{"check.steps[0].run"}},
		{"yaml roto", "", "tracker: [\n", []string{"bflow.yaml"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Load(fixture(t, c.global, c.repo))
			if err == nil {
				t.Fatal("esperaba error")
			}
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("el error no menciona %q:\n%v", w, err)
				}
			}
		})
	}
}

func TestSeveralValidationErrorsAtOnce(t *testing.T) {
	_, err := Load(fixture(t, "", "tracker: { adapter: plane }\ncheck:\n  steps:\n    - { run: x }\nflow:\n  lanes:\n    hotfix: [implementing, documenting, walkthrough, in_review, done]\n"))
	if err == nil {
		t.Fatal("esperaba error")
	}
	for _, w := range []string{"tracker.url", "tracker.workspace", "tracker.project", "check.steps[0].name", "quality"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("falta %q en:\n%v", w, err)
		}
	}
}

func TestFlowCoreFromConfig(t *testing.T) {
	c, err := Load(fixture(t, "", `
stack: angular
flow:
  max_quality_rounds: 3
  lanes:
    light: [spec, implementing, paused, quality, documenting, walkthrough, in_review, done]
  security_audit: false
`))
	if err != nil {
		t.Fatal(err)
	}
	core := c.Flow.Core()
	if core.MaxQualityRounds != 3 {
		t.Errorf("rondas %d", core.MaxQualityRounds)
	}
	if !slices.Contains(core.Lanes[flow.Light], flow.Paused) {
		t.Error("paused debería estar en light")
	}
	if !slices.Equal(core.Lanes[flow.Full], flow.DefaultLanes()[flow.Full]) {
		t.Error("full debe conservar su default si no se sobrescribe")
	}
	q := core.Agents[flow.Quality]
	if slices.Contains(q, "security-auditor") || !slices.Contains(q, "ux-auditor") || !slices.Contains(q, "reviewer") {
		t.Errorf("agentes de calidad para angular sin seguridad: %v", q)
	}
	if sp := core.Agents[flow.Spec]; !slices.Equal(sp, []string{"ui-designer", "spec-author"}) {
		t.Errorf("agentes de spec en stack con UI: %v", sp)
	}
	if !core.UI {
		t.Error("el núcleo debe saber que el stack tiene UI (gate de spec con blueprint)")
	}
}

func TestStackDefaults(t *testing.T) {
	c, err := Load(fixture(t, "", "stack: go\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(c.Check.CodePaths, "internal") || !slices.Contains(c.Guard.TestPatterns, "*_test.go") {
		t.Errorf("defaults del stack go: code=%v tests=%v", c.Check.CodePaths, c.Guard.TestPatterns)
	}
	c2, err := Load(fixture(t, "", "stack: go\ncheck: { code_paths: [src] }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c2.Check.CodePaths, []string{"src"}) {
		t.Errorf("el repo debe poder sobrescribir code_paths: %v", c2.Check.CodePaths)
	}
}

func TestGlobalDir(t *testing.T) {
	t.Setenv("BFLOW_CONFIG_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("AppData", `C:\Users\x\AppData\Roaming`)
	t.Setenv("HOME", "/home/x")
	d := GlobalDir()
	switch runtime.GOOS {
	case "windows":
		if d != filepath.Join(`C:\Users\x\AppData\Roaming`, "bflow") {
			t.Errorf("windows: %s", d)
		}
	default:
		if d != filepath.Join("/home/x", ".config", "bflow") {
			t.Errorf("unix: %s", d)
		}
	}
	t.Setenv("BFLOW_CONFIG_HOME", "/tmp/otro")
	if GlobalDir() != "/tmp/otro" {
		t.Errorf("BFLOW_CONFIG_HOME no se respeta: %s", GlobalDir())
	}
}

func TestFindRepoRootWalksUp(t *testing.T) {
	repo := fixture(t, "", "stack: go\n")
	sub := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	c, err := Load(sub)
	if err != nil {
		t.Fatal(err)
	}
	if c.Root != repo || c.Stack != "go" {
		t.Errorf("root=%s stack=%s", c.Root, c.Stack)
	}
}

func TestUIWatchIsPersonal(t *testing.T) {
	// Preferencia personal en la config global; el repo puede apagarla.
	c, err := Load(fixture(t, "ui: { watch: true }\n", "stack: go\n"))
	if err != nil || !c.UI.Watch {
		t.Fatalf("global: %v %+v", err, c.UI)
	}
	c, err = Load(fixture(t, "ui: { watch: true }\n", "ui: { watch: false }\n"))
	if err != nil || c.UI.Watch {
		t.Errorf("el repo la pisa: %v %+v", err, c.UI)
	}
	if c, err := Load(fixture(t, "", "ui: { watch: true }\n")); err != nil || !c.UI.Watch {
		t.Errorf("en bflow.yaml: %v", err)
	}
}

func TestSecurityAuditAddsSeparateAuditor(t *testing.T) {
	for _, c := range []struct {
		yaml string
		want []string
	}{
		{"stack: go\n", []string{"reviewer"}},
		{"stack: go\nflow: { security_audit: true }\n", []string{"reviewer", "security-auditor"}},
		{"stack: go\nflow: { security_audit: false }\n", []string{"reviewer"}},
		{"stack: angular\nflow: { security_audit: true }\n", []string{"reviewer", "ux-auditor", "security-auditor"}},
	} {
		cfg, err := Load(fixture(t, "", c.yaml))
		if err != nil {
			t.Fatal(err)
		}
		if q := cfg.Flow.Core().Agents[flow.Quality]; !slices.Equal(q, c.want) {
			t.Errorf("%q: %v, want %v", c.yaml, q, c.want)
		}
	}
}

func TestSecurityAuditorConfigAfterFusion(t *testing.T) {
	// Un repo que ajustó al security-auditor: el error dice cómo seguir.
	_, err := Load(fixture(t, "", "stack: go\nagents:\n  security-auditor:\n    extra: docs/sec.md\n"))
	if err == nil || !strings.Contains(err.Error(), "Pasa su read y extra a agents.reviewer, o pon flow.security_audit: true") {
		t.Errorf("error: %v", err)
	}
	if _, err := Load(fixture(t, "", "stack: go\nflow: { security_audit: true }\nagents:\n  security-auditor:\n    extra: docs/sec.md\n")); err != nil {
		t.Errorf("con security_audit: true sus ajustes valen: %v", err)
	}
}

func TestConsumerChangelogIsVersioned(t *testing.T) {
	for _, p := range []string{".bflow/x.md", "../fuera.md"} {
		if _, err := Load(fixture(t, "", "stack: go\nflow: { consumer_changelog: \""+p+"\" }\n")); err == nil || !strings.Contains(err.Error(), "consumer_changelog") {
			t.Errorf("%s debe rechazarse: %v", p, err)
		}
	}
	c, err := Load(fixture(t, "", "stack: go\nflow: { consumer_changelog: \"docs/{id}.md\" }\n"))
	if err != nil || c.Flow.Core().ChangelogPath("A-1", "x") != "docs/A-1.md" {
		t.Errorf("ruta del repo: %v", err)
	}
}

func TestValidateGithubTracker(t *testing.T) {
	gh := "vcs: { host: github, repo: acme/app }\n"
	ok := []string{
		gh + "tracker: { adapter: github }\n",
		gh + "tracker: { adapter: github, prefix: GH, project: acme/7 }\n",
		gh + "tracker: { adapter: github, prefix: Api2, project: Acme-Corp/120, start_field: Inicio }\n",
	}
	for _, y := range ok {
		c, err := Load(fixture(t, "", y))
		if err != nil {
			t.Errorf("debe aceptar %q: %v", y, err)
			continue
		}
		if c.Tracker.Adapter != "github" {
			t.Errorf("adapter = %q", c.Tracker.Adapter)
		}
	}
	c, _ := Load(fixture(t, "", ok[2]))
	if c.Tracker.Prefix != "Api2" || c.Tracker.StartField != "Inicio" {
		t.Errorf("prefix/start_field no se leen: %+v", c.Tracker)
	}

	bad := []struct{ name, yaml, want string }{
		{"sin host github", "tracker: { adapter: github }\n", "vcs.host"},
		{"host vacío explícito", "vcs: { host: \"\" }\ntracker: { adapter: github }\n", "vcs.host"},
		{"project sin número", gh + "tracker: { adapter: github, project: acme }\n", "tracker.project"},
		{"project con cero", gh + "tracker: { adapter: github, project: acme/0 }\n", "tracker.project"},
		{"project con cero a la izquierda", gh + "tracker: { adapter: github, project: acme/07 }\n", "tracker.project"},
		{"project con ruta extra", gh + "tracker: { adapter: github, project: acme/7/x }\n", "tracker.project"},
		{"project con owner raro", gh + "tracker: { adapter: github, project: -acme/7 }\n", "tracker.project"},
		{"prefix con guion", gh + "tracker: { adapter: github, prefix: G-H }\n", "tracker.prefix"},
		{"prefix con número al inicio", gh + "tracker: { adapter: github, prefix: 1GH }\n", "tracker.prefix"},
	}
	for _, b := range bad {
		t.Run(b.name, func(t *testing.T) {
			_, err := Load(fixture(t, "", b.yaml))
			if err == nil || !strings.Contains(err.Error(), b.want) {
				t.Errorf("esperaba error con %q, got %v", b.want, err)
			}
		})
	}
	// Las reglas son solo de github: plane sigue aceptando su identificador.
	if _, err := Load(fixture(t, "", "tracker: { adapter: plane, url: https://x, workspace: w, project: API, prefix: a-b }\n")); err != nil {
		t.Errorf("prefix solo se valida con github: %v", err)
	}
}

func TestToolsYAML(t *testing.T) {
	type doc struct {
		Agent Tools `yaml:"agent,omitempty"`
	}
	ok := []struct {
		in   string
		want []string
	}{
		{"agent: claude\n", []string{"claude"}},
		{"agent: opencode\n", []string{"opencode"}},
		{"agent: [claude, opencode]\n", []string{"claude", "opencode"}},
		{"agent:\n  - opencode\n  - claude\n", []string{"opencode", "claude"}},
		{"agent: \"\"\n", nil},
		{"agent: []\n", nil},
		{"{}\n", nil},
	}
	for _, c := range ok {
		var d doc
		if err := yaml.Unmarshal([]byte(c.in), &d); err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if !slices.Equal(d.Agent, Tools(c.want)) {
			t.Errorf("%q = %v, quiero %v", c.in, d.Agent, c.want)
		}
	}
	for _, in := range []string{"agent: { a: b }\n", "agent: [[claude]]\n", "agent: [{ a: b }]\n"} {
		var d doc
		if err := yaml.Unmarshal([]byte(in), &d); err == nil {
			t.Errorf("%q debería fallar: %v", in, d.Agent)
		}
	}

	if got := Tools(nil).Rendered(); !slices.Equal(got, []string{"claude"}) {
		t.Errorf("sin agent: solo claude (R9): %v", got)
	}
	if got := (Tools{"opencode"}).Rendered(); !slices.Equal(got, []string{"opencode"}) {
		t.Errorf("Rendered respeta la lista: %v", got)
	}
	if !(Tools{"claude", "opencode"}).Has("opencode") || (Tools{"claude"}).Has("opencode") || Tools(nil).Has("claude") {
		t.Error("Has")
	}
}

func TestAgentListValidate(t *testing.T) {
	for _, repo := range []string{"agent: claude\n", "agent: [claude, opencode]\n", "agent: [opencode]\n", "agent: \"\"\n", "stack: go\n"} {
		if _, err := Load(fixture(t, "", repo)); err != nil {
			t.Errorf("%q debe ser válido: %v", repo, err)
		}
	}
	c, err := Load(fixture(t, "profiles:\n  dos:\n    agent: [claude, opencode]\n", "profile: dos\n"))
	if err != nil || !slices.Equal(c.Agent, Tools{"claude", "opencode"}) {
		t.Errorf("el perfil trae la lista: %v %v", c, err)
	}
	c, err = Load(fixture(t, "profiles:\n  dos:\n    agent: [claude, opencode]\n", "profile: dos\nagent: opencode\n"))
	if err != nil || !slices.Equal(c.Agent, Tools{"opencode"}) {
		t.Errorf("el repo reemplaza la lista del perfil: %v %v", c, err)
	}

	bad := []struct{ repo, want string }{
		{"agent: vim\n", `agent "vim" no existe (disponibles: claude, opencode)`},
		{"agent: [claude, vim]\n", `"vim"`},
		{"agent: [claude, claude]\n", `agent repite "claude"`},
	}
	for _, b := range bad {
		_, err := Load(fixture(t, "", b.repo))
		if err == nil || !strings.Contains(err.Error(), b.want) {
			t.Errorf("%q: quiero un error con %q: %v", b.repo, b.want, err)
		}
	}
}

// R1: lo versionado (Models) sale solo de bflow.yaml; lo personal va aparte.
func TestModelsRepoOnly(t *testing.T) {
	const global = "models:\n  opencode: { sonnet: g/sonnet, haiku: g/haiku, opus: g/opus }\n"
	c, err := Load(fixture(t, global, "models:\n  opencode: { haiku: r/haiku }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if m := c.Models["opencode"]; len(m) != 1 || m["haiku"] != "r/haiku" {
		t.Errorf("Models solo trae lo de bflow.yaml, sin fusionar la global: %v", c.Models)
	}
	if m := c.PersonalModels["opencode"]; len(m) != 3 || m["sonnet"] != "g/sonnet" || m["haiku"] != "g/haiku" || m["opus"] != "g/opus" {
		t.Errorf("PersonalModels trae la tabla global completa: %v", c.PersonalModels)
	}
	c, err = Load(fixture(t, global, ""))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Models["opencode"]) != 0 || c.PersonalModels["opencode"]["sonnet"] != "g/sonnet" {
		t.Errorf("sin models en el repo, Models queda vacío: %v / %v", c.Models, c.PersonalModels)
	}
	c, err = Load(fixture(t, "", "models:\n  opencode: { sonnet: r/sonnet }\n"))
	if err != nil || c.Models["opencode"]["sonnet"] != "r/sonnet" || len(c.PersonalModels) != 0 || c.ProfileModelsIgnored {
		t.Errorf("solo repo: %v %v %v", c.Models, c.PersonalModels, err)
	}
}

// R2: los models de un perfil se ignoran y se marca para avisar.
func TestModelsProfileIgnored(t *testing.T) {
	const global = "models:\n  opencode: { sonnet: g/sonnet }\nprofiles:\n  p:\n    models:\n      opencode: { sonnet: p/sonnet, haiku: p/haiku }\n"
	c, err := Load(fixture(t, global, "profile: p\n"))
	if err != nil {
		t.Fatalf("un perfil con models no falla la validación: %v", err)
	}
	if len(c.Models) != 0 || c.PersonalModels["opencode"]["sonnet"] != "g/sonnet" || len(c.PersonalModels["opencode"]) != 1 {
		t.Errorf("el perfil no aporta a Models ni a PersonalModels: %v / %v", c.Models, c.PersonalModels)
	}
	if !c.ProfileModelsIgnored {
		t.Error("ProfileModelsIgnored debe ser true")
	}
	c, err = Load(fixture(t, "profiles:\n  p:\n    agent: opencode\n", "profile: p\n"))
	if err != nil || c.ProfileModelsIgnored {
		t.Errorf("un perfil sin models no avisa: %v %v", c.ProfileModelsIgnored, err)
	}
}

func TestModelsValidate(t *testing.T) {
	cases := []struct {
		name, global, repo string
		want               []string
	}{
		{"tabla desconocida", "", "models:\n  claude: { sonnet: x/y }\n", []string{"models.claude", "opencode", "bflow.yaml"}},
		{"alias vacío", "", "models:\n  opencode: { sonnet: \"\" }\n", []string{"models.opencode.sonnet está vacío"}},
		{"valor que no es texto", "", "models:\n  opencode: { sonnet: [a, b] }\n", []string{"bflow.yaml"}},
		{"valor que no es texto en la global", "models:\n  opencode: { sonnet: [a] }\n", "", []string{"config.yaml"}},
		{"tabla desconocida en la global", "models:\n  codex: { sonnet: x/y }\n", "", []string{"models.codex"}},
		{"alias vacío en la global", "models:\n  opencode: { sonnet: \"\" }\n", "", []string{"models.opencode.sonnet está vacío", "config.yaml"}},
		{"tabla desconocida en la global nombra su origen", "models:\n  claude: { sonnet: x/y }\n", "", []string{"models.claude", "config.yaml"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Load(fixture(t, c.global, c.repo))
			if err == nil {
				t.Fatal("esperaba error")
			}
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("el error no menciona %q:\n%v", w, err)
				}
			}
		})
	}
}

func TestPublicPathsValidated(t *testing.T) {
	for _, bad := range []string{"/abs/x", "../x", "a/../../x", ".bflow/tasks/", "!/abs"} {
		if _, err := Load(fixture(t, "", "flow:\n  public_paths: ['"+bad+"']\n")); err == nil || !strings.Contains(err.Error(), "flow.public_paths") {
			t.Errorf("%q debía rechazarse: %v", bad, err)
		}
	}
	c, err := Load(fixture(t, "", "flow:\n  public_paths: [internal/cli/, '!*_test.go']\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.Flow.PublicPaths, []string{"internal/cli/", "!*_test.go"}) || !slices.Equal(c.Flow.Core().PublicPaths, c.Flow.PublicPaths) {
		t.Errorf("no llegó a flow.Config: %v", c.Flow.Core().PublicPaths)
	}
}

// R1: flow.budget acepta enteros y cifras con sufijo k/K/M (decimal incluido).
func TestTokenCountParse(t *testing.T) {
	c, err := Load(fixture(t, "", "flow:\n  budget: { implementing: 200000, contract: 200k, quality: 1.5M }\n"))
	if err != nil {
		t.Fatal(err)
	}
	core := c.Flow.Core()
	want := map[flow.Phase]int64{flow.Implementing: 200000, flow.Contract: 200_000, flow.Quality: 1_500_000}
	for p, n := range want {
		if core.Budget[p] != n {
			t.Errorf("budget[%s] = %d, want %d (core=%v)", p, core.Budget[p], n, core.Budget)
		}
	}
}

// R2: una fase fuera de flow.BudgetPhases, o una cifra ilegible/≤0, falla al cargar.
func TestBudgetUnknownPhase(t *testing.T) {
	if _, err := Load(fixture(t, "", "flow:\n  budget: { coding: 200k }\n")); err == nil || !strings.Contains(err.Error(), "flow.budget") || !strings.Contains(err.Error(), "coding") {
		t.Errorf("fase desconocida debía rechazarse: %v", err)
	}
	if _, err := Load(fixture(t, "", "flow:\n  budget: { implementing: 0 }\n")); err == nil || !strings.Contains(err.Error(), "flow.budget") {
		t.Errorf("cifra ≤ 0 debía rechazarse: %v", err)
	}
	if _, err := Load(fixture(t, "", "flow:\n  budget: { implementing: abc }\n")); err == nil || !strings.Contains(err.Error(), "flow.budget") {
		t.Errorf("cifra ilegible debía rechazarse: %v", err)
	}
}

// R16: loop_repeats solo admite 0 (apagado) o ≥ 2.
func TestLoopRepeatsInvalid(t *testing.T) {
	for _, bad := range []string{"-1", "1"} {
		if _, err := Load(fixture(t, "", "flow:\n  loop_repeats: "+bad+"\n")); err == nil || !strings.Contains(err.Error(), "flow.loop_repeats") {
			t.Errorf("loop_repeats %s debía rechazarse: %v", bad, err)
		}
	}
	c, err := Load(fixture(t, "", "flow:\n  loop_repeats: 3\n"))
	if err != nil || c.Flow.LoopRepeats != 3 {
		t.Errorf("loop_repeats 3 debía aceptarse: c=%+v err=%v", c.Flow.LoopRepeats, err)
	}
}

// R3: sin flow.budget, el núcleo no trae presupuesto (ni pregunta ni línea en watch).
func TestBudgetEmptyByDefault(t *testing.T) {
	c, err := Load(fixture(t, "", "stack: go\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Flow.Core().Budget) != 0 {
		t.Errorf("sin flow.budget, core.Budget debía quedar vacío: %v", c.Flow.Core().Budget)
	}
}

func TestValidateCommentPatterns(t *testing.T) {
	_, err := Load(fixture(t, "", "flow:\n  comment_patterns: ['(']\n"))
	if err == nil || !strings.Contains(err.Error(), "flow.comment_patterns") {
		t.Errorf("un patrón que no compila debía rechazarse: %v", err)
	}
	if _, err := Load(fixture(t, "", "flow:\n  comment_patterns: ['TODO[0-9]+']\n")); err != nil {
		t.Errorf("un patrón válido no debe fallar: %v", err)
	}
}

func TestFlowCommentDefaults(t *testing.T) {
	c, err := Load(fixture(t, "", ""))
	if err != nil {
		t.Fatal(err)
	}
	core := c.Flow.Core()
	if !core.NarrativeComments || len(core.CommentPatterns) != len(review.DefaultCommentPatterns) {
		t.Errorf("por defecto la regla está activa con los patrones fijos: %v %v", core.NarrativeComments, core.CommentPatterns)
	}
	c, err = Load(fixture(t, "", "flow:\n  narrative_comments: false\n  comment_patterns: [FOO]\n"))
	if err != nil {
		t.Fatal(err)
	}
	core = c.Flow.Core()
	if core.NarrativeComments || !slices.Equal(core.CommentPatterns, []string{"FOO"}) {
		t.Errorf("config explícita: %v %v", core.NarrativeComments, core.CommentPatterns)
	}
}

func TestCheckTestStepDefault(t *testing.T) {
	c, err := Load(fixture(t, "", "check: { steps: [{ name: test, run: go test ./... }] }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Check.TestStep != "test" {
		t.Errorf("test_step por defecto: %q", c.Check.TestStep)
	}
	c, err = Load(fixture(t, "", "check: { test_step: unit, steps: [{ name: unit, run: go test ./... }] }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Check.TestStep != "unit" {
		t.Errorf("el repo sobrescribe test_step: %q", c.Check.TestStep)
	}
}

func TestValidateUILang(t *testing.T) {
	_, err := Load(fixture(t, "", "ui:\n  lang: fr\n"))
	if err == nil || !strings.Contains(err.Error(), "ui.lang \"fr\" no existe") {
		t.Fatalf("expected error for unsupported ui.lang, got %v", err)
	}
	c, err := Load(fixture(t, "", "ui:\n  lang: en\n"))
	if err != nil {
		t.Fatalf("valid ui.lang 'en' failed: %v", err)
	}
	if c.UI.Lang != "en" {
		t.Errorf("expected Lang to be en, got %q", c.UI.Lang)
	}
}
