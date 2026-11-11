package engine

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"innobytes.tech/bflow/internal/flow"
	"innobytes.tech/bflow/internal/output"
	"innobytes.tech/bflow/internal/store"
	"innobytes.tech/bflow/internal/tracker"
)

// Start toma la tarea del tracker y la pone en la primera fase del carril.
// fixes (solo hotfix) liga la tarea a la feature que corrige, para sus métricas.
func (e *Engine) Start(ctx context.Context, id string, lane flow.Lane, slug, fixes string) (Outcome, error) {
	if slug != "" {
		slug = Slug(slug)
	}
	fixes = strings.ToUpper(strings.TrimSpace(fixes))
	out, err := e.apply(ctx, id, flow.Event{Kind: flow.EvStart, Lane: lane, Fixes: fixes}, slug)
	if err == nil && fixes != "" {
		if _, lerr := e.Store.Load(fixes); lerr != nil {
			out.Warnings = append(out.Warnings, e.loc().T("engine.commands.fixes_no_history", fixes))
		}
	}
	return out, err
}

// New crea la tarea en el tracker y la arranca en el carril. Si el carril no
// existe no crea nada. slug es opcional (vacío: se deriva del título). Si Start falla, la tarea ya existe: se devuelve con un
// error que dice cómo reintentar.
func (e *Engine) New(ctx context.Context, lane flow.Lane, title, desc string, slug ...string) (tracker.Task, Outcome, error) {
	if _, ok := e.flowCfg().Lanes[lane]; !ok {
		var names []string
		for l := range e.flowCfg().Lanes {
			names = append(names, string(l))
		}
		sort.Strings(names)
		return tracker.Task{}, Outcome{}, &flow.Rejection{Code: "unknown_lane",
			Reason: e.loc().T("engine.commands.unknown_lane", lane, strings.Join(names, ", "))}
	}
	task, err := e.CreateTask(ctx, title, desc)
	if err != nil {
		return tracker.Task{}, Outcome{}, err
	}
	out, err := e.Start(ctx, task.ID, lane, strings.Join(slug, ""), "")
	if err != nil {
		return task, Outcome{ID: task.ID}, fmt.Errorf("%s: %w. %s", e.loc().T("engine.commands.created_not_started", task.ID), err, e.loc().T("engine.commands.retry_start", task.ID, lane))
	}
	return task, out, nil
}

// OpenTasks lista las tareas abiertas del tracker.
func (e *Engine) OpenTasks(ctx context.Context) ([]tracker.Task, error) {
	return e.Tracker.List(ctx, tracker.Filter{OpenOnly: true})
}

// ApproveOpts son las opciones de approve.
type ApproveOpts struct {
	Gate       string
	Choice     int
	Note       string
	Manual     bool
	Attachment string // discovery completo
}

