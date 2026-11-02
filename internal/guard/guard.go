// Package guard decide si una acción de un agente se permite. Hace cumplir
// con código lo que en el harness anterior era prosa ("no edites pruebas",
// "no hagas git reset --hard"). Lo llaman los hooks de la herramienta de
// agente antes de cada uso de herramienta, así que es rápido y no pregunta.
package guard

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"innobytes.tech/bflow/internal/flow"
	"innobytes.tech/bflow/internal/i18n"
)

// Tipos de herramienta, ya traducidos por el adaptador del agente.
const (
	Bash  = "bash"
	Edit  = "edit"
	Write = "write"
	Read  = "read"  // la registran todas las lecturas (guard --reads); Evaluate la permite
	Spawn = "spawn" // lanzamiento de un subagente (Command = prompt, Target = tipo); Evaluate la permite
)

// Action es lo que el agente quiere hacer.
type Action struct {
	Tool     string `json:"tool"`
	Command  string `json:"command,omitempty"`
	Path     string `json:"path,omitempty"`
	Subagent bool   `json:"subagent,omitempty"`
	Agent    string `json:"agent,omitempty"`   // agent_type del hook; "" en la sesión principal
	Partial  bool   `json:"partial,omitempty"` // Read con offset o limit; solo la llenan los parsers
	Session  string `json:"session,omitempty"` // llave de sesión (R4); la llenan los parsers
	Target   string `json:"target,omitempty"`  // Spawn: tipo de subagente lanzado
	// Fingerprint identifica una acción exacta (misma herramienta, misma
	// entrada cruda) para detectar ciclos de un subagente (GH-16). La llenan
	// los adaptadores con Fingerprint(tool, rawInput).
	Fingerprint string `json:"fingerprint,omitempty"`
}

// Fingerprint es la huella sha256 hex de una acción: la herramienta original
// (antes de traducirla a Bash/Edit/Write/Read) más su entrada cruda, separadas
// por un byte nulo. Dos llamadas con exactamente la misma herramienta y
// entrada dan la misma huella.
func Fingerprint(tool string, raw []byte) string {
	h := sha256.New()
	h.Write([]byte(tool))
	h.Write([]byte{0})
	h.Write(raw)
	return hex.EncodeToString(h.Sum(nil))
}

// Context es lo que el guard sabe del repo y de la tarea activa.
type Context struct {
	Root           string
	Protected      []string // ramas protegidas
	ProtectedPaths []string // código: la sesión principal no lo edita con StrictLeader
	TestPatterns   []string
	ForbidCoauthor bool
	StrictLeader   bool
	MaxDiffLines   int
	DiffLines      func() (int, error) // se llama solo al commitear

	// HumanFiles: archivos que solo edita una persona (las vulnerabilidades
	// aceptadas en accept_file de check.steps).
	HumanFiles []string

	Phase  flow.Phase // fase de la tarea activa ("" si no hay)
	Frozen []string   // pruebas congeladas al aprobar el contrato
	Loc    *i18n.Localizer
}

// Decision es el resultado.
type Decision struct {
	Allow  bool   `json:"allow"`
	Rule   string `json:"rule,omitempty"`
	Reason string `json:"reason,omitempty"`
}

var allow = Decision{Allow: true}

func deny(rule, format string, a ...any) Decision {
	return Decision{Rule: rule, Reason: fmt.Sprintf(format, a...)}
}

// frozenPhases son las fases en las que las pruebas del contrato no se tocan.
var frozenPhases = []flow.Phase{flow.Implementing, flow.Paused, flow.Quality, flow.Documenting, flow.Walkthrough}

// Evaluate aplica las reglas en orden.
func Evaluate(a Action, c Context) Decision {
	switch a.Tool {
	case Bash:
		return bash(a, c)
	case Edit, Write:
		return edit(a, c)
	}
	return allow
}

// Segments separa un comando en sus segmentos (&&, ||, ; y |), recortados y sin
// vacíos. Lo usan TaskScoped, bash y review.FromAction.
func Segments(cmd string) []string { return segments(cmd, 0) }

var heredocRe = regexp.MustCompile(`(?:^|[^<])<<-?\s*(?:'([^']+)'|"([^"]+)"|([A-Za-z_][A-Za-z0-9_]*))`)

