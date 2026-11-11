package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"innobytes.tech/bflow/internal/i18n"
	"innobytes.tech/bflow/internal/store"
)

// Section es una sección fija del spec.
type Section struct {
	Key     string // clave para --section
	Heading string // encabezado ## exacto
	Hint    string
	UIOnly  bool
}

// Sections son los encabezados fijos de specs/<ID>-<slug>/spec.md, en orden.
var Sections = []Section{
	{"brief", "Brief", "Objetivo en una frase · entra / no entra · decisiones nuevas [N] con la alternativa descartada · riesgos · tamaño. ≤35 líneas: es lo único que el humano lee para aprobar.", false},
	{"discovery", "Discovery", "Discovery completo; lo escribe bflow al cerrar el gate discovery. No lo edites.", false},
	{"requirements", "Requirements", "Criterios EARS numerados R1..Rn, cada uno [D] (del discovery) o [N] (nuevo).", false},
	{"design", "Design", "Estructuras, firmas, decisiones tomadas y descartadas, superficie de seguridad.", false},
	{"tasks", "Tasks", "Checklist T1..Tn; T1 es el contrato. Cada tarea cita sus R.", false},
	{"ui-blueprint", "UI blueprint", "Pantallas hermanas de referencia, flujo, layout, estados, tokens, responsive. ≤60 líneas.", true},
}

func (e *Engine) ui() bool { return e.Cfg.Flow.UI != nil && *e.Cfg.Flow.UI }

func (e *Engine) sectionHint(key string) string {
	return e.loc().T("engine.spec.hint_" + strings.ReplaceAll(key, "-", "_"))
}

func (e *Engine) ensureSpec(rec store.Record) error {
	path := e.specAbs(rec.Flow)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	title := rec.Title
	if title == "" {
		title = rec.Flow.Slug
	}
	fmt.Fprintf(&b, "# %s · %s\n", rec.Flow.ID, title)
	for _, s := range Sections {
		if s.UIOnly && !e.ui() {
			continue
		}
		hint := e.sectionHint(s.Key)
		fmt.Fprintf(&b, "\n## %s\n\n<!-- %s -->\n", s.Heading, hint)
	}
	return store.WriteAtomic(path, []byte(b.String()))
}

// Artifacts que show sabe mostrar además de las secciones del spec.
var artifacts = map[string]string{
	"contract":   "contract.md",
	"review-map": "reports/review-map.md",
	"decisions":  "decisions.md",
	"check":      "check.md",
	"scout":      "reports/scout.md",
}

// Show devuelve un artefacto: una sección del spec (brief, spec --section X) o
// un archivo de trabajo (contract, review-map, decisions, check).
func (e *Engine) Show(ctx context.Context, id, what, section string) (string, error) {
	switch what {
	case "task":
		return e.showTask(ctx, id)
	case "questions":
		return e.productQuestions(id)
	}
	rec, err := e.Store.Load(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", fmt.Errorf("%s", e.loc().T("engine.spec.not_started", id))
		}
		return "", err
	}
	switch what {
	case "brief":
		return e.specSection(rec, "brief")
	case "spec":
		if section == "" {
			b, err := os.ReadFile(e.specAbs(rec.Flow))
			return string(b), err
		}
		return e.specSection(rec, section)
	}
	if what == "discovery" {
		if body, err := e.specSection(rec, "discovery"); err == nil && body != "" {
			return body, nil
		}
		b, err := e.Store.ReadFile(id, "discovery.md")
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%s", e.loc().T("engine.spec.no_discovery", id, id))
		}
		return string(b), err
	}
	rel, ok := artifacts[what]
	if !ok {
		return "", fmt.Errorf("%s", e.loc().T("engine.spec.unknown_artifact", what))
	}
	b, err := e.Store.ReadFile(id, rel)
	if errors.Is(err, os.ErrNotExist) && what == "scout" {
		return e.loc().T("engine.spec.scout_empty_report", id), nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("%s", e.loc().T("engine.spec.no_artifact", id, what, ".bflow/tasks/"+id, rel))
	}
	return string(b), err
}

