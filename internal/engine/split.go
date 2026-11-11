package engine

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"innobytes.tech/bflow/internal/flow"
	"innobytes.tech/bflow/internal/i18n"
	"innobytes.tech/bflow/internal/store"
	"innobytes.tech/bflow/internal/tracker"
)

// SplitPart es una hija propuesta en la `### División` del Brief.
type SplitPart struct{ Title, Scope string }

const (
	maxSplitParts = 6
	maxSplitTitle = 120
)

var splitBullet = regexp.MustCompile(`^- \*\*(.*?)\*\*:(.*)$`)

func splitInvalid(loc *i18n.Localizer, key string, a ...any) error {
	l := locOf(loc)
	detail := l.T("engine.split."+key, a...)
	return &flow.Rejection{Code: "split_invalid", Reason: l.T("engine.split.invalid_wrapper", detail)}
}

func isDivisionHeading(line string) bool {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "###") || strings.HasPrefix(t, "####") {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(strings.TrimLeft(t, "#")))
	return name == "división" || name == "division"
}

// ParseSplit lee la `### División` del Brief. Cualquier violación de las
// reglas (2 a 6 hijas, título y alcance no vacíos, título único y de hasta
// 120 runas) devuelve *flow.Rejection con código split_invalid.
func ParseSplit(brief string, locs ...*i18n.Localizer) ([]SplitPart, error) {
	loc := locOf(locs...)
	lines := strings.Split(strings.ReplaceAll(brief, "\r\n", "\n"), "\n")
	start := -1
	for i, l := range lines {
		if isDivisionHeading(l) {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return nil, splitInvalid(loc, "missing_section")
	}
	var parts []SplitPart
	seen := map[string]bool{}
	var scope []string
	flush := func(line int) error {
		if len(parts) == 0 {
			return nil
		}
		p := &parts[len(parts)-1]
		p.Scope = strings.Join(scope, "\n")
		if strings.TrimSpace(p.Scope) == "" {
			return splitInvalid(loc, "child_no_scope", p.Title, line)
		}
		return nil
	}
	for i := start; i < len(lines); i++ {
		l := strings.TrimRight(lines[i], " \t")
		n := i + 1
		if strings.HasPrefix(strings.TrimSpace(l), "#") && !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "\t") {
			break
		}
		if strings.TrimSpace(l) == "" {
			continue
		}
		if l[0] == ' ' || l[0] == '\t' {
			if len(parts) == 0 {
				return nil, splitInvalid(loc, "indent_before_first_bullet", n)
			}
			scope = append(scope, strings.TrimSpace(l))
			continue
		}
		if !strings.HasPrefix(l, "-") {
			break // un párrafo sin sangría cierra la lista (p. ej. **Riesgos** después)
		}
		m := splitBullet.FindStringSubmatch(l)
		if m == nil {
			return nil, splitInvalid(loc, "malformed_bullet", n)
		}
		if err := flush(n); err != nil {
			return nil, err
		}
		title := strings.TrimSpace(m[1])
		switch {
		case title == "":
			return nil, splitInvalid(loc, "bullet_no_title", n)
		case utf8.RuneCountInString(title) > maxSplitTitle:
			return nil, splitInvalid(loc, "title_too_long", title, n, maxSplitTitle)
		case seen[title]:
			return nil, splitInvalid(loc, "title_duplicate", title, n)
		}
		seen[title] = true
		parts = append(parts, SplitPart{Title: title})
		scope = scope[:0]
		if s := strings.TrimSpace(m[2]); s != "" {
			scope = append(scope, s)
		}
	}
	if err := flush(len(lines)); err != nil {
		return nil, err
	}
	if len(parts) < 2 || len(parts) > maxSplitParts {
		return nil, splitInvalid(loc, "child_count_invalid", len(parts), maxSplitParts)
	}
	return parts, nil
}

// createChildren crea con el tracker las hijas del split que faltan, guarda
// cada una en cuanto existe y devuelve las líneas "<ID> · <título>" en el
// orden del Brief. Si falla una, el error lista lo ya creado y cómo reintentar.
func (e *Engine) createChildren(ctx context.Context, id string) ([]string, error) {
	cr, ok := e.Tracker.(tracker.Creator)
	if !ok {
		return nil, &flow.Rejection{Code: "no_creator", Reason: e.loc().T("engine.split.no_creator", e.Tracker.Name(), id)}
	}
	rec, err := e.Store.Load(id)
	if err != nil {
		return nil, err
	}
	brief, err := e.specSection(rec, "brief")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", e.loc().T("engine.split.brief_load_failed", id), err)
	}
	parts, err := ParseSplit(brief, e.loc())
	if err != nil {
		return nil, err
	}
	ids := map[string]string{}
	var order []string // títulos en el orden en que se crearon, para el mensaje de error
	for _, c := range rec.Split {
		ids[c.Title] = c.ID
		order = append(order, c.Title)
	}
	for _, p := range parts {
		if _, done := ids[p.Title]; done {
			continue
		}
		task, err := cr.Create(ctx, p.Title, p.Scope+"\n\nParte de "+id+" · "+rec.Title)
		if err != nil {
			return nil, fmt.Errorf("%s: %w; %s",
				e.loc().T("engine.split.child_create_failed", p.Title, id), err,
				e.loc().T("engine.split.child_create_retry_hint", createdList(ids, order, e.loc()), id))
		}
		ids[p.Title] = task.ID
		order = append(order, p.Title)
		if _, err := e.Store.Update(id, func(r *store.Record, _ bool) error {
			r.Split = append(r.Split, store.SplitChild{Title: p.Title, ID: task.ID})
			return nil
		}); err != nil {
			return nil, fmt.Errorf("%s: %w; %s",
				e.loc().T("engine.split.child_save_failed", p.Title, task.ID), err,
				e.loc().T("engine.split.children_already_created", createdList(ids, order, e.loc())))
		}
	}
	lines := make([]string, 0, len(parts))
	for _, p := range parts {
		lines = append(lines, ids[p.Title]+" · "+p.Title)
	}
	return lines, nil
}

func createdList(ids map[string]string, order []string, locs ...*i18n.Localizer) string {
	if len(order) == 0 {
		return locOf(locs...).T("engine.split.created_list_none")
	}
	out := make([]string, 0, len(order))
	for _, t := range order {
		out = append(out, ids[t]+" · "+t)
	}
	return strings.Join(out, ", ")
}
