// Package config carga la configuración de bflow en tres capas: global
// (perfiles compartidos entre repos), perfil y repo (bflow.yaml). La última gana.
// Los secretos nunca viven aquí: van al llavero del sistema o a variables de entorno.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"innobytes.tech/bflow/internal/flow"
	"innobytes.tech/bflow/internal/i18n"
	"innobytes.tech/bflow/internal/review"
)

// RepoFile es el nombre del archivo de configuración del repo.
const RepoFile = "bflow.yaml"

// Config es la configuración efectiva de un repo.
type Config struct {
	Profile string               `yaml:"profile,omitempty"`
	Project Project              `yaml:"project,omitempty"`
	Stack   string               `yaml:"stack,omitempty"`
	Agent   Tools                `yaml:"agent,omitempty"`
	Tracker Tracker              `yaml:"tracker,omitempty"`
	VCS     VCS                  `yaml:"vcs,omitempty"`
	Flow    Flow                 `yaml:"flow,omitempty"`
	Check   Check                `yaml:"check,omitempty"`
	Env     Env                  `yaml:"env,omitempty"`
	Guard   Guard                `yaml:"guard,omitempty"`
	Agents  map[string]AgentConf `yaml:"agents,omitempty"` // ajustes por agente para bflow render
	Doctor  Doctor               `yaml:"doctor,omitempty"`
	UI      UI                   `yaml:"ui,omitempty"` // preferencias de la persona; también en la config global
	// Models traduce los alias de modelo de los agentes (sonnet, haiku) al
	// proveedor/modelo de cada herramienta: models.<herramienta>.<alias>.
	Models map[string]map[string]string `yaml:"models,omitempty"`
	// PersonalModels son los models del nivel superior de la config global: no
	// entran a los archivos versionados, solo al opencode.json del usuario.
	PersonalModels map[string]map[string]string `yaml:"-"`
	// ProfileModelsIgnored avisa que el perfil activo definía models y se ignoraron.
	ProfileModelsIgnored bool `yaml:"-"`

	Root    string  `yaml:"-"` // raíz del repo
	Sources Sources `yaml:"-"`
}

// Sources dice de dónde salió la configuración (para doctor y mensajes).
type Sources struct {
	Global  string
	Profile string
	Repo    string
}

type Project struct {
	Name     string `yaml:"name,omitempty"`
	Language string `yaml:"language,omitempty"`
}

type Tracker struct {
	Adapter    string              `yaml:"adapter,omitempty"`
	URL        string              `yaml:"url,omitempty"`
	Workspace  string              `yaml:"workspace,omitempty"`
	Project    string              `yaml:"project,omitempty"`     // identificador legible (API), nunca UUID
	Path       string              `yaml:"path,omitempty"`        // tracker local: carpeta de las tareas (.bflow/local por defecto)
	Prefix     string              `yaml:"prefix,omitempty"`      // github: prefijo de los IDs (GH-42); GH por defecto
	StartField string              `yaml:"start_field,omitempty"` // github: campo de fecha del project donde se sella el inicio
	States     map[string][]string `yaml:"states,omitempty"`      // fase bflow → nombres en el tracker (el primero se usa al escribir)
}

type BranchPrefix struct {
	Feature string `yaml:"feature,omitempty"`
	Hotfix  string `yaml:"hotfix,omitempty"`
}

type VCS struct {
	Host              string       `yaml:"host,omitempty"`    // "" = sin host remoto
	APIURL            string       `yaml:"api_url,omitempty"` // API del host (GitHub Enterprise); por defecto la pública
	Repo              string       `yaml:"repo,omitempty"`    // owner/name; si falta se deduce del remoto
	Remote            string       `yaml:"remote,omitempty"`
	BaseBranch        string       `yaml:"base_branch,omitempty"`
	BranchPrefix      BranchPrefix `yaml:"branch_prefix,omitempty"`
	BranchPattern     string       `yaml:"branch_pattern,omitempty"` // {prefix}{id}-{slug} por defecto
	CommitStyle       string       `yaml:"commit_style,omitempty"`   // commits que hace bflow: {type}: {id} {summary} por defecto
	ProtectedBranches []string     `yaml:"protected_branches,omitempty"`
}