// Approve aprueba el gate pendiente.
func (e *Engine) Approve(ctx context.Context, id string, o ApproveOpts) (Outcome, error) {
	if o.Gate == string(flow.GateBudget) {
		return e.approveBudget(id)
	}
	var pending *flow.PendingGate
	if rec, err := e.Store.Load(id); err == nil {
		pending = rec.Flow.Gate
	}
	ev := flow.Event{Kind: flow.EvApprove, Gate: flow.Gate(o.Gate), Choice: o.Choice, Note: o.Note, Manual: o.Manual, Attachment: o.Attachment}
	if pending != nil && pending.Name == flow.GateSplit && (o.Gate == "" || o.Gate == string(flow.GateSplit)) {
		children, err := e.createChildren(ctx, id)
		if err != nil {
			return Outcome{ID: id}, err
		}
		ev.Children = children
	}
	out, err := e.apply(ctx, id, ev, "")
	if err != nil {
		return out, err
	}
	if pending != nil && pending.Name == flow.GateDiscovery && strings.TrimSpace(o.Attachment) != "" {
		rec, err := e.Store.Load(id)
		if err == nil {
			err = e.fillSection(rec, "discovery", o.Attachment)
		}
		if err != nil {
			out.Warnings = append(out.Warnings, e.loc().T("engine.commands.discovery_write_failed", err.Error()))
		}
	}
	if pending != nil && pending.Name == flow.GateContract && out.To == flow.Implementing {
		if n, err := e.freezeTests(ctx, id); err != nil {
			out.Warnings = append(out.Warnings, e.loc().T("engine.commands.freeze_failed", err.Error()))
		} else if n > 0 {
			out.Warnings = append(out.Warnings, e.loc().T("engine.commands.tests_frozen", n))
		}
	}
	if pending != nil && pending.Name == flow.GateRepro {
		line := fmt.Sprintf("- %s · gate repro: hotfix sin prueba de reproducción: %s (%s)\n", e.now().Format("2006-01-02"), oneLine(pending.Note), e.User)
		if err := e.appendFile(id, "decisions.md", "# Decisiones en vuelo\n\n", line); err != nil {
			out.Warnings = append(out.Warnings, e.loc().T("engine.commands.decisions_write_failed", err.Error()))
		}
	}
	if pending != nil && pending.Name == flow.GateDecision && out.Record != nil {
		line := fmt.Sprintf("- %s · %s: %s → **%s** (%s)\n", e.now().Format("2006-01-02"), pending.Agent,
			oneLine(pending.Note), out.Record.Flow.Decision, e.User)
		if err := e.appendFile(id, "decisions.md", "# Decisiones en vuelo\n\n", line); err != nil {
			out.Warnings = append(out.Warnings, e.loc().T("engine.commands.decisions_write_failed", err.Error()))
		}
	}
	if pending != nil && pending.Name != flow.GateDecision && pending.Name != flow.GateQuestions && strings.TrimSpace(o.Note) != "" {
		line := fmt.Sprintf("- %s · gate %s: %s (%s)\n", e.now().Format("2006-01-02"), pending.Name, oneLine(o.Note), e.User)
		if err := e.appendFile(id, "decisions.md", "# Decisiones en vuelo\n\n", line); err != nil {
			out.Warnings = append(out.Warnings, e.loc().T("engine.commands.decisions_write_failed", err.Error()))
		}
	}
	out.Next.Clear = flow.SuggestClear(out.To, out.Next)
	return out, nil
}

// RejectOpts son las opciones de reject.
type RejectOpts struct {
	Gate string
	To   string
	Note string
}

// Reject rechaza el gate pendiente.
func (e *Engine) Reject(ctx context.Context, id string, o RejectOpts) (Outcome, error) {
	if o.Gate == string(flow.GateBudget) {
		return e.rejectBudget(id, o.Note)
	}
	return e.apply(ctx, id, flow.Event{Kind: flow.EvReject, Gate: flow.Gate(o.Gate), To: flow.Phase(o.To), Note: o.Note}, "")
}

// ScoutMaxLines es el tope de líneas del reporte del scout.
const ScoutMaxLines = 40

// ReportOpts son las opciones de report.
type ReportOpts struct {
	Agent   string
	Verdict flow.Verdict
	Note    string
	File    string
	Options []string
	// Content es el reporte por stdin (solo el scout); nil = sin --stdin.
	Content *string
	// NoRepro apunta al motivo de --no-repro; nil si la bandera no vino.
	NoRepro *string
}

// Report registra el veredicto de un agente.
func (e *Engine) Report(ctx context.Context, id string, o ReportOpts) (Outcome, error) {
	ev := flow.Event{Kind: flow.EvReport, Agent: o.Agent, Verdict: flow.Verdict(strings.ToUpper(string(o.Verdict))), Note: o.Note, Options: o.Options}
	var after func() error
	if o.NoRepro != nil {
		if strings.TrimSpace(*o.NoRepro) == "" {
			return Outcome{ID: id}, &flow.Rejection{Code: "no_repro_invalid", Reason: e.loc().T("engine.commands.no_repro_invalid")}
		}
		ev.NoRepro = strings.TrimSpace(*o.NoRepro)
	}
	if o.Content != nil && o.Agent != flow.ScoutAgent {
		return Outcome{ID: id}, &flow.Rejection{Code: "stdin_scout_only", Reason: e.loc().T("engine.commands.stdin_scout_only")}
	}
	if o.Agent == flow.ScoutAgent {
		c := ""
		if o.Content != nil {
			c = *o.Content
		}
		if strings.TrimSpace(c) == "" {
			return Outcome{ID: id}, &flow.Rejection{Code: "scout_empty", Reason: e.loc().T("engine.commands.scout_empty")}
		}
		if n := strings.Count(strings.TrimRight(c, "\n"), "\n") + 1; n > ScoutMaxLines {
			return Outcome{ID: id}, &flow.Rejection{Code: "scout_too_long", Reason: e.loc().T("engine.commands.scout_too_long", n, ScoutMaxLines)}
		}
		after = func() error { return e.Store.WriteFile(id, "reports/scout.md", []byte(c)) }
	}
	out, err := e.applyWith(ctx, id, ev, "", after)
	if err == nil && o.File != "" {
		_ = e.Store.Append(store.Entry{TS: e.now(), ID: id, Event: "report_file", Agent: o.Agent, By: e.User, Data: map[string]any{"file": o.File}})
	}
	return out, err
}

