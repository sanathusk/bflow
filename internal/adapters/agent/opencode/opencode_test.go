package opencode

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	files "innobytes.tech/bflow/adapters/opencode"
	"innobytes.tech/bflow/internal/agents"
	"innobytes.tech/bflow/internal/flow"
)

// frontmatterOf separa el frontmatter YAML del resto del archivo.
func frontmatterOf(t *testing.T, doc []byte) (map[string]any, string) {
	t.Helper()
	rest, ok := strings.CutPrefix(string(doc), "---\n")
	if !ok {
		t.Fatalf("sin frontmatter:\n%s", doc)
	}
	fm, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		t.Fatalf("frontmatter sin cierre:\n%s", doc)
	}
	var m map[string]any
	if err := yaml.Unmarshal([]byte(fm), &m); err != nil {
		t.Fatalf("frontmatter inválido: %v\n%s", err, fm)
	}
	return m, body
}

func TestOpenCodeRender(t *testing.T) {
	specs := []agents.Spec{
		{Name: "implementer", Subagent: "bflow-implementer", Description: "Escribe: contrato # y código. Lo lanza la sesión.",
			Tools: []string{agents.Read, agents.Write, agents.Bash}, Model: "anthropic/claude-sonnet-4", Body: "## Contrato con bflow\n\ncuerpo\n\n"},
		{Name: "reviewer", Subagent: "bflow-reviewer", Description: "Revisa.", Tools: []string{agents.Read}, Body: "## Contrato con bflow\n\nrevisa\n"},
		{Name: "otro", Subagent: "bflow-otro", Description: "Inyección.", Tools: []string{agents.Read, agents.Bash},
			Model: "a/b\nmode: primary\npermission: {edit: allow}", Body: "x\n"},
	}
	out, err := Agent{}.RenderAgents(specs)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 6 { // tres agentes y los tres archivos del plugin
		t.Fatalf("un archivo por agente y el plugin: %d", len(out))
	}

	imp, ok := out[".opencode/agents/bflow-implementer.md"]
	if !ok {
		t.Fatalf("ruta .opencode/agents/bflow-<agente>.md: %v", keys(out))
	}
	fm, body := frontmatterOf(t, imp)
	perm, _ := fm["permission"].(map[string]any)
	if fm["description"] != specs[0].Description || fm["mode"] != "subagent" || fm["model"] != "anthropic/claude-sonnet-4" ||
		perm["edit"] != "allow" || perm["bash"] != "allow" {
		t.Errorf("frontmatter del implementer: %v", fm)
	}
	for k := range fm {
		if !slices.Contains([]string{"description", "mode", "model", "permission"}, k) {
			t.Errorf("clave inesperada en el frontmatter: %s", k)
		}
	}
	if want := agents.GeneratedMark + "\n\n## Contrato con bflow\n\ncuerpo\n"; body != want {
		t.Errorf("tras el frontmatter van la marca y el cuerpo del agente:\n%q", body)
	}

	rv, _ := frontmatterOf(t, out[".opencode/agents/bflow-reviewer.md"])
	perm, _ = rv["permission"].(map[string]any)
	if _, has := rv["model"]; has || perm["edit"] != "deny" || perm["bash"] != "deny" || rv["mode"] != "subagent" {
		t.Errorf("sin modelo se omite model; sin write ni bash, deny: %v", rv)
	}

	ot, _ := frontmatterOf(t, out[".opencode/agents/bflow-otro.md"])
	perm, _ = ot["permission"].(map[string]any)
	if ot["mode"] != "subagent" || ot["model"] != specs[2].Model || perm["edit"] != "deny" || perm["bash"] != "allow" {
		t.Errorf("el modelo va como texto, sin inyectar claves: %v", ot)
	}
}

func keys(m map[string][]byte) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return ks
}

func TestOpenCodeGenerated(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".opencode", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("bflow-a.md", "---\ndescription: x\n---\n"+agents.GeneratedMark+"\n\ncuerpo\n")
	write("bflow-c.md", "---\n---\n"+agents.GeneratedMark+"\n")
	write("bflow-mio.md", "---\ndescription: lo escribí yo\n---\ncuerpo\n") // sin marca: nunca es de bflow
	write("otro.md", agents.GeneratedMark+"\n")                             // con marca pero sin el prefijo bflow-

	got := Agent{}.GeneratedAgents(root)
	slices.Sort(got)
	if want := []string{".opencode/agents/bflow-a.md", ".opencode/agents/bflow-c.md"}; !slices.Equal(got, want) {
		t.Errorf("solo los bflow-*.md con la marca: %v", got)
	}
	if got := (Agent{}).GeneratedAgents(t.TempDir()); len(got) != 0 {
		t.Errorf("sin carpeta no hay nada: %v", got)
	}
}