// AgentConf ajusta un agente del flujo. El contrato con bflow no se configura:
// sale del flujo. Esto es el oficio del repo y cómo se ejecuta el agente.
type AgentConf struct {
	Model  string   `yaml:"model,omitempty"`
	Effort string   `yaml:"effort,omitempty"`
	Read   []string `yaml:"read,omitempty"`  // documentos del repo que el agente lee antes de empezar
	Extra  string   `yaml:"extra,omitempty"` // archivo con instrucciones propias; se copia al agente
	// OmitClaudeMd evita cargar CLAUDE.md en el agente. Por defecto, sí cuando
	// hay Read: las reglas del repo le llegan por esas rutas.
	OmitClaudeMd *bool `yaml:"omit_claude_md,omitempty"`
}

// Doctor ajusta los avisos de bflow doctor.
// UI son preferencias de cómo bflow se muestra a la persona.
type UI struct {
	Watch bool `yaml:"watch,omitempty"` // start abre el panel (bflow watch) en otra ventana
	// Además del panel en la terminal, la página en el navegador (bflow ui). La
	// sirve la misma ventana del panel: al cerrarla se cierra la página.
	Web bool `yaml:"web,omitempty"`
	// Notificación del sistema cuando una tarea espera a la persona. Encendida
	// salvo notify: false; nunca en CI ni sin escritorio.
	Notify *bool `yaml:"notify,omitempty"`
	// Lang es el idioma de la interfaz y de los agentes: en, es.
	Lang string `yaml:"lang,omitempty"`
}

type Doctor struct {
	IgnoreSkills []string `yaml:"ignore_skills,omitempty"` // skills que no chocan con el flujo aunque lo parezcan
}

// Efforts son los niveles de esfuerzo que acepta un agente.
var Efforts = []string{"low", "medium", "high", "xhigh", "max"}

type Flow struct {
	Lanes            map[string][]string `yaml:"lanes,omitempty"`
	Agents           map[string][]string `yaml:"agents,omitempty"`
	MaxQualityRounds int                 `yaml:"max_quality_rounds,omitempty"`
	SLAHours         int                 `yaml:"sla_hours,omitempty"`
	SecurityAudit    *bool               `yaml:"security_audit,omitempty"` // true: security-auditor aparte, además del reviewer
	UI               *bool               `yaml:"ui,omitempty"`             // por defecto lo decide el stack
	Scout            *bool               `yaml:"scout,omitempty"`          // false apaga el scout
	// ConsumerChangelog: dónde va el único changelog para consumidores, con
	// {id} y {slug}. Por defecto, junto a la spec.
	ConsumerChangelog string `yaml:"consumer_changelog,omitempty"`
	// PublicPaths: rutas del repo que cambian lo que ve quien usa el producto;
	// si el diff las toca, el documenter debe cambiar el changelog. "dir/" = carpeta,
	// "!patrón" excluye.
	PublicPaths []string `yaml:"public_paths,omitempty"`
	// NarrativeComments: false apaga el rechazo narrative_comment del DONE
	// (comentarios agregados que citan la tarea, la spec o sus criterios).
	NarrativeComments *bool `yaml:"narrative_comments,omitempty"`
	// CommentPatterns: expresiones regulares que delatan un comentario
	// narrativo; reemplazan a los fijos. El ID de la tarea se busca siempre.
	CommentPatterns []string `yaml:"comment_patterns,omitempty"`
	// Budget es el presupuesto de tokens nuevos por fase (GH-16). Fase ausente
	// o mapa vacío: sin presupuesto, sin aviso.
	Budget map[string]TokenCount `yaml:"budget,omitempty"`
	// LoopRepeats: a la N-ésima repetición idéntica de un subagente (Bash,
	// Read, Edit, Write en la misma corrida) el guard la rechaza. 0 = apagado.
	LoopRepeats int `yaml:"loop_repeats,omitempty"`
}

// TokenCount es una cifra de tokens en bflow.yaml: entero (200000) o con
// sufijo k/K (×1e3) o m/M (×1e6), con decimal opcional (1.5M).
type TokenCount int64

var tokenCountRe = regexp.MustCompile(`^([0-9]*\.?[0-9]+)([kKmM]?)$`)