// showTask devuelve la tarea del tracker envuelta como contenido externo: la
// escribieron personas ajenas al flujo (clientes, otros equipos) y el agente
// debe tratarla como datos, no como instrucciones.
func (e *Engine) showTask(ctx context.Context, id string) (string, error) {
	t, err := e.Tracker.Get(ctx, id)
	if err != nil {
		return "", e.trackerErr(id, err)
	}
	body := "# " + t.Title
	if d := strings.TrimSpace(t.Description); d != "" {
		body += "\n\n" + d
	}
	body = strings.ReplaceAll(body, "</pasted_content", "<\\/pasted_content")
	return fmt.Sprintf("<pasted_content id=\"tracker:%s\">\n%s\n</pasted_content>", id, body), nil
}

func (e *Engine) specSection(rec store.Record, key string) (string, error) {
	var sec *Section
	var keys []string
	for i := range Sections {
		if Sections[i].UIOnly && !e.ui() {
			continue
		}
		keys = append(keys, Sections[i].Key)
		if Sections[i].Key == key {
			sec = &Sections[i]
		}
	}
	if sec == nil {
		return "", fmt.Errorf("%s", e.loc().T("engine.spec.section_not_found", key, strings.Join(keys, ", ")))
	}
	b, err := os.ReadFile(e.specAbs(rec.Flow))
	if err != nil {
		return "", fmt.Errorf("spec: %w", err)
	}
	return cutSection(string(b), sec.Heading, e.loc())
}

// fillSection reemplaza el cuerpo de la sección key del spec (la crea si falta).
func (e *Engine) fillSection(rec store.Record, key, body string) error {
	var heading string
	for _, s := range Sections {
		if s.Key == key {
			heading = s.Heading
		}
	}
	if heading == "" {
		return fmt.Errorf("%s", e.loc().T("engine.spec.section_unknown", key))
	}
	if err := e.ensureSpec(rec); err != nil {
		return err
	}
	path := e.specAbs(rec.Flow)
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	start := -1
	for i, l := range lines {
		if strings.EqualFold(strings.TrimSpace(l), "## "+heading) {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return fmt.Errorf("%s", e.loc().T("engine.spec.missing_heading", heading))
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			end = i
			break
		}
	}
	out := append([]string{}, lines[:start]...)
	out = append(out, "", strings.TrimSpace(body))
	if end < len(lines) {
		out = append(out, "")
	}
	out = append(out, lines[end:]...)
	return store.WriteAtomic(path, []byte(strings.Join(out, "\n")))
}

var commentRe = regexp.MustCompile(`(?s)<!--.*?-->`)

// cutSection devuelve el cuerpo bajo "## heading" hasta el siguiente "## ".
func cutSection(doc, heading string, locs ...*i18n.Localizer) (string, error) {
	lines := strings.Split(strings.ReplaceAll(doc, "\r\n", "\n"), "\n")
	start := -1
	for i, l := range lines {
		if strings.EqualFold(strings.TrimSpace(l), "## "+heading) {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return "", fmt.Errorf("%s", locOf(locs...).T("engine.spec.missing_heading", heading))
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			end = i
			break
		}
	}
	body := commentRe.ReplaceAllString(strings.Join(lines[start:end], "\n"), "")
	return strings.TrimSpace(body), nil
}

// Slug convierte un título en un identificador de rama y carpeta: minúsculas,
// sin acentos, guiones, máximo 48 caracteres cortando en palabra.
func Slug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if f, ok := fold[r]; ok {
			r = f
		}
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 48 {
		s = s[:48]
		if i := strings.LastIndex(s, "-"); i > 0 {
			s = s[:i]
		}
	}
	return s
}

// fold translitera las letras acentuadas más comunes (español, portugués,
// francés, alemán) sin depender de golang.org/x/text.
var fold = func() map[rune]rune {
	m := map[rune]rune{}
	for base, variants := range map[rune]string{
		'a': "áàâäãåā", 'e': "éèêëē", 'i': "íìîïī", 'o': "óòôöõøō", 'u': "úùûüū",
		'n': "ñ", 'c': "ç", 'y': "ýÿ",
	} {
		for _, v := range variants {
			m[v] = base
		}
	}
	return m
}()