// heredocMatch devuelve el primer heredoc de la línea cuyo << está fuera de
// comillas (mismo escaneo que splitCommand), o nil.
func heredocMatch(line string) []string {
	for _, ix := range heredocRe.FindAllStringSubmatchIndex(line, -1) {
		at := strings.Index(line[ix[0]:], "<<") + ix[0]
		if quotedAt(line, at) {
			continue
		}
		m := make([]string, 4)
		for k := range m {
			if ix[2*k] >= 0 {
				m[k] = line[ix[2*k]:ix[2*k+1]]
			}
		}
		return m
	}
	return nil
}

// quotedAt dice si la posición pos de s queda dentro de comillas. Un $( dentro
// de comillas dobles abre un contexto sin comillas hasta su ).
func quotedAt(s string, pos int) bool {
	var q byte
	var stack []byte // comilla que rodea a cada $( abierto
	for i := 0; i < pos && i < len(s); i++ {
		c := s[i]
		switch {
		case q == '"' && c == '$' && i+1 < len(s) && s[i+1] == '(':
			stack = append(stack, q)
			q = 0
			i++
		case q != 0:
			if c == q {
				q = 0
			} else if c == '\\' && q == '"' {
				i++
			}
		case c == ')' && len(stack) > 0:
			q = stack[len(stack)-1]
			stack = stack[:len(stack)-1]
		case c == '"' || c == '\'':
			q = c
		case c == '\\':
			i++
		}
	}
	return q != 0
}

var shellNames = map[string]bool{"bash": true, "sh": true, "zsh": true, "dash": true, "ksh": true}