// UnmarshalYAML acepta 200000, 200k o 1.5M; cualquier otra cosa, o una cifra
// ≤ 0, es un error (lo ve bflow doctor vía Config.Validate).
func (t *TokenCount) UnmarshalYAML(n *yaml.Node) error {
	raw := strings.TrimSpace(n.Value)
	m := tokenCountRe.FindStringSubmatch(raw)
	if m == nil {
		return fmt.Errorf("flow.budget: cifra inválida %q", raw)
	}
	f, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return fmt.Errorf("flow.budget: cifra inválida %q", raw)
	}
	switch m[2] {
	case "k", "K":
		f *= 1e3
	case "m", "M":
		f *= 1e6
	}
	v := int64(f)
	if v <= 0 {
		return fmt.Errorf("flow.budget: cifra inválida %q", raw)
	}
	*t = TokenCount(v)
	return nil
}

type Step struct {
	Name     string `yaml:"name"`
	Run      string `yaml:"run"`
	Optional bool   `yaml:"optional,omitempty"` // si la herramienta falta, el check queda degradado, no falla
	Needs    string `yaml:"needs,omitempty"`    // binario requerido (para detectar el degradado)
	Accept   string `yaml:"accept_file,omitempty"`
	Baseline string `yaml:"baseline,omitempty"` // "new-only": solo cuentan hallazgos en archivos tocados
}

type Check struct {
	Steps         []Step   `yaml:"steps,omitempty"`
	Quick         []string `yaml:"quick,omitempty"`
	CodePaths     []string `yaml:"code_paths,omitempty"`
	EnvFirst      bool     `yaml:"env_first,omitempty"` // no correr si fallan env.tcp o env.require_env (la salud del API no cuenta)
	DBTestPattern string   `yaml:"db_test_pattern,omitempty"`
	DBTestImports []string `yaml:"db_test_imports,omitempty"` // sufijos de import que marcan un paquete de tests con BD
	FailPattern   string   `yaml:"fail_pattern,omitempty"`    // líneas que resumen un fallo (por defecto, las del stack)
	TestStep      string   `yaml:"test_step,omitempty"`       // paso cuyo fallo cuenta como reproducción de un hotfix (por defecto, test)
}

type Env struct {
	APIURL      string            `yaml:"api_url,omitempty"`
	HealthPaths []string          `yaml:"health_paths,omitempty"`
	TCP         []string          `yaml:"tcp,omitempty"` // nombre=host:puerto
	TimeoutMS   int               `yaml:"timeout_ms,omitempty"`
	RequireEnv  map[string]string `yaml:"require_env,omitempty"` // VAR → fragmento que debe contener
}

type Guard struct {
	ProtectedPaths []string `yaml:"protected_paths,omitempty"` // la sesión principal no las edita
	TestPatterns   []string `yaml:"test_patterns,omitempty"`   // no se editan después de aprobar el contrato
	MaxDiffLines   int      `yaml:"max_diff_lines,omitempty"`
	ForbidCoauthor bool     `yaml:"forbid_coauthor,omitempty"` // bloquear el trailer Co-Authored-By en commits
	StrictLeader   bool     `yaml:"strict_leader,omitempty"`   // la sesión principal no edita protected_paths
}

type globalFile struct {
	Profiles map[string]Config            `yaml:"profiles"`
	UI       UI                           `yaml:"ui"`
	Models   map[string]map[string]string `yaml:"models"`
}

// Known son los adaptadores que este binario sabe construir.
var Known = struct{ Trackers, Hosts, Agents, ModelTables []string }{
	Trackers:    []string{"local", "plane", "github"},
	Hosts:       []string{"", "github"},
	Agents:      []string{"claude", "opencode"},
	ModelTables: []string{"opencode"},
}

// GlobalDir es la carpeta de configuración global.
func GlobalDir() string {
	if d := os.Getenv("BFLOW_CONFIG_HOME"); d != "" {
		return d
	}
	if runtime.GOOS == "windows" {
		if d := os.Getenv("AppData"); d != "" {
			return filepath.Join(d, "bflow")
		}
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "bflow")
	}
	home := os.Getenv("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return filepath.Join(home, ".config", "bflow")
}

// GlobalPath es la ruta del archivo global.
func GlobalPath() string { return filepath.Join(GlobalDir(), "config.yaml") }

// FindRoot sube desde dir hasta encontrar bflow.yaml o .git. Si no hay
// ninguno, la raíz es dir.
func FindRoot(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	for d := abs; ; {
		for _, marker := range []string{RepoFile, ".git"} {
			if _, err := os.Stat(filepath.Join(d, marker)); err == nil {
				return d
			}
		}
		parent := filepath.Dir(d)
		if parent == d {
			return abs
		}
		d = parent
	}
}

