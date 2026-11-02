// Package setup detecta cómo es un repo (stack, remoto, rama base, pasos de
// check) y arma un bflow.yaml corto para bflow init.
package setup

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"innobytes.tech/bflow/internal/vcs"
)

// StepProposal es un paso de check propuesto.
type StepProposal struct {
	Name     string `yaml:"name"`
	Run      string `yaml:"run"`
	Needs    string `yaml:"needs,omitempty"`
	Optional bool   `yaml:"optional,omitempty"`
}

// Tools dice qué herramientas hay instaladas.
type Tools struct{ Has func(name string) bool }

// Detection es lo que init deduce del repo.
type Detection struct {
	Stack string
	Steps []StepProposal
	Quick []string
	DBEnv []string // variables de conexión a la base de datos, de los .env de ejemplo
}

func exists(dir, f string) bool {
	_, err := os.Stat(filepath.Join(dir, f))
	return err == nil
}

// Detect mira el repo en dir.
func Detect(dir string, tools Tools) Detection {
	var d Detection
	switch {
	case exists(dir, "angular.json"):
		d.Stack = "angular"
	case exists(dir, "go.mod"):
		d.Stack = "go"
	case exists(dir, "package.json"):
		d.Stack = "node"
	}
	switch {
	case d.Stack == "go":
		d.Steps, d.Quick = goSteps(tools), []string{"go vet {pkg}", "go test -count=1 {pkg}"}
	case exists(dir, "package.json"):
		d.Steps = npmSteps(dir, d.Stack)
	case exists(dir, "Makefile"):
		d.Steps = makeSteps(dir)
	}
	d.DBEnv = dbEnv(dir)
	return d
}

var dbEnvRe = regexp.MustCompile(`(?m)^\s*(?:export\s+)?([A-Z0-9_]*(?:DB|DATABASE|POSTGRES|PG|MYSQL|MONGO)[A-Z0-9_]*(?:URI|URL|DSN))\s*=`)

// dbEnv busca en los .env de ejemplo las variables con la conexión a la base
// de datos. Nunca lee .env: puede tener secretos.
func dbEnv(dir string) []string {
	var out []string
	for _, f := range []string{".env.example", ".env.sample", ".env.template", ".env.dist"} {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			continue
		}
		for _, m := range dbEnvRe.FindAllStringSubmatch(string(b), -1) {
			if !slices.Contains(out, m[1]) {
				out = append(out, m[1])
			}
		}
	}
	return out
}

func goSteps(t Tools) []StepProposal {
	tool := func(name, run, needs string) StepProposal {
		return StepProposal{Name: name, Run: run, Needs: needs, Optional: t.Has == nil || !t.Has(needs)}
	}
	return []StepProposal{
		{Name: "vet", Run: "go vet ./..."},
		{Name: "test", Run: "go test -count=1 {race} ./..."},
		{Name: "build", Run: "go build ./..."},
		tool("lint", "golangci-lint run --new-from-rev={base}", "golangci-lint"),
		tool("vulns", "govulncheck ./...", "govulncheck"),
		tool("secrets", "gitleaks detect --no-banner --redact", "gitleaks"),
	}
}

// npmSteps propone los scripts que verifican: check:* (el :full si existe),
// lint, test (sin watch) y build. Se omiten los agregados (check:all) y los
// que no verifican (start, serve, e2e, :list).
func npmSteps(dir, stack string) []StepProposal {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return nil
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(b, &pkg) != nil {
		return nil
	}
	var checks []string
	for name := range pkg.Scripts {
		switch {
		case !strings.HasPrefix(name, "check:"), name == "check:all", strings.HasSuffix(name, ":list"):
			continue
		case strings.Count(name, ":") > 1 && !strings.HasSuffix(name, ":full"):
			continue // variantes (check:theme:hex): se propone la base o la :full
		case pkg.Scripts[name+":full"] != "":
			continue // se usa la variante completa
		}
		checks = append(checks, name)
	}
	sort.Strings(checks)
	var steps []StepProposal
	for _, c := range checks {
		steps = append(steps, StepProposal{Name: c, Run: "npm run " + c})
	}
	if pkg.Scripts["lint"] != "" {
		steps = append(steps, StepProposal{Name: "lint", Run: "npm run lint"})
	}
	if pkg.Scripts["test"] != "" {
		run := "npm test"
		if stack == "angular" || strings.Contains(pkg.Scripts["test"], "ng test") || strings.Contains(pkg.Scripts["test"], "vitest") {
			run = "npm test -- --watch=false"
		}
		steps = append(steps, StepProposal{Name: "test", Run: run})
	}
	if pkg.Scripts["build"] != "" {
		steps = append(steps, StepProposal{Name: "build", Run: "npm run build"})
	}
	return steps
}

var makeTarget = regexp.MustCompile(`(?m)^([A-Za-z0-9_-]+):`)

func makeSteps(dir string) []StepProposal {
	b, err := os.ReadFile(filepath.Join(dir, "Makefile"))
	if err != nil {
		return nil
	}
	var targets []string
	for _, m := range makeTarget.FindAllStringSubmatch(string(b), -1) {
		targets = append(targets, m[1])
	}
	var steps []StepProposal
	for _, t := range []string{"vet", "lint", "test", "build"} {
		if slices.Contains(targets, t) {
			steps = append(steps, StepProposal{Name: t, Run: "make " + t})
		}
	}
	return steps
}

