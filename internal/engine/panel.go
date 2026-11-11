package engine

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"innobytes.tech/bflow/internal/flow"
	"innobytes.tech/bflow/internal/store"
	"innobytes.tech/bflow/internal/tracker"
	"innobytes.tech/bflow/internal/vcs"
)

// PR publica y abre o actualiza el PR de una tarea en in_review (reintento
// tras un fallo o refresco de la descripción).
func (e *Engine) PR(ctx context.Context, id string) (Outcome, error) {
	out := Outcome{ID: id}
	rec, err := e.Store.Update(id, func(rec *store.Record, exists bool) error {
		if !exists {
			return &flow.Rejection{Code: "not_started", Reason: e.loc().T("engine.panel.not_started", id)}
		}
		if rec.Flow.Phase != flow.InReview {
			return &flow.Rejection{Code: "pr_not_ready", Reason: e.loc().T("engine.panel.pr_not_ready", id, rec.Flow.Phase)}
		}
		w, err := e.openPR(ctx, rec)
		out.Warnings = w
		return err
	})
	if err != nil {
		return out, err
	}
	e.fill(&out, rec)
	return out, nil
}

// PanelItem es un compromiso que requiere atención humana.
type PanelItem struct {
	ID      string     `json:"id"`
	Title   string     `json:"title,omitempty"`
	Phase   flow.Phase `json:"phase"`
	Gate    string     `json:"gate,omitempty"`
	Hours   int        `json:"waiting_hours,omitempty"`
	SLA     bool       `json:"sla,omitempty"` // esperando más de sla_hours
	Due     string     `json:"due,omitempty"`
	Overdue bool       `json:"overdue,omitempty"`
	score   int
}

// PanelReport es el resultado de bflow panel.
type PanelReport struct {
	Items  []PanelItem `json:"items"`
	More   int         `json:"more,omitempty"`
	Closed []string    `json:"closed,omitempty"` // in_review → done por merge
	// ClosedOutside son las tareas cerradas en local porque se terminaron fuera
	// de esta copia (el tracker las da por hechas o su PR se mergeó).
	ClosedOutside []ClosedOutside `json:"closed_outside,omitempty"`
	Reminded      []string        `json:"reminded,omitempty"` // comentarios de SLA/vencimiento publicados
	Warnings      []string        `json:"warnings,omitempty"`
}

// ClosedOutside describe una tarea que panel cerró desde afuera del flujo.
type ClosedOutside struct {
	ID     string     `json:"id"`
	From   flow.Phase `json:"from"`
	Reason string     `json:"reason"`       // "tracker" | "pr"
	PR     int        `json:"pr,omitempty"` // número, con motivo "pr"
}

const panelMax = 9