// Tools son las herramientas de agente del repo. En YAML: texto o lista.
type Tools []string

// UnmarshalYAML acepta `agent: claude` o `agent: [claude, opencode]`.
func (t *Tools) UnmarshalYAML(n *yaml.Node) error {
	*t = nil
	if n.Kind == yaml.SequenceNode {
		var l []string
		if err := n.Decode(&l); err != nil {
			return err
		}
		if len(l) > 0 {
			*t = Tools(l)
		}
		return nil
	}
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	if s != "" {
		*t = Tools{s}
	}
	return nil
}

// Has dice si la herramienta está en la lista.
func (t Tools) Has(name string) bool { return slices.Contains(t, name) }

// Rendered son las herramientas para las que render genera agentes; sin
// lista, solo claude.
func (t Tools) Rendered() []string {
	if len(t) == 0 {
		return []string{"claude"}
	}
	return t
}

// Load carga la configuración efectiva para el repo que contiene dir.
func Load(dir string) (*Config, error) {
	root := FindRoot(dir)
	c := &Config{Root: root}

	globalMap, globalProfiles, err := readGlobal(GlobalPath())
	if err != nil {
		return nil, err
	}
	if globalMap != nil {
		c.Sources.Global = GlobalPath()
	}

	repoPath := filepath.Join(root, RepoFile)
	repoMap, err := readRepo(repoPath)
	if err != nil {
		return nil, err
	}
	if repoMap != nil {
		c.Sources.Repo = repoPath
	}

	profile, _ := repoMap["profile"].(string)
	if env := os.Getenv("BFLOW_PROFILE"); env != "" {
		profile = env
	}
	merged := map[string]any{}
	if ui, ok := globalMap["ui"].(map[string]any); ok { // preferencias personales: el repo o el perfil las pisan
		merged["ui"] = ui
	}
	if models, ok := globalMap["models"]; ok { // mapeo personal: no entra al merge, va a opencode.json del usuario
		rawM, err := yaml.Marshal(models)
		if err == nil {
			err = yaml.Unmarshal(rawM, &c.PersonalModels)
		}
		if err != nil {
			return nil, fmt.Errorf("models en %s: %w", GlobalPath(), err)
		}
	}
	if profile != "" {
		pm, ok := globalProfiles[profile]
		if !ok {
			return nil, fmt.Errorf("perfil %q no existe en %s (disponibles: %s)", profile, GlobalPath(), strings.Join(sortedKeys(globalProfiles), ", "))
		}
		if _, has := pm["models"]; has {
			cp := make(map[string]any, len(pm))
			for k, v := range pm {
				if k != "models" {
					cp[k] = v
				}
			}
			pm = cp
			c.ProfileModelsIgnored = true
		}
		merged = deepMerge(merged, pm)
		c.Sources.Profile = profile
	}
	merged = deepMerge(merged, repoMap)

	raw, err := yaml.Marshal(merged)
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(raw, c); err != nil {
		return nil, fmt.Errorf("configuración combinada: %w", err)
	}
	c.Root = root
	c.Profile = profile
	c.applyDefaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func readGlobal(path string) (map[string]any, map[string]map[string]any, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, map[string]map[string]any{}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := findSecrets(m, ""); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := strictDecode(data, &globalFile{}); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	profiles := map[string]map[string]any{}
	if ps, ok := m["profiles"].(map[string]any); ok {
		for name, p := range ps {
			pm, _ := p.(map[string]any)
			if pm == nil {
				pm = map[string]any{}
			}
			profiles[name] = pm
		}
	}
	return m, profiles, nil
}

func readRepo(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if m == nil {
		m = map[string]any{}
	}
	if err := findSecrets(m, ""); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := strictDecode(data, &Config{}); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

func strictDecode(data []byte, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return humanize(err)
	}
	return nil
}

var secretKey = regexp.MustCompile(`(?i)^(token|api_?key|password|passwd|secret|client_secret|.*_token|.*_secret|.*_password)$`)

func findSecrets(v any, path string) error {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	for _, k := range sortedKeys(m) {
		p := k
		if path != "" {
			p = path + "." + k
		}
		if secretKey.MatchString(k) {
			return fmt.Errorf("%s parece un secreto; los secretos no van en YAML sino en el llavero del sistema (bflow connect <plane|github>) o en una variable de entorno", p)
		}
		if err := findSecrets(m[k], p); err != nil {
			return err
		}
	}
	return nil
}

// deepMerge combina b sobre a: los mapas se combinan, lo demás (listas
// incluidas) se reemplaza.
func deepMerge(a, b map[string]any) map[string]any {
	out := make(map[string]any, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		if bm, ok := v.(map[string]any); ok {
			if am, ok := out[k].(map[string]any); ok {
				out[k] = deepMerge(am, bm)
				continue
			}
		}
		out[k] = v
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func (c *Config) applyDefaults() {
	if c.Tracker.Adapter == "" {
		c.Tracker.Adapter = "local"
	}
	if c.VCS.Remote == "" {
		c.VCS.Remote = "origin"
	}
	if c.VCS.BranchPrefix.Feature == "" {
		c.VCS.BranchPrefix.Feature = "feature/"
	}
	if c.VCS.BranchPrefix.Hotfix == "" {
		c.VCS.BranchPrefix.Hotfix = "hotfix/"
	}
	if c.VCS.BranchPattern == "" {
		c.VCS.BranchPattern = "{prefix}{id}-{slug}"
	}
	if c.VCS.CommitStyle == "" {
		c.VCS.CommitStyle = "{type}: {id} {summary}"
	}
	if len(c.VCS.ProtectedBranches) == 0 {
		c.VCS.ProtectedBranches = []string{"main", "master"}
		if c.VCS.BaseBranch != "" && !slices.Contains(c.VCS.ProtectedBranches, c.VCS.BaseBranch) {
			c.VCS.ProtectedBranches = append(c.VCS.ProtectedBranches, c.VCS.BaseBranch)
		}
	}
	if c.Flow.MaxQualityRounds == 0 {
		c.Flow.MaxQualityRounds = 2
	}
	if c.Flow.SLAHours == 0 {
		c.Flow.SLAHours = 24
	}
	if c.Env.TimeoutMS == 0 {
		c.Env.TimeoutMS = 2500
	}
	if c.Check.TestStep == "" {
		c.Check.TestStep = "test"
	}
	st := stacks[c.Stack]
	if len(c.Check.CodePaths) == 0 {
		c.Check.CodePaths = st.CodePaths
	}
	if len(c.Guard.TestPatterns) == 0 {
		c.Guard.TestPatterns = st.TestPatterns
	}
	if len(c.Guard.ProtectedPaths) == 0 {
		c.Guard.ProtectedPaths = c.Check.CodePaths
	}
	if c.Flow.UI == nil {
		ui := st.UI
		c.Flow.UI = &ui
	}
}

// ValidationError agrupa todos los problemas encontrados.
type ValidationError struct {
	File     string
	Problems []string
}

func (e *ValidationError) Error() string {
	where := "configuración"
	if e.File != "" {
		where = e.File
	}
	return fmt.Sprintf("%s tiene %d problema(s):\n  - %s", where, len(e.Problems), strings.Join(e.Problems, "\n  - "))
}

var (
	ghProjectRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[1-9][0-9]*$`)
	ghPrefixRe  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)
)

var uuidRe = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Validate revisa la configuración efectiva y reporta todos los problemas juntos.
func (c *Config) Validate() error {
	var p []string
	add := func(f string, a ...any) { p = append(p, fmt.Sprintf(f, a...)) }

	if !slices.Contains(Known.Trackers, c.Tracker.Adapter) {
		add("tracker.adapter %q no existe (disponibles: %s)", c.Tracker.Adapter, strings.Join(Known.Trackers, ", "))
	}
	if c.Tracker.Adapter == "plane" {
		if c.Tracker.URL == "" {
			add("tracker.url es obligatorio con plane")
		}
		if c.Tracker.Workspace == "" {
			add("tracker.workspace es obligatorio con plane")
		}
		if c.Tracker.Project == "" {
			add("tracker.project es obligatorio con plane (identificador legible, p. ej. API)")
		}
	}
	if c.Tracker.Adapter == "github" {
		if c.VCS.Host != "github" {
			add("tracker.adapter github exige vcs.host github (de ahí salen el repo y el token)")
		}
		if c.Tracker.Project != "" && !ghProjectRe.MatchString(c.Tracker.Project) {
			add("tracker.project %q no es owner/número (p. ej. acme/7)", c.Tracker.Project)
		}
		if c.Tracker.Prefix != "" && !ghPrefixRe.MatchString(c.Tracker.Prefix) {
			add("tracker.prefix %q debe empezar con letra y llevar solo letras y números", c.Tracker.Prefix)
		}
	}
	if uuidRe.MatchString(c.Tracker.Project) {
		add("tracker.project %q es un UUID; usa el identificador legible del proyecto (p. ej. API) y bflow resuelve el ID interno", c.Tracker.Project)
	}
	if !slices.Contains(Known.Hosts, c.VCS.Host) {
		add("vcs.host %q no existe (disponibles: github, o vacío para solo git local)", c.VCS.Host)
	}
	seenAgent := map[string]bool{}
	for _, a := range c.Agent {
		if !slices.Contains(Known.Agents, a) {
			add("agent %q no existe (disponibles: %s)", a, strings.Join(Known.Agents, ", "))
		} else if seenAgent[a] {
			add("agent repite %q", a)
		}
		seenAgent[a] = true
	}
	validateModels(add, c.Models, "")
	validateModels(add, c.PersonalModels, " (en "+GlobalPath()+")")
	if c.UI.Lang != "" && !slices.Contains(i18n.Supported, c.UI.Lang) {
		add("ui.lang %q no existe (disponibles: %s)", c.UI.Lang, strings.Join(i18n.Supported, ", "))
	}
	for i, s := range c.Check.Steps {
		if s.Name == "" {
			add("check.steps[%d].name es obligatorio", i)
		}
		if strings.TrimSpace(s.Run) == "" {
			add("check.steps[%d].run es obligatorio", i)
		}
		if s.Baseline != "" && s.Baseline != "new-only" {
			add("check.steps[%d].baseline solo admite new-only", i)
		}
	}
	if p := c.Flow.ConsumerChangelog; p != "" && (filepath.IsAbs(p) || strings.HasPrefix(filepath.ToSlash(filepath.Clean(p)), ".bflow/") || strings.HasPrefix(filepath.Clean(p), "..")) {
		add("flow.consumer_changelog debe ser una ruta del repo fuera de .bflow/ (se versiona)")
	}
	for _, p := range c.Flow.PublicPaths {
		q := strings.TrimPrefix(p, "!")
		cl := filepath.ToSlash(filepath.Clean(q))
		if q == "" || filepath.IsAbs(q) || strings.HasPrefix(q, "/") || strings.HasPrefix(cl, ".bflow") || cl == ".." || strings.HasPrefix(cl, "../") || strings.Contains(cl, "/../") {
			add("flow.public_paths: %q debe ser una ruta del repo (sin .., absoluta ni .bflow/)", p)
		}
	}
	for _, p := range c.Flow.CommentPatterns {
		if _, err := regexp.Compile(p); err != nil {
			add("flow.comment_patterns: %q no es una expresión regular válida", p)
		}
	}
	for _, phase := range sortedKeys(c.Flow.Budget) {
		if !slices.Contains(flow.BudgetPhases, flow.Phase(phase)) {
			names := make([]string, len(flow.BudgetPhases))
			for i, p := range flow.BudgetPhases {
				names[i] = string(p)
			}
			add("flow.budget: fase desconocida %q (válidas: %s)", phase, strings.Join(names, ", "))
		}
	}
	if c.Flow.LoopRepeats < 0 || c.Flow.LoopRepeats == 1 {
		add("flow.loop_repeats debe ser 0 (apagado) o ≥ 2 (es %d)", c.Flow.LoopRepeats)
	}
	if c.Check.DBTestPattern != "" {
		if _, err := regexp.Compile(c.Check.DBTestPattern); err != nil {
			add("check.db_test_pattern no es una regex válida: %v", err)
		}
	}
	for lane, phases := range c.Flow.Lanes {
		for _, ph := range phases {
			if !slices.Contains(flow.Order, flow.Phase(ph)) {
				add("flow.lanes.%s: fase desconocida %q", lane, ph)
			}
		}
	}
	unknownPhase := slices.ContainsFunc(p, func(s string) bool { return strings.Contains(s, "fase desconocida") })
	if !unknownPhase {
		core := c.Flow.Core()
		for _, ags := range c.Flow.Agents {
			if slices.Contains(ags, flow.ScoutAgent) {
				add("flow.agents: scout es un agente reservado; se apaga con flow.scout: false")
				break
			}
		}
		if err := core.Validate(); err != nil {
			add("flow: %v", err)
		}
		for _, name := range sortedKeys(c.Agents) {
			a := c.Agents[name]
			switch {
			case len(core.PhasesOf(name)) > 0:
			case name == flow.ScoutAgent && core.Scout != "":
			case name == "security-auditor": // desde que el reviewer revisa la seguridad
				add("agents.security-auditor: ya no trabaja por defecto, el reviewer revisa también la seguridad. Pasa su read y extra a agents.reviewer, o pon flow.security_audit: true para conservarlo aparte")
			default:
				add("agents.%s no trabaja en ninguna fase (revisa flow.agents)", name)
			}
			if a.Effort != "" && !slices.Contains(Efforts, a.Effort) {
				add("agents.%s.effort %q no existe (disponibles: %s)", name, a.Effort, strings.Join(Efforts, ", "))
			}
			for _, r := range append(slices.Clone(a.Read), a.Extra) {
				if filepath.IsAbs(r) || strings.HasPrefix(filepath.ToSlash(filepath.Clean(r)), "../") {
					add("agents.%s: %q debe ser una ruta dentro del repo", name, r)
				}
			}
		}
	}
	if len(p) > 0 {
		return &ValidationError{File: c.Sources.Repo, Problems: p}
	}
	return nil
}

// Core traduce la sección flow a la configuración del núcleo.
func (f Flow) Core() flow.Config {
	core := flow.DefaultConfig()
	for lane, phases := range f.Lanes {
		ps := make([]flow.Phase, len(phases))
		for i, p := range phases {
			ps[i] = flow.Phase(p)
		}
		core.Lanes[flow.Lane(lane)] = ps
	}
	if f.MaxQualityRounds > 0 {
		core.MaxQualityRounds = f.MaxQualityRounds
	}
	core.Changelog = f.ConsumerChangelog
	core.PublicPaths = f.PublicPaths
	core.NarrativeComments = f.NarrativeComments == nil || *f.NarrativeComments
	core.CommentPatterns = f.CommentPatterns
	if len(core.CommentPatterns) == 0 {
		core.CommentPatterns = slices.Clone(review.DefaultCommentPatterns)
	}
	if len(f.Budget) > 0 {
		core.Budget = make(map[flow.Phase]int64, len(f.Budget))
		for phase, n := range f.Budget {
			core.Budget[flow.Phase(phase)] = int64(n)
		}
	}
	if f.Scout == nil || *f.Scout {
		core.Scout = flow.ScoutAgent
	}
	if f.UI != nil && *f.UI {
		core.UI = true
		core.Agents[flow.Spec] = []string{"ui-designer", "spec-author"}
		core.Agents[flow.Quality] = append(slices.Clone(core.Agents[flow.Quality]), "ux-auditor")
	}
	// El reviewer revisa también seguridad; security_audit: true suma un
	// auditor aparte (más tokens, una segunda mirada).
	const sec = "security-auditor"
	switch q := core.Agents[flow.Quality]; {
	case f.SecurityAudit == nil:
	case *f.SecurityAudit && !slices.Contains(q, sec):
		core.Agents[flow.Quality] = append(slices.Clone(q), sec)
	case !*f.SecurityAudit:
		core.Agents[flow.Quality] = slices.DeleteFunc(slices.Clone(q), func(a string) bool { return a == sec })
	}
	for phase, agents := range f.Agents {
		core.Agents[flow.Phase(phase)] = slices.Clone(agents)
	}
	return core
}

var unknownField = regexp.MustCompile(`line (\d+): field (\S+) not found in type \S+`)

// humanize traduce los errores de yaml.v3 que exponen tipos internos de Go.
func humanize(err error) error {
	msg := unknownField.ReplaceAllString(err.Error(), "línea $1: campo desconocido «$2»")
	msg = strings.Replace(msg, "yaml: unmarshal errors:\n", "", 1)
	return errors.New(strings.TrimSpace(msg))
}

// validateModels revisa una tabla de alias; suffix nombra su origen si no es bflow.yaml.
func validateModels(add func(string, ...any), m map[string]map[string]string, suffix string) {
	for _, table := range sortedKeys(m) {
		if !slices.Contains(Known.ModelTables, table) {
			add("models.%s no existe (disponible: %s)%s", table, strings.Join(Known.ModelTables, ", "), suffix)
			continue
		}
		for _, alias := range sortedKeys(m[table]) {
			if strings.TrimSpace(m[table][alias]) == "" {
				add("models.%s.%s está vacío%s", table, alias, suffix)
			}
		}
	}
}
