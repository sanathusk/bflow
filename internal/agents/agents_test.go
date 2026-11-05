package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"innobytes.tech/bflow/internal/config"
	"innobytes.tech/bflow/internal/flow"
	"innobytes.tech/bflow/internal/i18n"
	"innobytes.tech/bflow/internal/testutil"
)

func byName(t *testing.T, specs []Spec) map[string]Spec {
	t.Helper()
	m := map[string]Spec{}
	for _, s := range specs {
		m[s.Name] = s
	}
	return m
}

func TestBuildDefaultFlow(t *testing.T) {
	specs, err := Build(flow.DefaultConfig(), nil, testutil.TempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	m := byName(t, specs)
	if len(m) != 4 || m["security-auditor"].Name != "" {
		t.Fatalf("agentes del flujo por defecto (el reviewer revisa también seguridad): %v", m)
	}
	imp := m["implementer"]
	if imp.Subagent != "bflow-implementer" || imp.Model != "sonnet" || imp.Effort != "medium" || imp.OmitClaudeMd {
		t.Errorf("implementer: %+v", imp)
	}
	for _, want := range []string{
		"`bflow report <id> --agent implementer --verdict CONTRACT_READY|NEEDS_DECISION|BLOCKED`",
		"`bflow report <id> --agent implementer --verdict DONE|NEEDS_DECISION|BLOCKED`",
		"`bflow check <id>`",
		"frozen",
		"`bflow freeze --allow <file>`",
		"`.bflow/tasks/<id>/contract.md`",
		"`.bflow/tasks/<id>/reports/impl.md`",
		"`bflow report`",
		"## Craft\n",
	} {
		if !strings.Contains(imp.Body, want) {
			t.Errorf("el contrato del implementer no dice %q:\n%s", want, imp.Body)
		}
	}
	rv := m["reviewer"].Body
	if !strings.Contains(rv, "**Security**") || strings.Contains(rv, "security.md") {
		t.Errorf("el reviewer revisa seguridad, pero security.md es del security-auditor, que corre en paralelo:\n%s", rv)
	}
	if !strings.Contains(imp.Body, "Do not write changelogs") {
		t.Errorf("el implementer no debe escribir changelogs:\n%s", imp.Body)
	}
	if doc := m["documenter"].Body; !strings.Contains(doc, "path received in `changelog`") || !strings.Contains(doc, "It is the only changelog") || strings.Contains(doc, "consumer-changelog.md") {
		t.Errorf("el documenter escribe el único changelog, versionado:\n%s", doc)
	}
	if !strings.Contains(rv, "--verdict APPROVED|REJECTED") || strings.Contains(rv, "NEEDS_DECISION") || strings.Contains(rv, "DONE requires") {
		t.Errorf("el contrato del reviewer sale de la fase quality:\n%s", rv)
	}
	if sa := m["spec-author"].Body; !strings.Contains(sa, "With SPLIT add") || !strings.Contains(sa, "your part of the spec") {
		t.Errorf("spec-author:\n%s", sa)
	}
	if m["documenter"].Model != "haiku" || m["documenter"].Effort != "low" {
		t.Errorf("documenter: %+v", m["documenter"])
	}
}

func TestBuildSpanishFlow(t *testing.T) {
	b, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	fc := flow.DefaultConfig()
	fc.Loc = b.New("es")
	specs, err := Build(fc, nil, testutil.TempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	m := byName(t, specs)
	imp := m["implementer"]
	for _, want := range []string{
		"en contract: `bflow report <id> --agent implementer --verdict CONTRACT_READY|NEEDS_DECISION|BLOCKED`",
		"en implementing: `bflow report <id> --agent implementer --verdict DONE|NEEDS_DECISION|BLOCKED`",
		"DONE exige `bflow check <id>`",
		"quedan congeladas",
		"comando literal `bflow freeze --allow <archivo>`",
		"`.bflow/tasks/<id>/contract.md` y `.bflow/tasks/<id>/reports/impl.md`",
		"Tu respuesta final es solo la salida de `bflow report`",
		"## Oficio\n",
	} {
		if !strings.Contains(imp.Body, want) {
			t.Errorf("el contrato en español del implementer no dice %q:\n%s", want, imp.Body)
		}
	}
}

func TestBuildRepoCraftAndCustomAgent(t *testing.T) {
	root := testutil.TempDir(t)
	if err := os.MkdirAll(filepath.Join(root, "docs", "bflow"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "bflow", "impl.md"), []byte("Commits de una línea.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "bflow", "perf.md"), []byte("Mide p95 de los endpoints tocados.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fc := flow.DefaultConfig()
	fc.Agents[flow.Quality] = append(fc.Agents[flow.Quality], "perf-auditor")
	no := false
	conf := map[string]config.AgentConf{
		"implementer":  {Model: "opus", Effort: "high", Read: []string{"docs/architecture/"}, Extra: "docs/bflow/impl.md"},
		"reviewer":     {Read: []string{"docs/architecture/"}, OmitClaudeMd: &no},
		"perf-auditor": {Extra: "docs/bflow/perf.md"},
	}
	specs, err := Build(fc, conf, root)
	if err != nil {
		t.Fatal(err)
	}
	m := byName(t, specs)
	imp := m["implementer"]
	if imp.Model != "opus" || imp.Effort != "high" || !imp.OmitClaudeMd {
		t.Errorf("ajustes del repo: %+v", imp)
	}
	if !strings.Contains(imp.Body, "Commits de una línea.") || (!strings.Contains(imp.Body, "## Repo craft") && !strings.Contains(imp.Body, "## Oficio del repo")) {
		t.Errorf("oficio del repo:\n%s", imp.Body)
	}
	if m["reviewer"].OmitClaudeMd {
		t.Error("omit_claude_md: false manda sobre el default de read")
	}
	perf := m["perf-auditor"]
	if !strings.Contains(perf.Body, "`.bflow/tasks/<id>/reports/perf-auditor.md`") || !strings.Contains(perf.Body, "Mide p95") ||
		strings.Contains(perf.Body, "## Oficio\n") || strings.Contains(perf.Body, "## Craft\n") {
		t.Errorf("agente propio: contrato de bflow + su oficio, sin oficio por defecto:\n%s", perf.Body)
	}

	delete(conf, "perf-auditor")
	if _, err := Build(fc, conf, root); err == nil || !strings.Contains(err.Error(), "agents.perf-auditor.extra") {
		t.Errorf("un agente propio sin oficio debe fallar: %v", err)
	}
}

func TestCatalogHasCraft(t *testing.T) {
	for name := range catalog {
		for _, lang := range []string{"en", "es"} {
			if b, err := craftFS.ReadFile(fmt.Sprintf("craft/%s/%s.md", lang, name)); err != nil || len(b) == 0 {
				t.Errorf("%s no tiene oficio en %s: %v", name, lang, err)
			}
		}
	}
}

func TestResolveModels(t *testing.T) {
	specs := []Spec{
		{Name: "implementer", Subagent: "bflow-implementer", Model: "sonnet"},
		{Name: "reviewer", Subagent: "bflow-reviewer", Model: "sonnet"},
		{Name: "documenter", Subagent: "bflow-documenter", Model: "haiku"},
		{Name: "spec-author", Subagent: "bflow-spec-author", Model: ""},
		{Name: "perf", Subagent: "bflow-perf", Model: "openai/gpt-5"},
		{Name: "otro", Subagent: "bflow-otro", Model: "opus"},
	}
	orig := append([]Spec(nil), specs...)
	out, un := ResolveModels(specs, map[string]string{"sonnet": "anthropic/claude-sonnet-4"})

	got := map[string]string{}
	for _, s := range out {
		got[s.Name] = s.Model
	}
	want := map[string]string{
		"implementer": "anthropic/claude-sonnet-4", // alias de la tabla
		"reviewer":    "anthropic/claude-sonnet-4",
		"documenter":  "",             // alias ausente: se omite
		"spec-author": "",             // vacío sigue vacío
		"perf":        "openai/gpt-5", // con "/" va tal cual
		"otro":        "",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: model %q, quiero %q", k, got[k], v)
		}
	}
	if len(out) != len(specs) {
		t.Fatalf("mismos agentes: %d", len(out))
	}
	for i := range specs {
		if !reflect.DeepEqual(specs[i], orig[i]) {
			t.Errorf("no muta la entrada: %+v", specs[i])
		}
	}

	if len(un) != 2 || un[0].Alias != "haiku" || un[1].Alias != "opus" {
		t.Fatalf("sin resolver, ordenado por alias: %+v", un)
	}
	if !slices.Equal(un[0].Agents, []string{"bflow-documenter"}) || !slices.Equal(un[1].Agents, []string{"bflow-otro"}) {
		t.Errorf("agentes afectados: %+v", un)
	}

	_, un = ResolveModels(specs, map[string]string{"sonnet": "a/b", "haiku": "a/c", "opus": "a/d"})
	if len(un) != 0 {
		t.Errorf("con toda la tabla no queda nada sin resolver: %+v", un)
	}
	_, un = ResolveModels(specs, nil)
	if len(un) != 3 || !slices.Equal(un[1].Agents, []string{"bflow-otro"}) || !slices.Equal(un[2].Agents, []string{"bflow-implementer", "bflow-reviewer"}) {
		t.Errorf("sin tabla, cada alias con sus agentes ordenados: %+v", un)
	}
}

// R16: el documenter escribe reports/docs.md además del walkthrough.
func TestDocumenterWritesDocsReport(t *testing.T) {
	specs, err := Build(flow.DefaultConfig(), nil, testutil.TempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	doc := byName(t, specs)["documenter"].Body
	wantEn := "- You write `.bflow/tasks/<id>/walkthrough.md` and `.bflow/tasks/<id>/reports/docs.md`. In `.bflow/` do not touch anything else."
	wantEs := "- Escribes `.bflow/tasks/<id>/walkthrough.md` y `.bflow/tasks/<id>/reports/docs.md`. En `.bflow/` no tocas nada más."
	if !strings.Contains(doc, wantEn) && !strings.Contains(doc, wantEs) {
		t.Errorf("el contrato del documenter no lista reports/docs.md:\n%s", doc)
	}
}

// GH-55: el oficio del documenter exige changelog si cambia salida pública.
func TestDocumenterChangelogPublicOutput(t *testing.T) {
	specs, err := Build(flow.DefaultConfig(), nil, testutil.TempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	doc := byName(t, specs)["documenter"].Body
	for _, want := range []string{"`flow.public_paths`", "BLOCKED"} {
		if !strings.Contains(doc, want) {
			t.Errorf("falta %q en el oficio del documenter", want)
		}
	}
	if !strings.Contains(doc, "public output (commands, JSON output, panel, config)") && !strings.Contains(doc, "salida pública (comandos, salida JSON, panel, config)") {
		t.Error("falta salida pública en el oficio del documenter")
	}
	if !strings.Contains(doc, "report BLOCKED with reason") && !strings.Contains(doc, "reporta BLOCKED con el motivo") {
		t.Error("falta reportar BLOCKED en el oficio del documenter")
	}
	if !strings.Contains(doc, `never write "no change" for changelog`) && !strings.Contains(doc, `nunca escribas "sin cambio" para el changelog`) {
		t.Error("falta nunca escribir sin cambio en el oficio del documenter")
	}
}

// variableInPrefix: lo que cambia entre tareas o entre máquinas y rompería el
// prefijo de caché si entrara en el cuerpo de un agente (GH-19).
var variableInPrefix = []*regexp.Regexp{
	regexp.MustCompile(`[A-Z][A-Z0-9]*-[0-9]+`),
	regexp.MustCompile(`[0-9]{4}-[0-9]{2}-[0-9]{2}`),
	regexp.MustCompile(`\.bflow/tasks/[^<\s]`),
}

// allCatalogConfig arma un flujo con todos los agentes del catálogo y el scout.
func allCatalogConfig() flow.Config {
	fc := flow.DefaultConfig()
	fc.Agents = map[flow.Phase][]string{
		flow.Spec:         {"spec-author", "ui-designer"},
		flow.Contract:     {"implementer"},
		flow.Implementing: {"implementer"},
		flow.Quality:      {"reviewer", "security-auditor", "ux-auditor"},
		flow.Documenting:  {"documenter"},
	}
	fc.Scout = "scout"
	return fc
}

func TestAgentBodiesHaveNoTaskData(t *testing.T) {
	root := testutil.TempDir(t)
	specs, err := Build(allCatalogConfig(), nil, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 8 {
		t.Fatalf("todos los agentes del catálogo y el scout: %d", len(specs))
	}
	for _, s := range specs {
		pats := append([]*regexp.Regexp{regexp.MustCompile(regexp.QuoteMeta(root)), regexp.MustCompile(regexp.QuoteMeta(filepath.ToSlash(root)))}, variableInPrefix...)
		for _, re := range pats {
			if loc := re.FindStringIndex(s.Body); loc != nil {
				line := s.Body[strings.LastIndex(s.Body[:loc[0]], "\n")+1:]
				line, _, _ = strings.Cut(line, "\n")
				t.Errorf("%s: %q aparece en el cuerpo, rompe el prefijo de caché: %s", s.Name, re, line)
			}
		}
	}
}

func TestAgentSpecsSameAcrossRoots(t *testing.T) {
	a, err := Build(allCatalogConfig(), nil, testutil.TempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build(allCatalogConfig(), nil, testutil.TempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("los Spec no dependen de la raíz del repo")
	}
}

func TestReviewerCraftCorreccion(t *testing.T) {
	specs, err := Build(flow.DefaultConfig(), nil, testutil.TempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	var rv string
	for _, s := range specs {
		if s.Name == "reviewer" {
			rv = s.Body
		}
	}
	if !strings.Contains(rv, "paths:") && !strings.Contains(rv, "caminos:") {
		t.Error("el reviewer no dice paths/caminos")
	}
	if !strings.Contains(rv, "## Unexecuted tests") && !strings.Contains(rv, "## Pruebas sin ejecutar") {
		t.Error("el reviewer no incluye la sección de pruebas sin ejecutar")
	}
	if !strings.Contains(rv, "untested") && !strings.Contains(rv, "sin prueba") {
		t.Error("el reviewer no dice untested/sin prueba")
	}
}

func TestCatalogDefaultModels(t *testing.T) {
	fc := flow.DefaultConfig()
	fc.Scout = flow.ScoutAgent
	got := Models(fc, nil)
	want := map[string]string{"spec-author": "opus", "reviewer": "opus", "implementer": "sonnet", "documenter": "haiku", "scout": "haiku"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Models por defecto = %v, quiero %v", got, want)
	}
	specs, err := Build(fc, nil, testutil.TempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range specs {
		if s.Model != want[s.Name] {
			t.Errorf("Build %s: model %q, Models dice %q", s.Name, s.Model, want[s.Name])
		}
	}
}

func TestModelsRespectsRepoOverride(t *testing.T) {
	fc := flow.DefaultConfig()
	fc.Scout = flow.ScoutAgent
	got := Models(fc, map[string]config.AgentConf{
		"spec-author": {Model: "sonnet"},
		"scout":       {Model: "opus"},
	})
	if got["spec-author"] != "sonnet" || got["scout"] != "opus" || got["reviewer"] != "opus" || got["implementer"] != "sonnet" {
		t.Errorf("agents.<nombre>.model manda sobre el catálogo: %v", got)
	}
	fc.Scout = ""
	if _, ok := Models(fc, nil)["scout"]; ok {
		t.Error("sin scout en el flujo no aparece")
	}
}

// El agente del eval usa modelo y esfuerzo de agents.reviewer y su
// contrato; el oficio no va embebido porque lo lee del craft del ref.
func TestEvalReviewerSpec(t *testing.T) {
	s := EvalReviewerSpec(config.AgentConf{Model: "sonnet", Effort: "high"})
	if s.Name != "eval-reviewer" || s.Subagent != "bflow-eval-reviewer" {
		t.Errorf("nombres: %q %q", s.Name, s.Subagent)
	}
	if s.Model != "sonnet" || s.Effort != "high" {
		t.Errorf("modelo y esfuerzo del reviewer: %q %q", s.Model, s.Effort)
	}
	if !slices.Equal(s.Tools, []string{Read, Write, Bash}) {
		t.Errorf("herramientas: %v", s.Tools)
	}
	if (!strings.Contains(s.Description, "eval case") && !strings.Contains(s.Description, "eval del reviewer")) || (!strings.Contains(s.Description, "do not invoke on your own") && !strings.Contains(s.Description, "no lo invoques por tu cuenta")) {
		t.Errorf("descripción: %q", s.Description)
	}
	for _, want := range []string{
		"`id`", "`base`", "`worktree`", "`out`", "`craft`", "`spec`",
		"git -C <worktree> diff <base>...HEAD",
		"`bflow eval reviewer report <id> --verdict APPROVED|REJECTED`",
	} {
		if !strings.Contains(s.Body, want) {
			t.Errorf("el contrato del eval no dice %q", want)
		}
	}
	if !strings.Contains(s.Body, "do not run `bflow check`") && !strings.Contains(s.Body, "no corras `bflow check`") {
		t.Errorf("el contrato del eval no indica no correr bflow check:\n%s", s.Body)
	}
	if !strings.Contains(s.Body, "`<out>/<name>.md`") && !strings.Contains(s.Body, "`<out>/<nombre>.md`") {
		t.Errorf("el contrato del eval no indica `<out>/<name>.md`:\n%s", s.Body)
	}
	if !strings.Contains(s.Body, "Your final response is only that output") && !strings.Contains(s.Body, "Tu respuesta final es solo esa salida") {
		t.Errorf("el contrato del eval no indica la respuesta final:\n%s", s.Body)
	}
	if strings.Contains(s.Body, "## Oficio") || strings.Contains(s.Body, "## Craft") {
		t.Error("sin oficio embebido")
	}

	def := EvalReviewerSpec(config.AgentConf{})
	if def.Model != "opus" || def.Effort != "medium" {
		t.Errorf("sin ajustes toma el catálogo del reviewer: %q %q", def.Model, def.Effort)
	}
}