// Panel revisa las tareas en curso: sincroniza pendientes, cierra las que se
// mergearon y arma la lista de compromisos. Con sla, además comenta en el
// tracker los gates que llevan más de sla_hours esperando y las vencidas
// (una sola vez por aviso).
func (e *Engine) Panel(ctx context.Context, sla bool) (PanelReport, error) {
	var rep PanelReport
	recs, err := e.Store.List()
	if err != nil {
		return rep, err
	}
	due := map[string]*time.Time{}
	open := map[string]tracker.Task{}
	listed := false
	if tasks, err := e.Tracker.List(ctx, tracker.Filter{OpenOnly: true}); err == nil {
		listed = true
		for _, t := range tasks {
			due[t.ID] = t.Due
			open[t.ID] = t
		}
	} else {
		rep.Warnings = append(rep.Warnings, e.loc().T("engine.panel.tracker_unavailable", err.Error()))
	}
	now := e.now()
	slaHours := e.Cfg.Flow.SLAHours
	for _, rec := range recs {
		if rec.Flow.Phase == flow.Done || rec.Flow.Phase == flow.Dropped {
			continue
		}
		id := rec.Flow.ID
		// Un PR mergeado manda sobre el estado del tracker: "Closes #N" cierra el
		// issue sin mover su etiqueta, y el cierre normal sí la deja en done.
		if rec.Flow.Phase == flow.InReview {
			closed, warn := e.checkMerged(ctx, e.reload(rec))
			if warn != "" {
				rep.Warnings = append(rep.Warnings, warn)
			}
			if closed {
				rep.Closed = append(rep.Closed, id)
				continue
			}
		}
		// Primero el cierre desde afuera: un efecto pendiente viejo no debe
		// regresar en el tracker lo que ya se terminó.
		if co, warns := e.reconcile(ctx, rec, open, listed); co != nil || len(warns) > 0 {
			rep.Warnings = append(rep.Warnings, warns...)
			if co != nil {
				rep.ClosedOutside = append(rep.ClosedOutside, *co)
				continue
			}
		}
		if len(rec.Pending) > 0 {
			if _, err := e.Sync(ctx, id); err != nil {
				rep.Warnings = append(rep.Warnings, id+": "+err.Error())
			}
		}
		it := PanelItem{ID: id, Title: rec.Title, Phase: rec.Flow.Phase}
		waitingSince := time.Time{}
		switch {
		case rec.Flow.Phase == flow.Blocked:
			it.Gate, waitingSince = "blocked", rec.Since
		case rec.Flow.Gate != nil:
			it.Gate, waitingSince = string(rec.Flow.Gate.Name), rec.GateSince
		}
		if !waitingSince.IsZero() {
			it.Hours = int(now.Sub(waitingSince).Hours())
			it.SLA = it.Hours >= slaHours
			it.score++
			if it.SLA {
				it.score += 2
			}
		}
		if d := due[id]; d != nil {
			it.Due = d.Format("2006-01-02")
			days := d.Sub(now).Hours() / 24
			if days < 0 {
				it.Overdue = true
				it.score += 4
			} else if days <= 3 {
				it.score++
			}
		}
		if it.score == 0 {
			continue
		}
		if sla {
			rep.Reminded = append(rep.Reminded, e.remind(ctx, it, waitingSince, &rep)...)
		}
		rep.Items = append(rep.Items, it)
	}
	sort.SliceStable(rep.Items, func(i, j int) bool { return rep.Items[i].score > rep.Items[j].score })
	if len(rep.Items) > panelMax {
		rep.More = len(rep.Items) - panelMax
		rep.Items = rep.Items[:panelMax]
	}
	return rep, nil
}

// reload devuelve el registro al día (el Sync previo pudo cambiarlo).
func (e *Engine) reload(rec store.Record) store.Record {
	if r, err := e.Store.Load(rec.Flow.ID); err == nil {
		return r
	}
	return rec
}

// reconcile revisa si una tarea no cerrada ya se terminó fuera de esta copia:
// el tracker la da por hecha o cerrada, o el PR de su rama se mergeó. Las
// tareas en in_review con PR mergeado siguen el cierre normal (checkMerged).
// Los errores de red solo dan avisos.
func (e *Engine) reconcile(ctx context.Context, rec store.Record, open map[string]tracker.Task, listed bool) (*ClosedOutside, []string) {
	id := rec.Flow.ID
	var warns []string
	t, ok := open[id]
	if !ok && listed {
		got, err := e.Tracker.Get(ctx, id)
		if err != nil {
			warns = append(warns, e.loc().T("engine.panel.tracker_query_failed", id, err.Error()))
		} else {
			t, ok = got, true
		}
	}
	if ok && (t.Phase == flow.Done || t.Closed) {
		state := t.State
		if state == "" {
			state = string(t.Phase)
		}
		if co, w := e.closeOutside(ctx, rec, "tracker", nil, state); co {
			if w != "" {
				warns = append(warns, w)
			}
			return &ClosedOutside{ID: id, From: rec.Flow.Phase, Reason: "tracker"}, warns
		} else if w != "" {
			warns = append(warns, w)
		}
		return nil, warns
	}
	if rec.Flow.Phase == flow.InReview || e.Host == nil {
		return nil, warns
	}
	var (
		pr  vcs.PR
		err error
		n   int
	)
	switch {
	case rec.PR != nil && rec.PR.Number > 0:
		n = rec.PR.Number
		pr, err = e.Host.PRStatus(ctx, n)
	case rec.Branch != "":
		pr, err = e.Host.FindMergedPR(ctx, rec.Branch)
	default:
		return nil, warns
	}
	switch {
	case errors.Is(err, vcs.ErrNoCredentials), errors.Is(err, vcs.ErrNoPR):
		return nil, warns
	case err != nil:
		return nil, append(warns, e.loc().T("engine.panel.pr_query_failed", id, rec.Flow.Phase, err))
	case pr.Merged:
		if co, w := e.closeOutside(ctx, rec, "pr", &pr, ""); co {
			if w != "" {
				warns = append(warns, w)
			}
			return &ClosedOutside{ID: id, From: rec.Flow.Phase, Reason: "pr", PR: pr.Number}, warns
		} else if w != "" {
			warns = append(warns, w)
		}
	case pr.State == "closed":
		num := pr.Number
		if n > 0 {
			num = n
		}
		warns = append(warns, e.loc().T("engine.panel.pr_closed_unmerged", id, num))
	}
	return nil, warns
}