// feedsShell dice si algún segmento de la línea ejecuta un shell (bash <<EOF,
// cat <<EOF | bash): entonces el cuerpo del heredoc son comandos.
func feedsShell(line string) bool {
	for _, seg := range segSplit.Split(line, -1) {
		for _, f := range quotedFields(seg) {
			if strings.Contains(f, "=") && !strings.HasPrefix(f, "-") {
				continue
			}
			f = strings.Trim(f, `"'`)
			f = strings.ToLower(f[strings.LastIndexAny(f, `/\`)+1:])
			f = strings.TrimSuffix(f, ".exe")
			if shellNames[f] {
				return true
			}
			if f != "sudo" && f != "env" && f != "exec" {
				break
			}
		}
	}
	return false
}

// stripHeredocs quita el cuerpo de los heredocs que leen otros programas: es
// texto. Si el heredoc alimenta a un shell, el cuerpo se conserva (se ejecuta).
func stripHeredocs(cmd string) string {
	if !strings.Contains(cmd, "<<") {
		return cmd
	}
	var out []string
	end, run := "", false
	for _, line := range strings.Split(cmd, "\n") {
		if end != "" {
			if strings.TrimSpace(line) == end {
				end = ""
			} else if run {
				out = append(out, line)
			}
			continue
		}
		out = append(out, line)
		if m := heredocMatch(line); m != nil {
			end = m[1] + m[2] + m[3]
			run = feedsShell(line)
		}
	}
	return strings.Join(out, "\n")
}

// splitCommand parte en &&, ||, ;, | y saltos de línea, pero no dentro de
// comillas ni en el cuerpo de un heredoc. Si las comillas no cierran o hay sustitución ($(...), `) entre comillas dobles, cae al
// corte simple para no esconder comandos.
func splitCommand(cmd string) []string {
	cmd = stripHeredocs(cmd)
	var out []string
	var q byte
	subst := false
	start := 0
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch {
		case q != 0:
			if c == q {
				q = 0
			} else if c == '\\' && q == '"' {
				i++
			} else if q == '"' && (c == '`' || c == '$' && i+1 < len(cmd) && cmd[i+1] == '(') {
				subst = true
			}
		case c == '"' || c == '\'':
			q = c
		case c == '\\':
			i++
		case c == '|' && i > 0 && cmd[i-1] == '>': // >| es una redirección, no un pipe
		case c == ';' || c == '|' || c == '\n' || c == '&' && i+1 < len(cmd) && cmd[i+1] == '&':
			out = append(out, cmd[start:i])
			if (c == '|' || c == '&') && i+1 < len(cmd) && cmd[i+1] == c {
				i++
			}
			start = i + 1
		}
	}
	if subst {
		cmd = strings.NewReplacer("$(", ";", "`", ";", ")", ";").Replace(cmd)
	}
	if q != 0 || subst {
		return segSplit.Split(cmd, -1)
	}
	return append(out, cmd[start:])
}

func segments(cmd string, depth int) []string {
	var out []string
	for _, seg := range splitCommand(cmd) {
		s := strings.TrimSpace(seg)
		if s == "" {
			continue
		}
		if depth < 3 {
			if inner, ok := unwrapShell(s); ok {
				out = append(out, segments(inner, depth+1)...)
				continue
			}
		}
		out = append(out, s)
	}
	return out
}

var groupedFlags = regexp.MustCompile(`^-[A-Za-z]+$`)

// unwrapShell devuelve <cmd> de `bash|sh|zsh [-flags] -c "<cmd>"`.
func unwrapShell(seg string) (string, bool) {
	fields := quotedFields(seg)
	t := skipPrefix(slices.Clone(fields))
	if len(t) == 0 {
		return "", false
	}
	switch exeName(t[0]) {
	case "bash", "sh", "zsh":
	default:
		return "", false
	}
	i := 1
	found := false
	for ; i < len(t) && t[i] != "" && (t[i][0] == '-' || t[i][0] == '+'); i++ {
		switch t[i] {
		case "-o", "+o", "-O", "+O":
			i++
			continue
		}
		if groupedFlags.MatchString(t[i]) && strings.Contains(t[i], "c") {
			found = true
			break
		}
	}
	if !found {
		return "", false
	}
	// texto original tras el token -c
	rest := seg
	for k := 0; k <= i+len(fields)-len(t); k++ {
		rest = strings.TrimSpace(rest)
		f := quotedFields(rest)
		if len(f) == 0 {
			return "", false
		}
		rest = rest[len(f[0]):]
	}
	rest = strings.TrimSpace(rest)
	if rest != "" && (rest[0] == '"' || rest[0] == '\'') {
		q := rest[0]
		rest = rest[1:]
		if n := len(rest); n > 0 && rest[n-1] == q {
			rest = rest[:n-1]
		}
	}
	return rest, true
}

// exeName es el nombre base en minúsculas, sin comillas, ruta ni .exe.
func exeName(w string) string {
	w = strings.TrimRight(strings.ReplaceAll(strings.Trim(w, `"'`), `\`, "/"), "/")
	return strings.TrimSuffix(strings.ToLower(w[strings.LastIndex(w, "/")+1:]), ".exe")
}

// skipPrefix quita asignaciones VAR=x y `env [flags] VAR=x` del inicio, y
// las comillas de cada token.
func skipPrefix(t []string) []string {
	for i := range t {
		t[i] = strings.Trim(t[i], `"'`)
	}
	isAssign := func(w string) bool { return strings.Contains(w, "=") && !strings.HasPrefix(w, "-") }
	for len(t) > 0 && isAssign(t[0]) {
		t = t[1:]
	}
	if len(t) > 0 && strings.ToLower(t[0]) == "env" {
		t = t[1:]
		for len(t) > 0 && (strings.HasPrefix(t[0], "-") || isAssign(t[0])) {
			t = t[1:]
		}
	}
	return t
}

var (
	segSplit     = regexp.MustCompile(`&&|\|\||;|\|`)
	resetHard    = regexp.MustCompile(`^git\s+(-\S+\s+)*reset\b.*--hard`)
	cleanForce   = regexp.MustCompile(`^git\s+(-\S+\s+)*clean\b.*\s-[a-zA-Z]*f`)
	checkoutDash = regexp.MustCompile(`^git\s+(-\S+\s+)*checkout\b.*\s--(\s|$)`)
	restore      = regexp.MustCompile(`^git\s+(-\S+\s+)*restore\b`)
	stashDrop    = regexp.MustCompile(`^git\s+(-\S+\s+)*stash\s+(drop|clear)\b`)
	pushRe       = regexp.MustCompile(`^git\s+(-\S+\s+)*push\b(.*)$`)
	forceRe      = regexp.MustCompile(`(^|\s)(--force(-with-lease)?|-f)(\s|$|=)`)
	addRe        = regexp.MustCompile(`^git\s+(-[cC]\s+\S+\s+|-\S+\s+)*(add|stage)\b(.*)$`)
	commitRe     = regexp.MustCompile(`^git\s+(-\S+\s+)*commit\b`)
	coauthorRe   = regexp.MustCompile(`(?i)co-authored-by`)
	rmRe         = regexp.MustCompile(`^(rm|del|git\s+rm|mv|git\s+mv)\s`)

	// Acciones que le tocan a bflow mientras hay una tarea en curso.
	ghPRCreate  = regexp.MustCompile(`^gh\s+pr\s+create\b`)
	checkoutNew = regexp.MustCompile(`^git\s+(-\S+\s+)*checkout\s+(.*\s)?-[bB](\s|$)`)
	switchNew   = regexp.MustCompile(`^git\s+(-\S+\s+)*switch\s+(.*\s)?(-[cC]|--create|--force-create)(\s|$)`)
	branchNew   = regexp.MustCompile(`^git\s+(-\S+\s+)*branch\s+[^-\s]`)
)

// HumanOnly son los subcomandos que responden una decisión humana: un
// subagente no los corre (los corre la sesión principal después de preguntar).
var HumanOnly = []string{"approve", "reject", "unblock", "start", "new", "drop", "pause"}

// BflowSubcommand devuelve el subcomando de bflow de un segmento ya recortado
// ("approve" en `./bin/bflow.exe --json approve X`), o "" si el segmento no
// invoca a bflow.
func BflowSubcommand(seg string) string {
	t, ok := bflowTokens(seg)
	if !ok || len(t) == 0 {
		return ""
	}
	return strings.ToLower(t[0])
}

// BflowArgs devuelve los argumentos sin guion tras el subcomando de un segmento
// bflow (misma lógica de BflowSubcommand); nil si el segmento no es bflow.
func BflowArgs(seg string) []string {
	t, ok := bflowTokens(seg)
	if !ok || len(t) < 2 {
		return nil
	}
	var out []string
	for _, w := range t[1:] {
		if !strings.HasPrefix(w, "-") {
			out = append(out, w)
		}
	}
	return out
}

// bflowTokens devuelve los tokens de un segmento bflow desde el subcomando
// (sin los flags previos a él); ok=false si el segmento no invoca a bflow.
func bflowTokens(seg string) ([]string, bool) {
	t := skipPrefix(quotedFields(seg))
	if len(t) == 0 {
		return nil, false
	}
	parts := func(w string) []string {
		return strings.Split(strings.ReplaceAll(w, `\`, "/"), "/")
	}
	name := func(w string) string {
		ps := parts(strings.TrimRight(strings.ReplaceAll(w, `\`, "/"), "/"))
		return strings.TrimSuffix(strings.ToLower(ps[len(ps)-1]), ".exe")
	}
	switch {
	case name(t[0]) == "bflow":
		t = t[1:]
	case strings.ToLower(t[0]) == "go" && len(t) > 1 && strings.ToLower(t[1]) == "run":
		t = t[2:]
		for len(t) > 0 && strings.HasPrefix(t[0], "-") {
			t = t[1:]
		}
		if len(t) == 0 {
			return nil, false
		}
		ps := parts(strings.TrimRight(strings.ReplaceAll(t[0], `\`, "/"), "/"))
		if len(ps) < 2 || ps[len(ps)-1] != "bflow" || ps[len(ps)-2] != "cmd" {
			return nil, false
		}
		t = t[1:]
	default:
		return nil, false
	}
	for len(t) > 0 && strings.HasPrefix(t[0], "-") {
		t = t[1:]
	}
	return t, true
}

// TaskScoped dice si el comando toca algo que bflow maneja por tarea (rama,
// PR): el guard necesita saber si hay una tarea en curso.
func TaskScoped(cmd string) bool {
	for _, s := range Segments(cmd) {
		if ghPRCreate.MatchString(s) || checkoutNew.MatchString(s) || switchNew.MatchString(s) || branchNew.MatchString(s) {
			return true
		}
	}
	return false
}

func bash(a Action, c Context) Decision {
	for _, s := range Segments(a.Command) {
		s = stripGitGlobals(s)
		if d := protectedWrite(s, c); !d.Allow {
			return d
		}
		sub := BflowSubcommand(s)
		switch {
		case resetHard.MatchString(s), cleanForce.MatchString(s), checkoutDash.MatchString(s), stashDrop.MatchString(s):
			return deny("git_destructive", "`%s` descarta trabajo sin forma de recuperarlo. Si necesitas deshacer algo, haz un commit que lo revierta o pregunta.", s)
		case restore.MatchString(s) && !strings.Contains(s, "--staged"):
			return deny("git_destructive", "`%s` descarta cambios del árbol de trabajo. Para quitar algo del stage usa git restore --staged.", s)
		case sub == "freeze":
			return deny("human_only", "bflow freeze acepta cambios a pruebas congeladas: lo corre una persona desde su terminal. Si una prueba está mal, reporta NEEDS_DECISION.")
		case a.Subagent && slices.Contains(HumanOnly, sub):
			return deny("human_only", "`bflow %s` responde una decisión de una persona: lo corre la sesión principal después de preguntarle. Termina tu parte con `bflow report` (o NEEDS_DECISION).", sub)
		case c.Phase != "" && ghPRCreate.MatchString(s):
			return deny("bflow_pr", "el PR lo abre bflow al aprobar el walkthrough, con el review-map y el walkthrough; para actualizarlo usa bflow pr.")
		case c.Phase != "" && (checkoutNew.MatchString(s) || switchNew.MatchString(s) || branchNew.MatchString(s)):
			return deny("bflow_branch", "la rama de la tarea la crea bflow (al aprobar el spec o al empezar un hotfix); sigue el next de bflow status.")
		}
		if m := pushRe.FindStringSubmatch(s); m != nil {
			args := m[2]
			if forceRe.MatchString(args) || plusRefspec(args) {
				return deny("force_push", "push forzado prohibido: reescribe historia compartida.")
			}
			for _, f := range strings.Fields(args) {
				target := strings.TrimPrefix(f[strings.LastIndex(f, ":")+1:], "+")
				if slices.Contains(c.Protected, target) {
					return deny("protected_branch", "no se empuja a %s: los cambios llegan por PR (bflow pr).", target)
				}
			}
		}
		if m := addRe.FindStringSubmatch(s); m != nil {
			bad := false
			for _, f := range strings.Fields(m[3]) {
				if f == "--force" || (strings.HasPrefix(f, "-") && !strings.HasPrefix(f, "--") && strings.Contains(f, "f")) {
					bad = true
					break
				}
				r := strings.ReplaceAll(rel(c.Root, f), `\`, "/")
				if (r == ".bflow" || strings.HasPrefix(r, ".bflow/")) && !evalsPath(r) {
					bad = true
					break
				}
			}
			if bad {
				return deny("bflow_tracked", "`%s` metería archivos que git ignora: lo de .bflow/ (walkthrough, reportes) se queda fuera del repo; bflow commitea la spec. Agrega solo código, docs y el changelog para consumidores, sin -f.", s)
			}
		}
		if commitRe.MatchString(s) {
			if c.ForbidCoauthor && coauthorRe.MatchString(s) {
				return deny("coauthor", "este repo no usa el trailer Co-Authored-By en los commits.")
			}
			if c.MaxDiffLines > 0 && c.DiffLines != nil {
				if n, err := c.DiffLines(); err == nil && n > c.MaxDiffLines {
					return deny("diff_size", "el diff de la tarea suma %d líneas y el máximo es %d: divide el cambio o pide NEEDS_DECISION.", n, c.MaxDiffLines)
				}
			}
		}
		if m := rmRe.FindString(s); m != "" && isFrozenPhase(c) {
			for _, f := range strings.Fields(s)[1:] {
				if slices.Contains(c.Frozen, rel(c.Root, f)) {
					return deny("frozen_test", "%s es una prueba congelada al aprobar el contrato: no se borra ni se mueve.", f)
				}
			}
		}
	}
	if isFrozenPhase(c) {
		if p, read := heredocsAll(a.Command, c, 0); p != "" {
			return deny("frozen_test", "%s", frozenBashReason(p, read))
		}
		for _, s := range Segments(a.Command) {
			if p, read := frozenWrite(s, c); p != "" {
				return deny("frozen_test", "%s", frozenBashReason(p, read))
			}
		}
	}
	return allow
}

func edit(a Action, c Context) Decision {
	p := rel(c.Root, a.Path)
	base := filepath.Base(p)
	if (base == ".env" || strings.HasPrefix(base, ".env.")) && base != ".env.example" {
		return deny("env_file", "%s puede tener secretos: no se edita desde un agente.", p)
	}
	if (p == ".bflow" || strings.HasPrefix(p, ".bflow/")) && !agentArtifact(p) && !evalOutArtifact(p) {
		return deny("bflow_state", "%s es estado de bflow: cámbialo con sus comandos (report, approve, block…), no a mano. Los agentes solo escriben %s en .bflow/tasks/<ID>/.",
			p, strings.Join(flow.AgentArtifacts, ", "))
	}
	if slices.Contains(c.HumanFiles, p) {
		return deny("human_file", "%s guarda las vulnerabilidades aceptadas: lo edita una persona. Si hace falta aceptar una, reporta NEEDS_DECISION.", p)
	}
	if isFrozenPhase(c) && slices.Contains(c.Frozen, p) {
		return deny("frozen_test", "%s", frozenBashReason(p, false))
	}
	if c.StrictLeader && !a.Subagent && underAny(p, c.ProtectedPaths) {
		return deny("leader_code", "la sesión principal no edita %s: eso lo hace el implementer.", p)
	}
	return allow
}

// agentArtifact dice si p (.bflow/tasks/<ID>/<rel>) es un archivo que escribe un agente.
func agentArtifact(p string) bool {
	parts := strings.SplitN(p, "/", 4)
	return len(parts) == 4 && parts[1] == "tasks" && flow.IsAgentArtifact(parts[3])
}

// evalOutArtifact dice si p es un .md directo en la carpeta out de un caso del
// eval (.bflow/cache/evals/<corrida>/<caso>/out/<nombre>.md).
func evalOutArtifact(p string) bool {
	parts := strings.Split(p, "/")
	if len(parts) != 7 || parts[0] != ".bflow" || parts[1] != "cache" || parts[2] != "evals" || parts[5] != "out" {
		return false
	}
	for _, s := range parts {
		if s == "" || s == "." || s == ".." {
			return false
		}
	}
	return strings.HasSuffix(parts[6], ".md") && len(parts[6]) > 3
}

// evalsPath dice si r (relativa, con /) está bajo .bflow/evals/, sin escapar con "..".
func evalsPath(r string) bool {
	if strings.Contains("/"+r+"/", "/../") {
		return false
	}
	return strings.HasPrefix(r, ".bflow/evals/")
}

func isFrozenPhase(c Context) bool {
	return len(c.Frozen) > 0 && FrozenPhase(c.Phase)
}

// rel normaliza una ruta a relativa a la raíz con barras normales.
func rel(root, p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	r := strings.ReplaceAll(root, `\`, "/")
	if r != "" && strings.HasPrefix(strings.ToLower(p), strings.ToLower(strings.TrimSuffix(r, "/"))+"/") {
		p = p[len(strings.TrimSuffix(r, "/"))+1:]
	}
	return strings.TrimPrefix(p, "./")
}

func underAny(p string, dirs []string) bool {
	for _, d := range dirs {
		d = strings.Trim(strings.ReplaceAll(d, `\`, "/"), "/")
		if p == d || strings.HasPrefix(p, d+"/") {
			return true
		}
	}
	return false
}

// MatchesTest dice si una ruta es de prueba según los patrones del stack.
func MatchesTest(patterns []string, p string) bool {
	p = strings.ReplaceAll(p, `\`, "/")
	for _, pat := range patterns {
		if strings.HasSuffix(pat, "/**") {
			dir := strings.TrimSuffix(pat, "/**")
			if strings.HasPrefix(p, dir+"/") || strings.Contains(p, "/"+dir+"/") {
				return true
			}
			continue
		}
		if ok, _ := filepath.Match(pat, filepath.Base(p)); ok {
			return true
		}
	}
	return false
}

// quotedFields parte s por espacios fuera de comillas. Un tramo "…" o '…' queda
// dentro de un token (con sus comillas); una comilla sin cerrar llega al final.
// `\` no es especial (en Windows separa rutas). Cada token es un substring de s.
func quotedFields(s string) []string {
	var out []string
	start := -1
	var q rune
	for i, r := range s {
		switch {
		case q != 0:
			if r == q {
				q = 0
			}
		case r == '"' || r == '\'':
			if start < 0 {
				start = i
			}
			q = r
		case unicode.IsSpace(r):
			if start >= 0 {
				out = append(out, s[start:i])
				start = -1
			}
		default:
			if start < 0 {
				start = i
			}
		}
	}
	if start >= 0 {
		out = append(out, s[start:])
	}
	return out
}

// gitGlobalWithArg son las opciones globales de git que llevan un valor aparte.
var gitGlobalWithArg = []string{"-c", "-C", "--git-dir", "--work-tree", "--namespace", "--exec-path", "--super-prefix", "--config-env"}

// stripGitGlobals quita las opciones globales de git (-c k=v, -C dir,
// --git-dir, --work-tree...) para que las reglas vean el subcomando. Si el
// segmento no es git, lo devuelve igual.
func stripGitGlobals(seg string) string {
	f := quotedFields(seg)
	i := 0
	for i < len(f) && strings.Contains(f[i], "=") && !strings.HasPrefix(f[i], "-") {
		i++
	}
	if i >= len(f) || exeName(f[i]) != "git" {
		return seg
	}
	out := append([]string{}, f[:i+1]...)
	j := i + 1
	for j < len(f) && strings.HasPrefix(f[j], "-") {
		name, _, hasVal := strings.Cut(f[j], "=")
		switch {
		case slices.Contains(gitGlobalWithArg, f[j]):
			j += 2
		case hasVal && slices.Contains(gitGlobalWithArg, name),
			strings.HasPrefix(f[j], "-c") && len(f[j]) > 2 && f[j][1] == 'c',
			strings.HasPrefix(f[j], "-C") && len(f[j]) > 2:
			j++
		case strings.HasPrefix(f[j], "--") || len(f[j]) == 2:
			j++ // --no-pager, --bare, -p...
		default:
			return seg
		}
	}
	if j > len(f) {
		j = len(f)
	}
	return strings.Join(append(out, f[j:]...), " ")
}

// plusRefspec dice si algún refspec de push empieza con + (push forzado).
func plusRefspec(args string) bool {
	for _, f := range strings.Fields(args) {
		if strings.HasPrefix(strings.Trim(f, `"'`), "+") {
			return true
		}
	}
	return false
}

// protectedWrite trata como escritura a .env*, .bflow/ las redirecciones, tee,
// rm, mv y cp de Bash, con la misma regla que Edit/Write.
func protectedWrite(s string, c Context) Decision {
	raw := quotedFields(s)
	var targets []string
	for i, w := range raw {
		k := strings.Index(w, ">")
		if k < 0 || strings.HasPrefix(w, `"`) || strings.HasPrefix(w, "'") {
			continue
		}
		dst := strings.Trim(strings.TrimLeft(w[k:], ">|"), `"'`)
		if dst == "" && i+1 < len(raw) {
			dst = strings.Trim(raw[i+1], `"'`)
		}
		if dst != "" && !strings.HasPrefix(dst, "&") {
			targets = append(targets, dst)
		}
	}
	if t := skipPrefix(slices.Clone(raw)); len(t) > 0 {
		name, args := cmdName(t[0]), t[1:]
		if name == "git" && len(args) > 0 && (args[0] == "rm" || args[0] == "mv") {
			name, args = args[0], args[1:]
		}
		nf := nonFlags(args)
		switch name {
		case "tee", "rm", "del", "mv":
			targets = append(targets, nf...)
		case "cp", "install":
			if len(nf) > 0 {
				targets = append(targets, nf[len(nf)-1])
			}
		}
	}
	for _, t := range targets {
		p := rel(c.Root, t)
		if b := filepath.Base(p); strings.HasPrefix(b, ".env") && strings.ContainsAny(b, "*?[") {
			return deny("env_file", "%s puede tener secretos: no se edita desde un agente.", p)
		}
		if d := edit(Action{Tool: Write, Path: t, Subagent: true}, Context{Root: c.Root}); !d.Allow {
			return d
		}
	}
	return allow
}
