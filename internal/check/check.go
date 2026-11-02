// Package check es la compuerta determinista (port de gate.mjs): corre los
// pasos de bflow.yaml, resume los fallos en pocas líneas y deja un resultado
// ligado al commit para que --verify sepa si sigue valiendo.
package check

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"innobytes.tech/bflow/internal/config"
	"innobytes.tech/bflow/internal/i18n"
)

// GitReader es lo que check necesita de git.
type GitReader interface {
	HeadSHA(ctx context.Context) (string, error)
	Dirty(ctx context.Context, paths []string) ([]string, error)
	DiffNames(ctx context.Context, base string) ([]string, error)
	ChangedSince(ctx context.Context, sha string, paths []string) ([]string, error)
}

// ExecFunc corre argv directo, o shell si argv es nil. Devuelve la salida
// combinada y el código de salida.
type ExecFunc func(ctx context.Context, dir string, argv []string, shell string) (string, int, error)

// Runner corre los pasos.
type Runner struct {
	Root     string
	Base     string // referencia base para diffs y {base}: origin/dev
	Cfg      config.Check
	Git      GitReader
	Exec     ExecFunc
	LookPath func(string) (string, error)
	CGO      func() bool
	Now      func() time.Time
	Packages func(ctx context.Context) ([]GoPackage, error)
	// TestPatterns sirve para {affected_tests}.
	TestPatterns []string

	pkgs     []GoPackage
	pkgsErr  error
	pkgsDone bool
}

// StepResult es el resultado de un paso.
type StepResult struct {
	Name   string   `json:"name"`
	Cmd    string   `json:"cmd,omitempty"`
	Status string   `json:"status"` // pass | fail | skip
	Exit   int      `json:"exit"`
	Secs   float64  `json:"secs"`
	Tail   string   `json:"tail,omitempty"`
	Detail string   `json:"detail,omitempty"` // p. ej. "5 paq."
	Fails  []string `json:"fails,omitempty"`  // líneas de fallo sin repetir (lo que lee el agente)
	// Preexisting: vulnerabilidades en dependencias que la tarea no tocó. No
	// las arregla el agente por su cuenta: decide una persona.
	Preexisting bool   `json:"preexisting,omitempty"`
	Accept      string `json:"accept_file,omitempty"`
}

// Result es una corrida de check.
type Result struct {
	SHA      string       `json:"sha"` // DIRTY si había cambios sin commitear en code_paths
	Date     time.Time    `json:"date"`
	Result   string       `json:"result"` // PASS | FAIL
	Degraded []string     `json:"degraded,omitempty"`
	Warnings []string     `json:"warnings,omitempty"` // pasos que pasaron sospechosamente rápido
	Steps    []StepResult `json:"steps"`
}

// DefaultFailPattern son las líneas que resumen un fallo de Go (gate.mjs).
const DefaultFailPattern = `^\s*(--- FAIL|FAIL\b|panic:|\S+_test\.go:\d+:)`

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Run corre todos los pasos.
func (r *Runner) Run(ctx context.Context) (Result, error) {
	res := Result{Date: r.now(), Result: "PASS"}
	sha, err := r.Git.HeadSHA(ctx)
	if err != nil {
		return res, err
	}
	dirty, err := r.Git.Dirty(ctx, r.Cfg.CodePaths)
	if err != nil {
		return res, err
	}
	res.SHA = sha
	if len(dirty) > 0 {
		res.SHA = "DIRTY"
	}
	for _, st := range r.Cfg.Steps {
		sr := r.step(ctx, st, &res)
		if sr.Status == "fail" {
			res.Result = "FAIL"
		}
		res.Steps = append(res.Steps, sr)
	}
	return res, nil
}

// Quick corre los pasos rápidos sobre un paquete, sin registrar resultado.
func (r *Runner) Quick(ctx context.Context, pkg string) (Result, error) {
	if pkg == "" {
		pkg = "./..."
	}
	res := Result{Date: r.now(), Result: "PASS"}
	for i, line := range r.Cfg.Quick {
		sr := r.step(ctx, config.Step{Name: fmt.Sprintf("quick %d", i+1), Run: strings.ReplaceAll(line, "{pkg}", pkg)}, &res)
		if sr.Status == "fail" {
			res.Result = "FAIL"
		}
		res.Steps = append(res.Steps, sr)
	}
	return res, nil
}