// Block bloquea la tarea con un motivo.
func (e *Engine) Block(ctx context.Context, id, reason string) (Outcome, error) {
	return e.apply(ctx, id, flow.Event{Kind: flow.EvBlock, Note: reason}, "")
}

// Unblock regresa la tarea a la fase donde se bloqueó. Si note no está vacía,
// la guarda para que llegue en los args del agente que retoma.
// Con noChangelog, además, exime a documenting del changelog (exige nota).
func (e *Engine) Unblock(ctx context.Context, id, note string, noChangelog bool) (Outcome, error) {
	if noChangelog && strings.TrimSpace(note) == "" {
		return Outcome{}, &flow.Rejection{Code: "note_required", Reason: e.loc().T("engine.commands.no_changelog_note_required")}
	}
	return e.apply(ctx, id, flow.Event{Kind: flow.EvUnblock, Note: note, NoChangelog: noChangelog}, "")
}

// Drop retira la tarea sin terminarla: la cierra en el tracker y la saca de
// status. No toca la rama ni el PR; avisa si existen.
func (e *Engine) Drop(ctx context.Context, id, note string) (Outcome, error) {
	out, err := e.apply(ctx, id, flow.Event{Kind: flow.EvDrop, Note: note}, "")
	if err != nil || out.Record == nil {
		return out, err
	}
	if b := out.Record.Branch; b != "" {
		out.Warnings = append(out.Warnings, e.loc().T("engine.commands.branch_still_exists", b))
	}
	if pr := out.Record.PR; pr != nil && !pr.Merged {
		out.Warnings = append(out.Warnings, e.loc().T("engine.commands.pr_still_open", pr.URL))
	}
	return out, nil
}

// Merged cierra una tarea en in_review cuyo PR se mergeó.
func (e *Engine) Merged(ctx context.Context, id string) (Outcome, error) {
	out, err := e.apply(ctx, id, flow.Event{Kind: flow.EvMerged}, "")
	if err == nil {
		_, _ = e.Store.Update(id, func(r *store.Record, _ bool) error {
			if r.PR != nil {
				r.PR.Merged = true
			}
			return nil
		})
	}
	return out, err
}

// View es la foto de una tarea para status.
type View struct {
	ID      string     `json:"id"`
	Title   string     `json:"title,omitempty"`
	URL     string     `json:"url,omitempty"`
	Lane    flow.Lane  `json:"lane,omitempty"`
	Phase   flow.Phase `json:"phase"`
	Gate    string     `json:"gate,omitempty"`
	Round   int        `json:"round,omitempty"`
	Since   *time.Time `json:"since,omitempty"`
	Branch  string     `json:"branch,omitempty"`
	PR      *store.PR  `json:"pr,omitempty"`
	Blocked string     `json:"blocked,omitempty"`
	Pending int        `json:"pending,omitempty"`
	Started bool       `json:"started,omitempty"`
	// Estado en el tracker de una tarea que bflow todavía no empezó.
	TrackerState string      `json:"tracker_state,omitempty"`
	TrackerPhase flow.Phase  `json:"tracker_phase,omitempty"`
	Next         output.Next `json:"-"`
	// Para el panel de una persona (bflow watch); no van en la salida de status.
	Upcoming flow.Step `json:"-"` // qué viene si todo sale bien
	// El repo no puede validar el contrato: tras contract se abre la gate contract.
	ContractGate bool         `json:"-"`
	Phases       []flow.Phase `json:"-"` // las del carril, en orden
	Reported     []string     `json:"-"` // agentes que ya reportaron en esta fase
	GateSince    time.Time    `json:"-"`
	// Dónde estaba al bloquearse.
	BlockedFrom flow.Phase `json:"-"`
}