// HostFromRemote traduce la URL del remoto a host soportado y owner/repo.
func HostFromRemote(url string) (host, repo string) {
	h, r, ok := vcs.ParseRemote(url)
	if !ok {
		return "", ""
	}
	if strings.EqualFold(h, "github.com") {
		return "github", r
	}
	return "", r
}

// PickBase elige la rama base: dev o develop si existen (flujo con rama de
// integración); si no, la rama por defecto del remoto.
func PickBase(head string, branches []string) string {
	for _, b := range []string{"dev", "develop"} {
		if slices.Contains(branches, b) {
			return b
		}
	}
	if head != "" {
		return head
	}
	for _, b := range []string{"main", "master"} {
		if slices.Contains(branches, b) {
			return b
		}
	}
	return ""
}

// Profile son los valores que ya da el perfil (no se repiten en el repo).
type Profile struct {
	Tracker, URL, Workspace, Host, BaseBranch string
	Agent                                     []string
}

// Answers son las respuestas de init.
type Answers struct {
	Profile, Stack                 string
	Agent                          []string // herramientas de agente: claude, opencode
	Tracker, TrackerURL, Workspace string
	Project                        string
	Host, BaseBranch               string
	Lang                           string // idioma/language
	Steps                          []StepProposal
	Quick                          []string
	RequireEnv                     []string // variables que el check necesita definidas
}

type yamlTracker struct {
	Adapter   string `yaml:"adapter,omitempty"`
	URL       string `yaml:"url,omitempty"`
	Workspace string `yaml:"workspace,omitempty"`
	Project   string `yaml:"project,omitempty"`
}

type yamlVCS struct {
	Host       string `yaml:"host,omitempty"`
	BaseBranch string `yaml:"base_branch,omitempty"`
}

type yamlCheck struct {
	Steps []StepProposal `yaml:"steps,omitempty"`
	Quick []string       `yaml:"quick,omitempty"`
}

type yamlEnv struct {
	RequireEnv map[string]string `yaml:"require_env,omitempty"`
}

type yamlUI struct {
	Lang string `yaml:"lang,omitempty"`
}

// agentList es agent: como texto con una herramienta y como lista en línea
// con varias.
type agentList []string

func (l agentList) MarshalYAML() (any, error) {
	if len(l) == 1 {
		return l[0], nil
	}
	n := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
	for _, v := range l {
		n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: v})
	}
	return n, nil
}

type yamlRepo struct {
	Profile string       `yaml:"profile,omitempty"`
	Stack   string       `yaml:"stack,omitempty"`
	Agent   agentList    `yaml:"agent,omitempty"`
	Tracker *yamlTracker `yaml:"tracker,omitempty"`
	VCS     *yamlVCS     `yaml:"vcs,omitempty"`
	Check   *yamlCheck   `yaml:"check,omitempty"`
	Env     *yamlEnv     `yaml:"env,omitempty"`
	UI      *yamlUI      `yaml:"ui,omitempty"`
}

func unless(v, inProfile string) string {
	if v == inProfile {
		return ""
	}
	return v
}

// RenderYAML arma un bflow.yaml con solo lo propio del repo.
func RenderYAML(a Answers, p Profile) (string, error) {
	r := yamlRepo{Profile: a.Profile, Stack: a.Stack}
	if !slices.Equal(a.Agent, p.Agent) {
		r.Agent = a.Agent
	}
	t := yamlTracker{Adapter: unless(a.Tracker, p.Tracker), URL: unless(a.TrackerURL, p.URL), Workspace: unless(a.Workspace, p.Workspace), Project: a.Project}
	if t.Adapter == "local" && p.Tracker == "" {
		t.Adapter = "" // local es el default
	}
	if t != (yamlTracker{}) {
		r.Tracker = &t
	}
	v := yamlVCS{Host: unless(a.Host, p.Host), BaseBranch: unless(a.BaseBranch, p.BaseBranch)}
	if v != (yamlVCS{}) {
		r.VCS = &v
	}
	if len(a.Steps)+len(a.Quick) > 0 {
		r.Check = &yamlCheck{Steps: a.Steps, Quick: a.Quick}
	}
	if len(a.RequireEnv) > 0 {
		r.Env = &yamlEnv{RequireEnv: map[string]string{}}
		for _, v := range a.RequireEnv {
			r.Env.RequireEnv[v] = ""
		}
	}
	if a.Lang != "" {
		r.UI = &yamlUI{Lang: a.Lang}
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(r); err != nil {
		return "", err
	}
	out := "# bflow.yaml — generado por bflow init. Solo lo propio de este repo; lo compartido va en el perfil.\n" + buf.String()
	if len(a.RequireEnv) > 0 {
		out += "# La base de datos de las pruebas sale de " + strings.Join(a.RequireEnv, ", ") + ": doctor y el inicio de sesión avisan si falta.\n" +
			"# Para que tampoco apunte a producción, pon en require_env un fragmento que deba contener (por ejemplo localhost),\n" +
			"# y en check, env_first: true para que el check no corra sin ella.\n"
	}
	return out, nil
}