func (r *Runner) degrade(res *Result, msg string) {
	if !slices.Contains(res.Degraded, msg) {
		res.Degraded = append(res.Degraded, msg)
	}
}

func (r *Runner) step(ctx context.Context, st config.Step, res *Result) StepResult {
	sr := StepResult{Name: st.Name, Cmd: st.Run}
	if st.Needs != "" && r.LookPath != nil {
		if _, err := r.LookPath(st.Needs); err != nil {
			if st.Optional {
				r.degrade(res, fmt.Sprintf("%s no instalado: %s omitido", st.Needs, st.Name))
				sr.Status = "skip"
				return sr
			}
			sr.Status, sr.Exit, sr.Tail = "fail", 127, fmt.Sprintf("falta la herramienta %s (paso obligatorio)", st.Needs)
			return sr
		}
	}
	argv, shell, skip, n, err := r.expand(ctx, st.Run, res)
	if n > 0 {
		sr.Detail = fmt.Sprintf("%d paq.", n)
	}
	if err != nil {
		sr.Status, sr.Exit, sr.Tail = "fail", 1, err.Error()
		return sr
	}
	if skip != "" {
		sr.Status, sr.Tail = "skip", skip
		return sr
	}
	t0 := time.Now()
	out, exit, err := r.Exec(ctx, r.Root, argv, shell)
	sr.Secs = float64(time.Since(t0).Round(100*time.Millisecond)) / float64(time.Second)
	if err != nil && exit == 0 {
		exit = 1
		out += "\n" + err.Error()
	}
	sr.Exit = exit
	sr.Status = "pass"
	if exit == 0 {
		return sr
	}
	sr.Status = "fail"
	if st.Accept != "" {
		if ok, msg := r.acceptVulns(st.Accept, out); ok {
			sr.Status = "pass"
			r.degrade(res, msg)
			return sr
		} else if msg != "" {
			out = msg + "\n" + out
		}
	}
	if isVulnStep(st) && !r.depsTouched(ctx) {
		sr.Preexisting, sr.Accept = true, st.Accept
	}
	if st.Baseline == "new-only" {
		mine := r.onlyTouched(ctx, out)
		if mine == "" {
			sr.Status = "pass"
			sr.Tail = "hallazgos solo en archivos que esta tarea no tocó (baseline)"
			return sr
		}
		out = mine
	}
	sr.Tail, sr.Fails = r.tail(out)
	return sr
}

var shellMeta = regexp.MustCompile(`[|&;<>()$` + "`" + `]`)

// expand reemplaza los marcadores. Sin operadores de shell el comando se corre
// directo (sin límite de longitud de cmd.exe para listas de paquetes largas).
func (r *Runner) expand(ctx context.Context, line string, res *Result) (argv []string, shell, skip string, npkgs int, err error) {
	vals := map[string][]string{}
	if strings.Contains(line, "{race}") {
		if r.CGO != nil && r.CGO() {
			vals["{race}"] = []string{"-race"}
		} else {
			vals["{race}"] = nil
			r.degrade(res, "sin cgo: -race omitido (CGO_ENABLED=0)")
		}
	}
	vals["{base}"] = []string{r.Base}
	vals["{devnull}"] = []string{os.DevNull}
	for _, k := range []string{"{pkgs_db}", "{pkgs_nodb}"} {
		if !strings.Contains(line, k) {
			continue
		}
		pkgs, err := r.packages(ctx)
		if err != nil {
			return nil, "", "", 0, err
		}
		var sel []string
		for _, p := range pkgs {
			if p.DB == (k == "{pkgs_db}") {
				sel = append(sel, p.ImportPath)
			}
		}
		if len(sel) == 0 {
			return nil, "", "sin paquetes para " + k, 0, nil
		}
		vals[k] = sel
		npkgs = len(sel)
	}
	for _, k := range []string{"{affected}", "{affected_tests}"} {
		if !strings.Contains(line, k) {
			continue
		}
		files, err := r.Git.DiffNames(ctx, r.Base)
		if err != nil {
			return nil, "", "", 0, err
		}
		if k == "{affected_tests}" {
			files = slices.DeleteFunc(files, func(f string) bool { return !matchesAny(r.TestPatterns, f) })
		}
		if len(files) == 0 {
			return nil, "", "sin archivos para " + k, 0, nil
		}
		vals[k] = files
	}
	if shellMeta.MatchString(line) {
		for k, v := range vals {
			line = strings.ReplaceAll(line, k, strings.Join(v, " "))
		}
		return nil, line, "", npkgs, nil
	}
	for _, f := range splitArgs(line) {
		replaced := false
		for k, v := range vals {
			if f == k {
				argv = append(argv, v...)
				replaced = true
				break
			}
			if strings.Contains(f, k) {
				f = strings.ReplaceAll(f, k, strings.Join(v, " "))
			}
		}
		if !replaced && f != "" {
			argv = append(argv, f)
		}
	}
	return argv, "", "", npkgs, nil
}