// Status devuelve la vista de una tarea, empezada o no.
func (e *Engine) Status(ctx context.Context, id string) (View, error) {
	rec, err := e.Store.Load(id)
	if errors.Is(err, store.ErrNotFound) {
		task, terr := e.Tracker.Get(ctx, id)
		if terr != nil {
			return View{}, e.trackerErr(id, terr)
		}
		s := flow.New(id, Slug(task.Title))
		return View{ID: id, Title: task.Title, URL: task.URL, Phase: flow.Backlog, TrackerState: task.State, TrackerPhase: task.Phase,
			Next: e.next(store.Record{Flow: s})}, nil
	}
	if err != nil {
		return View{}, err
	}
	v := e.view(rec)
	v.Next = e.withDisplay(ctx, rec, v.Next)
	return v, nil
}

func (e *Engine) view(rec store.Record) View {
	v := View{ID: rec.Flow.ID, Title: rec.Title, URL: rec.URL, Lane: rec.Flow.Lane, Phase: rec.Flow.Phase, Round: rec.Flow.Round,
		Since: &rec.Since, Branch: rec.Branch, PR: rec.PR, Pending: len(rec.Pending), Started: true,
		Next: e.next(rec), Upcoming: flow.Upcoming(e.flowCfg(), rec.Flow), ContractGate: !e.contractValidable(), GateSince: rec.GateSince,
		Phases: e.flowCfg().Lanes[rec.Flow.Lane]}
	for _, a := range slices.Sorted(maps.Keys(rec.Flow.Reports)) {
		v.Reported = append(v.Reported, a)
	}
	if rec.Flow.Gate != nil {
		v.Gate = string(rec.Flow.Gate.Name)
	}
	if rec.Flow.Block != nil {
		v.Blocked = rec.Flow.Block.Reason
		v.BlockedFrom = rec.Flow.Block.From
	}
	return v
}

// Views devuelve todas las tareas empezadas y no cerradas.
func (e *Engine) Views(ctx context.Context) ([]View, error) {
	recs, err := e.Store.List()
	if err != nil {
		return nil, err
	}
	var out []View
	for _, r := range recs {
		if r.Flow.Phase != flow.Done && r.Flow.Phase != flow.Dropped {
			out = append(out, e.view(r))
		}
	}
	return out, nil
}

// Active resuelve la tarea sobre la que actuar cuando no se da ID: la de la
// rama actual; si no, la única tarea abierta.
func (e *Engine) Active(ctx context.Context) (string, error) {
	views, err := e.Views(ctx)
	if err != nil {
		return "", err
	}
	if e.Git != nil {
		if br, err := e.Git.CurrentBranch(ctx); err == nil && br != "" {
			for _, v := range views {
				if v.Branch == br {
					return v.ID, nil
				}
			}
		}
	}
	switch len(views) {
	case 0:
		return "", &flow.Rejection{Code: "no_active_task", Reason: e.loc().T("engine.commands.no_active_task")}
	case 1:
		return views[0].ID, nil
	}
	var ids []string
	for _, v := range views {
		ids = append(ids, fmt.Sprintf("%s (%s)", v.ID, v.Phase))
	}
	return "", &flow.Rejection{Code: "ambiguous_task", Reason: e.loc().T("engine.commands.ambiguous_task", strings.Join(ids, ", "))}
}

// CreateTask crea una tarea si el tracker lo permite.
func (e *Engine) CreateTask(ctx context.Context, title, desc string) (tracker.Task, error) {
	c, ok := e.Tracker.(tracker.Creator)
	if !ok {
		return tracker.Task{}, fmt.Errorf("%s", e.loc().T("engine.commands.tracker_no_create", e.Tracker.Name()))
	}
	return c.Create(ctx, title, desc)
}

// afterTransition hace el trabajo de archivos que depende de la fase nueva.
func (e *Engine) afterTransition(rec store.Record) []string {
	if rec.Flow.Phase == flow.Spec {
		if err := e.ensureSpec(rec); err != nil {
			return []string{e.loc().T("engine.commands.spec_create_failed", err.Error())}
		}
	}
	return nil
}

func (e *Engine) appendFile(id, rel, header, line string) error {
	b, err := e.Store.ReadFile(id, rel)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(b) == 0 {
		b = []byte(header)
	}
	return e.Store.WriteFile(id, rel, append(b, []byte(line)...))
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// SortedIDs es un auxiliar para mensajes.
func SortedIDs(views []View) []string {
	var ids []string
	for _, v := range views {
		ids = append(ids, v.ID)
	}
	sort.Strings(ids)
	return ids
}

// specAbs es la ruta absoluta del spec.
func (e *Engine) specAbs(s flow.State) string {
	return filepath.Join(e.Cfg.Root, filepath.FromSlash(flow.SpecPath(s.ID, s.Slug)))
}
