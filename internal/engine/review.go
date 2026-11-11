package engine

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"innobytes.tech/bflow/internal/flow"
	"innobytes.tech/bflow/internal/guard"
	"innobytes.tech/bflow/internal/i18n"
	"innobytes.tech/bflow/internal/review"
	"innobytes.tech/bflow/internal/store"
)

// locOf devuelve el primer localizador no nil de locs, o el por defecto.
func locOf(locs ...*i18n.Localizer) *i18n.Localizer {
	for _, l := range locs {
		if l != nil {
			return l
		}
	}
	return i18n.DefaultLocalizer()
}

// docsReport es el reporte del documenter con las rutas de ## Docs sin cambio.
const docsReport = "reports/docs.md"

// diffBase es la rama contra la que se compara el trabajo de la tarea
// (remote/base), o "" si no hay.
func (e *Engine) diffBase() string {
	base := e.Cfg.VCS.BaseBranch
	if base != "" && e.Cfg.VCS.Remote != "" {
		base = e.Cfg.VCS.Remote + "/" + base
	}
	return base
}

// reviewMap lee el review-map de la tarea; ok es false si todavía no existe.
func (e *Engine) reviewMap(id string) (m review.Map, ok bool) {
	b, err := e.Store.ReadFile(id, artifacts["review-map"])
	if err != nil {
		return m, false
	}
	return review.ParseMap(string(b)), true
}

// qualityEntered es cuándo la tarea entró a quality por última vez; las
// lecturas anteriores son de otra ronda.
func (e *Engine) qualityEntered(id string) time.Time {
	log, err := e.Store.Log(id)
	if err != nil {
		return time.Time{}
	}
	for i := len(log) - 1; i >= 0; i-- {
		if log[i].To == flow.Quality && log[i].From != flow.Quality {
			return log[i].TS
		}
	}
	return time.Time{}
}