func (r *Runner) packages(ctx context.Context) ([]GoPackage, error) {
	if !r.pkgsDone {
		r.pkgsDone = true
		if r.Packages == nil {
			r.pkgsErr = errors.New("este stack no sabe listar paquetes ({pkgs_db}/{pkgs_nodb} son de Go)")
		} else {
			r.pkgs, r.pkgsErr = r.Packages(ctx)
		}
	}
	return r.pkgs, r.pkgsErr
}

// tail resume la salida: primero las líneas de fallo (hasta 60), luego las
// últimas 25, como gate.mjs.
func (r *Runner) tail(out string) (string, []string) {
	pat := r.Cfg.FailPattern
	if pat == "" {
		pat = DefaultFailPattern
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		re = regexp.MustCompile(DefaultFailPattern)
	}
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(out, "\r\n", "\n"), "\n"), "\n")
	var fails, uniq []string
	seen := map[string]bool{}
	for _, l := range lines {
		if re.MatchString(l) {
			fails = append(fails, l)
			if k := strings.TrimSpace(l); !seen[k] && k != "FAIL" {
				seen[k] = true
				uniq = append(uniq, k)
			}
			if len(fails) == 60 {
				break
			}
		}
	}
	last := lines[max(0, len(lines)-25):]
	if len(fails) > 0 {
		return strings.Join(append(append(fails, "…"), last...), "\n"), uniq
	}
	return strings.Join(last, "\n"), nil
}

var vulnToolRe = regexp.MustCompile(`\b(govulncheck|npm audit|pnpm audit|yarn audit|pip-audit|osv-scanner)\b|dotnet list .*--vulnerable`)

// isVulnStep: el paso busca vulnerabilidades en dependencias.
func isVulnStep(st config.Step) bool {
	return st.Accept != "" || vulnToolRe.MatchString(st.Run)
}

// depManifests son los archivos que fijan versiones de dependencias o del
// toolchain. Si la tarea no tocó ninguno, sus vulnerabilidades ya estaban.
var depManifests = []string{"go.mod", "go.sum", "package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock",
	"pyproject.toml", "poetry.lock", "Pipfile.lock", "pom.xml", "build.gradle", "build.gradle.kts",
	"packages.lock.json", "Directory.Packages.props"}