// closeOutside cierra en local una tarea que se terminó fuera de esta copia.
// Con motivo "tracker" no toca el tracker y descarta los efectos pendientes
// (un estado viejo regresaría el issue); con "pr" lo mueve a done como un merge.
func (e *Engine) closeOutside(ctx context.Context, rec store.Record, reason string, pr *vcs.PR, trackerState string) (bool, string) {
	id := rec.Flow.ID
	var entry store.Entry
	var warns []string
	_, err := e.Store.Update(id, func(r *store.Record, exists bool) error {
		if !exists {
			return store.ErrNotFound
		}
		before := r.Flow
		res, err := flow.Apply(e.flowCfg(), r.Flow, flow.Event{Kind: flow.EvClosedOutside, Reason: reason})
		if err != nil {
			return err
		}
		r.Flow = res.State
		r.GateSince = time.Time{}
		r.Nudges = nil
		data := map[string]any{"reason": reason}
		if reason == "tracker" {
			r.Pending = nil
			if trackerState != "" {
				data["tracker_state"] = trackerState
			}
		} else if pr != nil {
			r.PR = &store.PR{Number: pr.Number, URL: pr.URL, Merged: true}
			data["pr"] = pr.Number
		}
		w, err := e.runEffects(ctx, r, res.Effects)
		if err != nil {
			r.Flow = before
			return err
		}
		warns = w
		entry = store.Entry{TS: e.now(), ID: id, Event: string(flow.EvClosedOutside), From: before.Phase, To: flow.Done, By: e.User, Data: data}
		return nil
	})
	if err != nil {
		return false, e.loc().T("engine.panel.close_outside_failed", id, err)
	}
	if err := e.Store.Append(entry); err != nil {
		warns = append(warns, e.loc().T("engine.core.log_append_failed", err.Error()))
	}
	if len(warns) > 0 {
		return true, strings.Join(warns, "; ")
	}
	return true, ""
}

// checkMerged consulta el PR de una tarea en in_review y la cierra si se mergeó.
func (e *Engine) checkMerged(ctx context.Context, rec store.Record) (bool, string) {
	id := rec.Flow.ID
	if e.Host == nil || rec.PR == nil {
		return false, ""
	}
	n := rec.PR.Number
	if n == 0 { // PR creado a mano desde la URL de compare: buscarlo por rama
		pr, err := e.Host.FindPR(ctx, rec.Branch)
		if err != nil {
			return false, ""
		}
		n = pr.Number
		_, _ = e.Store.Update(id, func(r *store.Record, _ bool) error {
			r.PR = &store.PR{Number: pr.Number, URL: pr.URL}
			return nil
		})
	}
	pr, err := e.Host.PRStatus(ctx, n)
	if errors.Is(err, vcs.ErrNoCredentials) {
		return false, ""
	}
	if err != nil {
		return false, e.loc().T("engine.panel.pr_status_failed", id, n, err)
	}
	switch {
	case pr.Merged:
		if _, err := e.Merged(ctx, id); err != nil {
			return false, e.loc().T("engine.panel.merged_close_failed", id, err)
		}
		return true, ""
	case pr.State == "closed":
		return false, e.loc().T("engine.panel.pr_closed_unmerged", id, n)
	}
	return false, ""
}

// remind publica en el tracker los avisos de SLA y vencimiento, una sola vez.
func (e *Engine) remind(ctx context.Context, it PanelItem, since time.Time, rep *PanelReport) []string {
	var want []string
	if it.SLA {
		want = append(want, e.loc().T("engine.panel.sla_waiting", it.Gate, since.Format("2006-01-02 15:04")))
	}
	if it.Overdue {
		want = append(want, e.loc().T("engine.panel.overdue", it.Due))
	}
	if len(want) == 0 {
		return nil
	}
	existing, err := e.Tracker.Comments(ctx, it.ID)
	if err != nil {
		rep.Warnings = append(rep.Warnings, it.ID+": "+err.Error())
		return nil
	}
	var posted []string
	for _, w := range want {
		dup := false
		for _, c := range existing {
			if strings.Contains(c.Body, w) {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		if err := e.Tracker.Comment(ctx, it.ID, w); err != nil {
			rep.Warnings = append(rep.Warnings, it.ID+": "+err.Error())
			continue
		}
		posted = append(posted, it.ID+": "+w)
	}
	return posted
}