// reviewCoverage mide lo que leyó el reviewer desde la última entrada a quality
// contra el diff de la rama (R6). hasMap dice si el review-map existe. Si git
// falla o no hay base, la cobertura queda sin medir.
func (e *Engine) reviewCoverage(ctx context.Context, id string) (cov review.Coverage, m review.Map, hasMap bool) {
	m, hasMap = e.reviewMap(id)
	base := e.diffBase()
	if e.Git == nil {
		return cov, m, hasMap
	}
	diff, err := e.Git.DiffNames(ctx, base)
	if err != nil {
		return cov, m, hasMap
	}
	lines, err := e.Git.DiffLines(ctx, base)
	if err != nil {
		return cov, m, hasMap
	}
	for i := range diff {
		diff[i] = strings.ReplaceAll(diff[i], `\`, "/")
	}
	var reads []review.Read
	if b, err := e.Store.ReadFile(id, review.ReadsFile); err == nil {
		since := e.qualityEntered(id)
		for _, r := range review.ParseReads(strings.NewReader(string(b))) {
			if !r.TS.Before(since) {
				reads = append(reads, r)
			}
		}
	}
	isTest := func(p string) bool { return guard.MatchesTest(e.Cfg.Guard.TestPatterns, p) }
	binary, err := e.Git.DiffBinaries(ctx, base)
	if err != nil {
		binary = nil // R11: se mide sin exentar binarios
	}
	for i := range binary {
		binary[i] = strings.ReplaceAll(binary[i], `\`, "/")
	}
	return review.Measure(diff, lines, reads, m.Red, isTest, binary), m, hasMap
}

// isReviewer dice si el agente del reporte es el reviewer (con o sin prefijo).
func isReviewer(agent string) bool {
	return strings.TrimPrefix(agent, flow.SubagentPrefix) == "reviewer"
}

// reviewIncomplete arma el rechazo de APPROVED cuando falta cobertura o formato
// en el review-map (R7, R8); nil si no falta nada.
func reviewIncomplete(cov review.Coverage, m review.Map, hasMap bool, base string, locs ...*i18n.Localizer) *flow.Rejection {
	if !hasMap {
		return nil
	}
	loc := locOf(locs...)
	var blocks []string
	if cov.Measured && len(cov.RedMissing) > 0 {
		blocks = append(blocks, loc.T("engine.review.red_unread", strings.Join(cov.RedMissing, "\n- ")))
	}
	if cov.Measured && len(cov.Pending) > 0 {
		if base == "" {
			base = "<base>"
		}
		shown, more := cov.Pending, 0
		if len(shown) > 20 {
			shown, more = shown[:20], len(shown)-20
		}
		b := loc.T("engine.review.pending_head", strings.Join(shown, "\n- "))
		if more > 0 {
			b += loc.T("engine.review.pending_more", more)
		}
		b += loc.T("engine.review.pending_open", base)
		if cov.DiffLines < review.WholeDiffMax {
			b += loc.T("engine.review.pending_whole", base)
		}
		b += "."
		blocks = append(blocks, b)
	}
	if len(m.RedNoPath) > 0 {
		blocks = append(blocks, loc.T("engine.review.red_no_path", strings.Join(m.RedNoPath, "\n- ")))
	}
	if len(m.RedNoExits) > 0 {
		blocks = append(blocks, loc.T("engine.review.red_no_exits", strings.Join(m.RedNoExits, "\n- ")))
	}
	if len(m.QuestionTells) > 0 {
		blocks = append(blocks, loc.T("engine.review.question_tells", strings.Join(m.QuestionTells, "\n- ")))
	}
	if !m.HasDocs {
		blocks = append(blocks, loc.T("engine.review.docs_missing"))
	}
	if len(blocks) == 0 {
		return nil
	}
	return &flow.Rejection{Code: "review_incomplete",
		Reason: loc.T("engine.review.incomplete", strings.Join(blocks, "\n"))}
}

// docsPending arma el rechazo del documenter cuando una ruta de ## Docs ni
// cambió en la rama ni consta en reports/docs.md (R15); nil si no aplica.
func (e *Engine) docsPending(ctx context.Context, id string) *flow.Rejection {
	m, ok := e.reviewMap(id)
	base := e.diffBase()
	if !ok || len(m.Docs) == 0 || e.Git == nil {
		return nil
	}
	diff, err := e.Git.DiffNames(ctx, base)
	if err != nil {
		return nil
	}
	for i := range diff {
		diff[i] = strings.ReplaceAll(diff[i], `\`, "/")
	}
	var report string
	if b, err := e.Store.ReadFile(id, docsReport); err == nil {
		report = string(b)
	}
	pending := review.DocsPending(m.Docs, diff, report)
	if len(pending) == 0 {
		return nil
	}
	loc := locOf(e.Loc)
	return &flow.Rejection{Code: "docs_pending", Reason: loc.T("engine.review.docs_pending", docsReport, strings.Join(pending, "\n- "), docsReport)}
}

// coverageEntry es el evento review_coverage de una corrida del reviewer (R12).
func (e *Engine) coverageEntry(id string, cov review.Coverage, agent string, round int) store.Entry {
	data := map[string]any{}
	if b, err := json.Marshal(cov); err == nil {
		_ = json.Unmarshal(b, &data)
	}
	data["round"] = round
	return store.Entry{TS: e.now(), ID: id, Event: "review_coverage", By: e.User, Agent: strings.TrimPrefix(agent, flow.SubagentPrefix), Data: data}
}

// lastCoverage es la cobertura del último review_coverage de la tarea.
func (e *Engine) lastCoverage(id string) (review.Coverage, bool) {
	log, err := e.Store.Log(id)
	if err != nil {
		return review.Coverage{}, false
	}
	for i := len(log) - 1; i >= 0; i-- {
		if log[i].Event != "review_coverage" {
			continue
		}
		var c review.Coverage
		if b, err := json.Marshal(log[i].Data); err == nil && json.Unmarshal(b, &c) == nil {
			return c, true
		}
	}
	return review.Coverage{}, false
}

// changelogRequired rechaza el DONE del documenter cuando el diff toca
// flow.public_paths y el changelog para consumidores no cambió (ni en commits
// ni sin commitear), salvo exención humana (unblock --no-changelog).
func (e *Engine) changelogRequired(ctx context.Context, rec *store.Record) *flow.Rejection {
	cfg := e.flowCfg()
	base := e.diffBase()
	if len(cfg.PublicPaths) == 0 || e.Git == nil || base == "" {
		return nil
	}
	diff, err := e.Git.DiffNames(ctx, base)
	if err != nil {
		return nil
	}
	touched := review.PublicTouched(diff, cfg.PublicPaths)
	if len(touched) == 0 {
		return nil
	}
	cl := cfg.ChangelogPath(rec.Flow.ID, rec.Flow.Slug)
	for _, f := range diff {
		if strings.ReplaceAll(f, `\`, "/") == cl {
			return nil
		}
	}
	if dirty, err := e.Git.Dirty(ctx, []string{cl}); err == nil && len(dirty) > 0 {
		return nil
	}
	if e.changelogExempt(rec.Flow.ID) {
		return nil
	}
	loc := locOf(e.Loc)
	return &flow.Rejection{Code: "changelog_required", Reason: loc.T("engine.review.changelog_required", strings.Join(touched, "\n- "), cl, rec.Flow.ID)}
}

// changelogExempt: hay un unblock --no-changelog posterior a la última entrada
// a documenting (el propio unblock que regresa a la fase no cuenta como entrada).
func (e *Engine) changelogExempt(id string) bool {
	log, err := e.Store.Log(id)
	if err != nil {
		return false
	}
	entered := -1
	for i, en := range log {
		if en.To == flow.Documenting && en.Event != string(flow.EvUnblock) {
			entered = i
		}
	}
	for i := len(log) - 1; i > entered; i-- {
		if log[i].Event == string(flow.EvUnblock) && log[i].Data["no_changelog"] == true {
			return true
		}
	}
	return false
}

// NarrativeComments lista los comentarios agregados en el diff contra la base
// que narran la tarea. Devuelve nil si la regla está apagada, si no hay git o
// base, o si git falla (igual que changelogRequired).
func (e *Engine) NarrativeComments(ctx context.Context, id string) []string {
	cfg := e.flowCfg()
	base := e.diffBase()
	if !cfg.NarrativeComments || e.Git == nil || base == "" {
		return nil
	}
	added, err := e.Git.DiffAdded(ctx, base, e.Cfg.Check.CodePaths)
	if err != nil {
		return nil
	}
	found, err := review.NarrativeComments(added, id, cfg.CommentPatterns)
	if err != nil {
		return nil
	}
	return found
}

// narrativeRejection es el rechazo de DONE con hasta 20 hallazgos.
func narrativeRejection(found []string, locs ...*i18n.Localizer) *flow.Rejection {
	loc := locOf(locs...)
	const max = 20
	shown := found
	if len(shown) > max {
		shown = shown[:max]
	}
	list := strings.Join(shown, "\n")
	if n := len(found) - len(shown); n > 0 {
		list += loc.T("engine.review.narrative_more", n)
	}
	return &flow.Rejection{Code: "narrative_comment", Reason: loc.T("engine.review.narrative_rejected", list)}
}