func (r *Runner) depsTouched(ctx context.Context) bool {
	files, _ := r.Git.DiffNames(ctx, r.Base)
	dirty, _ := r.Git.Dirty(ctx, nil)
	for _, f := range append(files, dirty...) {
		base := filepath.Base(strings.ReplaceAll(f, `\`, "/"))
		if slices.Contains(depManifests, base) || strings.HasSuffix(base, ".csproj") || strings.HasPrefix(base, "requirements") {
			return true
		}
	}
	return false
}

// PreexistingHint le dice al agente qué hacer con un paso de vulnerabilidades
// que falla por dependencias que la tarea no tocó.
func (s StepResult) PreexistingHint() string {
	accept := "`" + s.Accept + "`"
	if s.Accept == "" {
		accept = "un `accept_file` del paso (JSON: {\"accepted\": [{\"id\", \"revisar\": \"AAAA-MM-DD\", \"seguimiento\"}]})"
	}
	return "vulnerabilidades en dependencias que esta tarea no tocó: no las arregles por tu cuenta. Reporta NEEDS_DECISION con dos opciones: " +
		"que una persona las acepte en " + accept + " con fecha de revisión y se abra otra tarea, o actualizar la dependencia en esta tarea."
}

func (r *Runner) onlyTouched(ctx context.Context, out string) string {
	files, _ := r.Git.DiffNames(ctx, r.Base)
	dirty, _ := r.Git.Dirty(ctx, nil)
	files = append(files, dirty...)
	var keep []string
	for _, l := range strings.Split(out, "\n") {
		norm := strings.ReplaceAll(l, `\`, "/")
		for _, f := range files {
			if f != "" && strings.Contains(norm, f) {
				keep = append(keep, l)
				break
			}
		}
	}
	return strings.Join(keep, "\n")
}

var vulnRe = regexp.MustCompile(`(?m)^Vulnerability #\d+: (GO-[\w-]+)`)

// acceptVulns aplica el archivo de vulnerabilidades aceptadas: el paso pasa
// solo si todas las encontradas están aceptadas y su revisión no venció.
func (r *Runner) acceptVulns(file, out string) (bool, string) {
	found := vulnRe.FindAllStringSubmatch(out, -1)
	if len(found) == 0 {
		return false, ""
	}
	b, err := os.ReadFile(filepath.Join(r.Root, filepath.FromSlash(file)))
	if err != nil {
		return false, ""
	}
	var f struct {
		Accepted []struct {
			ID          string `json:"id"`
			Revisar     string `json:"revisar"`
			Seguimiento string `json:"seguimiento"`
		} `json:"accepted"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return false, file + " no es JSON válido: " + err.Error()
	}
	today := r.now().Format("2006-01-02")
	var missing, expired, detail []string
	for _, m := range found {
		id := m[1]
		i := slices.IndexFunc(f.Accepted, func(a struct {
			ID          string `json:"id"`
			Revisar     string `json:"revisar"`
			Seguimiento string `json:"seguimiento"`
		}) bool {
			return a.ID == id
		})
		switch {
		case i < 0:
			missing = append(missing, id)
		case f.Accepted[i].Revisar <= today:
			expired = append(expired, id)
		default:
			s := f.Accepted[i].Seguimiento
			if s == "" {
				s = "sin issue"
			}
			detail = append(detail, fmt.Sprintf("%s (revisar %s, %s)", id, f.Accepted[i].Revisar, s))
		}
	}
	if len(missing)+len(expired) > 0 {
		var why []string
		if len(missing) > 0 {
			why = append(why, "sin aceptar: "+strings.Join(missing, ", "))
		}
		if len(expired) > 0 {
			why = append(why, "aceptación vencida (toca revisarlas): "+strings.Join(expired, ", "))
		}
		return false, strings.Join(why, " · ")
	}
	return true, "vulnerabilidades aceptadas por el humano: " + strings.Join(detail, ", ")
}

// Verify dice si el resultado sigue valiendo para el código actual.
func Verify(ctx context.Context, g GitReader, codePaths []string, res *Result) (bool, string) {
	switch {
	case res == nil:
		return false, "no hay check registrado (bflow check)"
	case res.SHA == "DIRTY":
		return false, "el check corrió con código sin commitear: commitea y vuelve a correr bflow check"
	case res.Result != "PASS":
		return false, "el último check no pasó"
	}
	return Fresh(ctx, g, codePaths, res)
}

// Markdown es el reporte legible (bflow show check).
func (res Result) Markdown() string {
	var b strings.Builder
	result := res.Result
	if len(res.Degraded) > 0 {
		result += " (DEGRADADO)"
	}
	fmt.Fprintf(&b, "# Check\nsha: %s\ndate: %s\nresult: %s\n\n| paso | estado | s |\n|:--|:--|:--|\n", res.SHA, res.Date.Format(time.RFC3339), result)
	for _, s := range res.Steps {
		name := s.Name
		if s.Detail != "" {
			name += " (" + s.Detail + ")"
		}
		fmt.Fprintf(&b, "| %s | %s | %.1f |\n", name, s.Status, s.Secs)
	}
	if len(res.Degraded) > 0 {
		b.WriteString("\n## ⚠️ Degradado (no verificado en local)\n")
		for _, d := range res.Degraded {
			b.WriteString("- " + d + "\n")
		}
	}
	if len(res.Warnings) > 0 {
		b.WriteString("\n## ⚠️ Avisos\n")
		for _, w := range res.Warnings {
			b.WriteString("- " + w + "\n")
		}
	}
	for _, s := range res.Steps {
		if s.Status == "fail" {
			fmt.Fprintf(&b, "\n## ❌ %s\n`%s`\n", s.Name, s.Cmd)
			if s.Preexisting {
				b.WriteString("\n" + s.PreexistingHint() + "\n")
			}
			fmt.Fprintf(&b, "```\n%s\n```\n", s.Tail)
		}
	}
	return b.String()
}

// SummaryLoc es una línea para el agente localizada; el detalle queda en check.md.
func (res Result) SummaryLoc(loc *i18n.Localizer) string {
	if loc == nil {
		loc = i18n.DefaultLocalizer()
	}
	var failed, skipped []string
	for _, s := range res.Steps {
		switch s.Status {
		case "fail":
			if s.Preexisting {
				failed = append(failed, s.Name+" (preexistente)")
			} else {
				failed = append(failed, s.Name)
			}
		case "skip":
			skipped = append(skipped, s.Name)
		}
	}
	line := loc.T("check.summary_steps", res.Result, len(res.Steps))
	if len(failed) > 0 {
		line += loc.T("check.failed_prefix", strings.Join(failed, ", "))
	}
	if len(res.Degraded) > 0 {
		line += loc.T("check.degraded", len(res.Degraded))
	}
	if len(res.Warnings) > 0 {
		line += loc.T("check.warnings", len(res.Warnings))
	}
	return line
}

// Summary es una línea para el agente; el detalle queda en check.md.
func (res Result) Summary() string {
	return res.SummaryLoc(nil)
}

func matchesAny(patterns []string, file string) bool {
	base := filepath.Base(file)
	for _, p := range patterns {
		if strings.HasSuffix(p, "/**") {
			dir := strings.TrimSuffix(p, "/**")
			if strings.HasPrefix(file, dir+"/") || strings.Contains(file, "/"+dir+"/") {
				return true
			}
			continue
		}
		if ok, _ := filepath.Match(p, base); ok {
			return true
		}
	}
	return false
}

// splitArgs parte una línea respetando comillas simples y dobles.
func splitArgs(s string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	has := false
	for _, r := range s {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote == 0 && (r == '"' || r == '\''):
			quote, has = r, true
		case quote == 0 && (r == ' ' || r == '\t'):
			if cur.Len() > 0 || has {
				out = append(out, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 || has {
		out = append(out, cur.String())
	}
	return out
}

// Fresh dice si el check guardado sigue vigente sobre el código actual, sin
// mirar si pasó o falló.
func Fresh(ctx context.Context, g GitReader, codePaths []string, res *Result) (bool, string) {
	switch {
	case res == nil:
		return false, "no hay check registrado (bflow check)"
	case res.SHA == "DIRTY":
		return false, "el check corrió con código sin commitear: commitea y vuelve a correr bflow check"
	}
	if dirty, err := g.Dirty(ctx, codePaths); err != nil {
		return false, err.Error()
	} else if len(dirty) > 0 {
		return false, "hay código sin commitear posterior al check: " + strings.Join(dirty, ", ")
	}
	changed, err := g.ChangedSince(ctx, res.SHA, codePaths)
	if err != nil {
		return false, err.Error()
	}
	if len(changed) > 0 {
		return false, "hay cambios de código posteriores al check: " + strings.Join(changed, ", ")
	}
	return true, "check " + res.SHA[:min(8, len(res.SHA))] + " vigente"
}