func TestOpenCodeInstallXDG(t *testing.T) {
	home, xdg := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	p, err := Agent{}.InstallSkill(home)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(xdg, "opencode", "commands", "bflow.md")
	if p != want {
		t.Errorf("con XDG_CONFIG_HOME el comando va ahí: %s", p)
	}
	if b, err := os.ReadFile(want); err != nil || len(b) == 0 || !bytes.Equal(b, files.Command) {
		t.Errorf("el comando embebido queda escrito: %v", err)
	}
	if _, err := (Agent{}).InstallSkill(home); err != nil {
		t.Errorf("reinstalar es idempotente: %v", err)
	}
	if ents, _ := os.ReadDir(filepath.Dir(want)); len(ents) != 1 {
		t.Errorf("sin temporales sobrantes: %v", ents)
	}

	t.Setenv("XDG_CONFIG_HOME", "")
	p, err = Agent{}.InstallSkill(home)
	if want := filepath.Join(home, ".config", "opencode", "commands", "bflow.md"); err != nil || p != want {
		t.Errorf("sin XDG_CONFIG_HOME: ~/.config/opencode/commands/bflow.md: %s %v", p, err)
	}
}

func TestOpenCodeInstallBadDir(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)

	t.Setenv("XDG_CONFIG_HOME", filepath.Join("rel", "dir"))
	if _, err := (Agent{}).InstallSkill(t.TempDir()); err == nil || !strings.Contains(err.Error(), "XDG_CONFIG_HOME") {
		t.Errorf("XDG_CONFIG_HOME relativa: error que la nombra: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cwd, "rel")); err == nil {
		t.Error("no escribe relativo al directorio actual")
	}

	t.Setenv("XDG_CONFIG_HOME", "")
	if _, err := (Agent{}).InstallSkill(""); err == nil {
		t.Error("sin XDG_CONFIG_HOME ni home no se resuelve la carpeta")
	}

	blocker := filepath.Join(t.TempDir(), "archivo")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", blocker)
	_, err := Agent{}.InstallSkill(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), blocker) {
		t.Errorf("no se puede escribir: el error trae la ruta: %v", err)
	}
}

func TestOpenCodeSkillStateCRLF(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	path, state := (Agent{}).SkillState(home)
	want := filepath.Join(home, ".config", "opencode", "commands", "bflow.md")
	if path != want || state != "missing" {
		t.Errorf("sin instalar: %s %s", path, state)
	}
	if _, err := (Agent{}).InstallSkill(home); err != nil {
		t.Fatal(err)
	}
	if _, state := (Agent{}).SkillState(home); state != "ok" {
		t.Errorf("recién instalado: %s", state)
	}
	crlf := bytes.ReplaceAll(files.Command, []byte("\n"), []byte("\r\n"))
	if err := os.WriteFile(want, crlf, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, state := (Agent{}).SkillState(home); state != "ok" {
		t.Errorf("igual tras normalizar CRLF: %s", state)
	}
	if err := os.WriteFile(want, []byte("vieja"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, state := (Agent{}).SkillState(home); state != "stale" {
		t.Errorf("distinto: %s", state)
	}
}

// allSpecs construye todos los agentes del catálogo y el scout.
func allSpecs(t *testing.T, root string) []agents.Spec {
	t.Helper()
	fc := flow.DefaultConfig()
	fc.Agents = map[flow.Phase][]string{
		flow.Spec:         {"spec-author", "ui-designer"},
		flow.Contract:     {"implementer"},
		flow.Implementing: {"implementer"},
		flow.Quality:      {"reviewer", "security-auditor", "ux-auditor"},
		flow.Documenting:  {"documenter"},
	}
	fc.Scout = "scout"
	specs, err := agents.Build(fc, nil, root)
	if err != nil {
		t.Fatal(err)
	}
	return specs
}

// variable: lo que cambia entre tareas o máquinas y rompe el prefijo de caché (GH-19).
func variable(root string) []*regexp.Regexp {
	return []*regexp.Regexp{
		regexp.MustCompile(`[A-Z][A-Z0-9]*-[0-9]+`),
		regexp.MustCompile(`[0-9]{4}-[0-9]{2}-[0-9]{2}`),
		regexp.MustCompile(`\.bflow/tasks/[^<\s]`),
		regexp.MustCompile(regexp.QuoteMeta(root)),
	}
}

func TestOpenCodeRenderedAgentsStablePrefix(t *testing.T) {
	root := t.TempDir()
	specs := allSpecs(t, root)
	first, err := Agent{}.RenderAgents(specs)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Agent{}.RenderAgents(specs)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for path, doc := range first {
		if !bytes.Equal(doc, second[path]) {
			t.Errorf("%s: dos renders seguidos difieren", path)
		}
		if !strings.HasPrefix(path, ".opencode/agents/") {
			continue // el plugin no lleva cuerpo de agente
		}
		n++
		for _, re := range variable(root) {
			if loc := re.FindIndex(doc); loc != nil {
				t.Errorf("%s: %q aparece en el archivo: %q", path, re, doc[loc[0]:loc[1]])
			}
		}
		_, body := frontmatterOf(t, doc)
		if !strings.HasPrefix(body, agents.GeneratedMark+"\n\n## Contract with bflow") && !strings.HasPrefix(body, agents.GeneratedMark+"\n\n## Contrato con bflow") {
			t.Errorf("%s: el cuerpo empieza con la marca y el contrato: %q", path, body[:min(len(body), 80)])
		}
	}
	if n != len(specs) {
		t.Errorf("un archivo por agente: %d de %d", n, len(specs))
	}
}
