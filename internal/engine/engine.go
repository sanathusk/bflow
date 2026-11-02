// Package engine conecta el núcleo de flujo con el mundo: carga el estado del
// store, aplica el evento con flow.Apply y ejecuta los efectos contra el
// tracker, git y el host. Solo conoce interfaces; los adaptadores los inyecta
// cmd/bflow.
package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"innobytes.tech/bflow/internal/agents"
	"innobytes.tech/bflow/internal/check"
	"innobytes.tech/bflow/internal/config"
	"innobytes.tech/bflow/internal/flow"
	"innobytes.tech/bflow/internal/i18n"
	"innobytes.tech/bflow/internal/output"
	"innobytes.tech/bflow/internal/store"
	"innobytes.tech/bflow/internal/tracker"
	"innobytes.tech/bflow/internal/vcs"
)

// CheckHistory lee el check guardado de un commit.
type CheckHistory interface {
	StepAt(ctx context.Context, id, sha, step string) (found bool, status string, err error)
}

// Verifier confirma que el check pasó sobre el código actual.
type Verifier interface {
	Verify(ctx context.Context, id string) (ok bool, detail string, err error)
}

// ContractChecker lee el último check de la tarea para validar el contrato.
type ContractChecker interface {
	Last(ctx context.Context, id string) (res *check.Result, fresh bool, detail string, err error)
}

// Engine ejecuta comandos de flujo.
type Engine struct {
	Cfg      *config.Config
	Store    *store.Store
	Tracker  tracker.Tracker
	Git      vcs.Git         // nil: sin git (se registra la rama pero no se crea)
	Host     vcs.Host        // nil: sin host remoto (se da la URL de compare si se puede)
	Checks   CheckHistory    // nil: hotfix no exige prueba de reproducción
	Verifier Verifier        // nil: DONE se acepta sin verificar si no hay pasos de check
	Contract ContractChecker // nil: el contrato pasa por la gate contract
	Now      func() time.Time
	User     string
	Loc      *i18n.Localizer
}

// Outcome es el resultado de un comando de flujo.
type Outcome struct {
	ID       string        `json:"id"`
	From     flow.Phase    `json:"from,omitempty"`
	To       flow.Phase    `json:"to,omitempty"` // vacío si la fase no cambió
	Phase    flow.Phase    `json:"phase"`
	Gate     string        `json:"gate,omitempty"`
	Round    int           `json:"round"`
	Branch   string        `json:"branch,omitempty"`
	PR       *store.PR     `json:"pr,omitempty"`
	Effects  []string      `json:"effects,omitempty"`
	Warnings []string      `json:"warnings,omitempty"`
	Next     output.Next   `json:"-"`
	Record   *store.Record `json:"-"`
}

func (e *Engine) flowCfg() flow.Config {
	fc := e.Cfg.Flow.Core()
	fc.ContractGate = !e.contractValidable()
	fc.Loc = e.Loc
	return fc
}

// restoreFrozen deja frozen-tests.json como estaba antes de congelar.
func (e *Engine) restoreFrozen(id string, prev map[string]string) {
	if prev == nil {
		if p, err := e.Store.Path(id, FrozenFile); err == nil {
			_ = os.Remove(p)
		}
		return
	}
	_ = e.saveFrozen(id, prev)
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// apply es el camino único de todo comando que cambia estado.
func (e *Engine) apply(ctx context.Context, id string, ev flow.Event, slug string) (Outcome, error) {
	return e.applyWith(ctx, id, ev, slug, nil)
}

// applyWith es apply con un hook que corre dentro de la transacción, tras un
// flow.Apply sin error; si falla, el estado no se guarda.
func (e *Engine) applyWith(ctx context.Context, id string, ev flow.Event, slug string, after func() error) (Outcome, error) {
	out := Outcome{ID: id}
	if w, err := e.syncFirst(ctx, id); err != nil {
		return out, err
	} else {
		out.Warnings = append(out.Warnings, w...)
	}
	var entries []store.Entry
	refreeze := false
	rec, err := e.Store.Update(id, func(rec *store.Record, exists bool) error {
		if !exists {
			if ev.Kind != flow.EvStart {
				if _, err := e.Tracker.Get(ctx, id); err != nil {
					return trackerErr(id, err)
				}
				return &flow.Rejection{Code: "not_started", Reason: fmt.Sprintf("%s no ha empezado: bflow start %s --lane <full|light|hotfix>", id, id)}
			}
			task, err := e.Tracker.Get(ctx, id)
			if err != nil {
				return trackerErr(id, err)
			}
			if slug == "" {
				slug = Slug(task.Title)
			}
			rec.Flow = flow.New(id, slug)
			rec.Title, rec.URL = task.Title, task.URL
		}

		freeze := false
		var prevFrozen map[string]string
		froze := false
		if ev.Kind == flow.EvReport && ev.Verdict == flow.ContractReady && rec.Flow.Phase == flow.Contract {
			verified, rej := e.contractCheck(ctx, id)
			if rej != nil {
				return rej
			}
			ev.ContractOK = verified
			freeze = verified
		}
		if ev.Kind == flow.EvReport && ev.Verdict == flow.Split && rec.Flow.Phase == flow.Spec {
			brief, err := e.specSection(*rec, "brief")
			if err != nil {
				return splitInvalid("no se pudo leer el Brief (%v)", err)
			}
			if _, err := ParseSplit(brief); err != nil {
				return err
			}
		}
		if ev.Kind == flow.EvReport && ev.Agent != flow.ScoutAgent && ev.Verdict == flow.DoneV && rec.Flow.Phase == flow.Documenting {
			if files := e.uncommitted(ctx, rec.Flow); len(files) > 0 {
				return uncommittedRejection(files, "reporta DONE otra vez")
			}
			if files := e.bflowInDiff(ctx); len(files) > 0 {
				return bflowInDiffRejection(files, "reporta DONE otra vez")
			}
			if found := e.NarrativeComments(ctx, id); len(found) > 0 {
				return narrativeRejection(found)
			}
			if rej := e.docsPending(ctx, id); rej != nil {
				return rej
			}
			if rej := e.changelogRequired(ctx, rec); rej != nil {
				return rej
			}
		}
		if ev.Kind == flow.EvReport && ev.Agent != flow.ScoutAgent && ev.Verdict == flow.DoneV && rec.Flow.Phase == flow.Implementing {
			ok, detail, err := e.verify(ctx, id)
			if err != nil {
				return err
			}
			if !ok {
				return &flow.Rejection{Code: "check_required", Reason: "DONE requiere un check verde sobre el código actual: " + detail}
			}
			if changed := e.FrozenChanged(id); len(changed) > 0 {
				return frozenRejection(changed)
			}
			if rec.Flow.RejectHead != "" && e.Git != nil {
				if head, err := e.Git.HeadSHA(ctx); err == nil && head == rec.Flow.RejectHead {
					return &flow.Rejection{Code: "no_new_commits", Reason: "la compuerta de calidad rechazó (ronda " + strconv.Itoa(rec.Flow.Round) + ") y HEAD no avanzó: no hay commit con la corrección. Aplica lo que pide la nota del rechazo, haz commit y reporta DONE otra vez."}
				}
			}
			if open := openTasks(e, *rec); len(open) > 0 {
				return &flow.Rejection{Code: "tasks_open", Reason: "DONE exige todas las tareas de la spec marcadas [x]; faltan:\n" + strings.Join(open, "\n") +
					"\nMárcalas al terminarlas; si una ya no aplica, anótalo en Design y márcala con el motivo."}
			}
			if files := e.uncommitted(ctx, rec.Flow); len(files) > 0 {
				return uncommittedRejection(files, "reporta DONE otra vez")
			}
			if files := e.bflowInDiff(ctx); len(files) > 0 {
				return bflowInDiffRejection(files, "reporta DONE otra vez")
			}
			if found := e.NarrativeComments(ctx, id); len(found) > 0 {
				return narrativeRejection(found)
			}
			if rec.Flow.Lane == flow.Hotfix && e.Checks != nil && e.Git != nil && ev.NoRepro == "" && rec.Flow.NoRepro == "" {
				if rej := e.reproMissing(ctx, *rec); rej != nil {
					return rej
				}
			}
			ev.CheckOK = true
			refreeze = true
		}

		var coverage *store.Entry
		if ev.Kind == flow.EvReport && rec.Flow.Phase == flow.Quality && isReviewer(ev.Agent) {
			cov, m, hasMap := e.reviewCoverage(ctx, id)
			if ev.Verdict == flow.Approved {
				if rej := reviewIncomplete(cov, m, hasMap, e.diffBase()); rej != nil {
					return rej
				}
			}
			en := e.coverageEntry(id, cov, ev.Agent, rec.Flow.Round)
			coverage = &en
		}
		fc := e.flowCfg()
		before := rec.Flow
		res, err := flow.Apply(fc, rec.Flow, ev)
		if err != nil {
			return err
		}
		if after != nil {
			if err := after(); err != nil {
				return err
			}
		}
		if freeze && res.State.Phase != flow.Contract {
			// Sin pruebas congeladas no se avanza: se abre la gate contract.
			prevFrozen = e.readFrozen(id)
			if n, err := e.freezeTests(ctx, id); err != nil {
				ev.ContractOK = false
				out.Warnings = append(out.Warnings, "no se pudieron congelar las pruebas, el contrato queda en la gate contract: "+err.Error())
				if res, err = flow.Apply(fc, before, ev); err != nil {
					return err
				}
			} else {
				froze = true
				if n > 0 {
					out.Warnings = append(out.Warnings, fmt.Sprintf("%d prueba(s) congelada(s): no se editan hasta el PR", n))
				}
			}
		}
		rec.Flow = res.State
		if before.Phase == flow.Quality && rec.Flow.Phase == flow.Implementing && e.Git != nil {
			if head, err := e.Git.HeadSHA(ctx); err == nil {
				rec.Flow.RejectHead = head
			}
		}
		rec.Nudges = nil // cualquier evento cuenta como avance: el agente vuelve a tener sus intentos
		if g := rec.Flow.Gate; g == nil {
			rec.GateSince = time.Time{}
		} else if before.Gate == nil || before.Gate.Name != g.Name || before.Phase != rec.Flow.Phase {
			rec.GateSince = e.now()
		}
		warns, err := e.runEffects(ctx, rec, res.Effects)
		if err != nil {
			rec.Flow = before
			if froze {
				e.restoreFrozen(id, prevFrozen)
			}
			return err
		}
		out.Warnings = append(out.Warnings, warns...)
		for _, fx := range res.Effects {
			out.Effects = append(out.Effects, string(fx.Kind))
		}
		out.From = before.Phase
		if before.Phase != rec.Flow.Phase {
			out.To = rec.Flow.Phase
		}
		entries = append(entries, e.entry(id, ev, before, rec.Flow))
		if coverage != nil {
			entries = append(entries, *coverage)
		}
		return nil
	})
	if err != nil {
		var rej *flow.Rejection
		if errors.As(err, &rej) { // fricción: el agente o el humano pidió algo que el flujo no permite
			_ = e.Store.Append(store.Entry{TS: e.now(), ID: id, Event: "refused", By: e.User,
				Data: map[string]any{"code": rej.Code, "event": string(ev.Kind)}})
		}
		return out, err
	}
	if refreeze { // solo con el DONE aceptado: uno rechazado no gasta el freeze --allow
		e.refreezeAllowed(id)
	}
	if err := e.Store.Append(entries...); err != nil {
		out.Warnings = append(out.Warnings, "no se pudo escribir log.jsonl: "+err.Error())
	}
	out.Warnings = append(out.Warnings, e.afterTransition(rec)...)
	e.updateStatusCache(rec)
	e.fill(&out, rec)
	return out, nil
}

func (e *Engine) fill(out *Outcome, rec store.Record) {
	out.Phase = rec.Flow.Phase
	out.Round = rec.Flow.Round
	out.Branch = rec.Branch
	out.PR = rec.PR
	if rec.Flow.Gate != nil {
		out.Gate = string(rec.Flow.Gate.Name)
	}
	out.Next = e.withDisplay(context.Background(), rec, e.next(rec))
	r := rec
	out.Record = &r
}

func (e *Engine) entry(id string, ev flow.Event, before, after flow.State) store.Entry {
	en := store.Entry{TS: e.now(), ID: id, Event: string(ev.Kind), From: before.Phase, To: after.Phase,
		Agent: ev.Agent, Verdict: string(ev.Verdict), Round: after.Round, Note: ev.Note, By: e.User}
	if ev.Kind == flow.EvApprove || ev.Kind == flow.EvReject {
		switch {
		case ev.Gate == flow.GateBudget:
			en.Gate = string(flow.GateBudget)
		case before.Gate != nil:
			en.Gate = string(before.Gate.Name)
		}
	}
	if len(ev.Children) > 0 {
		ids := make([]string, 0, len(ev.Children))
		for _, c := range ev.Children {
			ids = append(ids, strings.TrimSpace(strings.SplitN(c, " · ", 2)[0]))
		}
		en.Data = map[string]any{"children": ids}
	}
	if ev.Kind == flow.EvUnblock && ev.NoChangelog {
		en.Data = map[string]any{"no_changelog": true}
	}
	if ev.Kind == flow.EvApprove && before.Gate != nil && before.Gate.Name == flow.GateRepro {
		en.Data = map[string]any{"no_repro": before.Gate.Note}
	}
	if ev.Kind == flow.EvStart {
		en.Data = map[string]any{"lane": string(ev.Lane)}
		en.Data["models"] = agents.Models(e.flowCfg(), e.Cfg.Agents)
		if ev.Fixes != "" {
			en.Data["fixes"] = ev.Fixes
		}
	}
	if after.Gate != nil && (before.Gate == nil || before.Gate.Name != after.Gate.Name || before.Phase != after.Phase) {
		if en.Data == nil {
			en.Data = map[string]any{}
		}
		en.Data["gate_opened"] = string(after.Gate.Name)
	}
	return en
}

func (e *Engine) verify(ctx context.Context, id string) (bool, string, error) {
	if e.Verifier != nil {
		return e.Verifier.Verify(ctx, id)
	}
	return true, "", nil
}

func trackerErr(id string, err error) error {
	if errors.Is(err, tracker.ErrNotFound) {
		return fmt.Errorf("%s no existe en el tracker", id)
	}
	return fmt.Errorf("tracker: %w", err)
}

// runEffects ejecuta los efectos de una transición. Rama y PR son
// obligatorios: si fallan, la transición se aborta. Los del tracker no: si
// fallan, el estado local avanza y quedan pendientes para reintentarse.
func (e *Engine) runEffects(ctx context.Context, rec *store.Record, fxs []flow.Effect) ([]string, error) {
	var warns []string
	for _, fx := range fxs {
		switch fx.Kind {
		case flow.FxCreateBranch:
			w, err := e.createBranch(ctx, rec, fx.Hotfix)
			if err != nil {
				return nil, err
			}
			warns = append(warns, w...)
		case flow.FxOpenPR:
			w, err := e.openPR(ctx, rec)
			if err != nil {
				return nil, err
			}
			warns = append(warns, w...)
		default:
			tfx := fx
			if fx.Kind == flow.FxStampStart {
				tfx.Phase = rec.Flow.Phase
			}
			if len(rec.Pending) > 0 {
				rec.Pending = append(rec.Pending, tfx)
				continue
			}
			if err := e.trackerEffect(ctx, rec.Flow.ID, tfx); err != nil {
				rec.Pending = append(rec.Pending, tfx)
				warns = append(warns, fmt.Sprintf("tracker: %s quedó pendiente (%v); se reintenta en el próximo comando o con bflow panel", tfx.Kind, err))
			}
		}
	}
	return warns, nil
}

func (e *Engine) trackerEffect(ctx context.Context, id string, fx flow.Effect) error {
	switch fx.Kind {
	case flow.FxTrackerState:
		return e.Tracker.Transition(ctx, id, fx.Phase, tracker.Patch{})
	case flow.FxStampStart:
		return e.Tracker.Transition(ctx, id, fx.Phase, tracker.Patch{StampStart: e.now()})
	case flow.FxComment:
		return e.Tracker.Comment(ctx, id, fx.Body)
	}
	return fmt.Errorf("efecto desconocido %q", fx.Kind)
}

// syncPending reintenta los efectos pendientes en orden y se detiene en el
// primero que vuelva a fallar.
func (e *Engine) syncPending(ctx context.Context, rec *store.Record) []string {
	for len(rec.Pending) > 0 {
		if err := e.trackerEffect(ctx, rec.Flow.ID, rec.Pending[0]); err != nil {
			return []string{fmt.Sprintf("tracker: %d efecto(s) pendiente(s) siguen sin sincronizar (%v)", len(rec.Pending), err)}
		}
		rec.Pending = rec.Pending[1:]
	}
	rec.Pending = nil
	return nil
}

// Sync reintenta los efectos pendientes de una tarea. Devuelve cuántos aplicó.
func (e *Engine) Sync(ctx context.Context, id string) (int, error) {
	applied := 0
	_, err := e.Store.Update(id, func(rec *store.Record, exists bool) error {
		if !exists {
			return store.ErrNotFound
		}
		before := len(rec.Pending)
		e.syncPending(ctx, rec)
		applied = before - len(rec.Pending)
		return nil
	})
	return applied, err
}

// BranchName arma el nombre de la rama con el patrón configurado.
func (e *Engine) BranchName(s flow.State) string {
	prefix := e.Cfg.VCS.BranchPrefix.Feature
	if s.Lane == flow.Hotfix {
		prefix = e.Cfg.VCS.BranchPrefix.Hotfix
	}
	r := strings.NewReplacer("{prefix}", prefix, "{id}", s.ID, "{id_lower}", strings.ToLower(s.ID), "{slug}", s.Slug)
	name := r.Replace(e.Cfg.VCS.BranchPattern)
	return strings.TrimSuffix(name, "-")
}

func (e *Engine) createBranch(ctx context.Context, rec *store.Record, _ bool) ([]string, error) {
	name := e.BranchName(rec.Flow)
	rec.Branch = name
	if e.Git == nil {
		return []string{"sin git: la rama " + name + " se registró pero no se creó"}, nil
	}
	if dirty, err := e.Git.Dirty(ctx, e.Cfg.Check.CodePaths); err != nil {
		return nil, fmt.Errorf("git: %w", err)
	} else if len(dirty) > 0 {
		return nil, &flow.Rejection{Code: "dirty_worktree", Reason: "hay cambios sin commitear en el código; guárdalos antes de crear la rama: " + strings.Join(dirty, ", ")}
	}
	base := e.Cfg.VCS.BaseBranch
	how, err := e.Git.EnsureBranch(ctx, name, base)
	if err != nil {
		return nil, fmt.Errorf("git: %w", err)
	}
	if head, err := e.Git.HeadSHA(ctx); err == nil {
		rec.StartSHA = head
	}
	// Una rama que ya existía trae commits propios: el rango de la tarea arranca
	// donde se separó de la principal, no en su punta.
	if how != "created" {
		if mb, err := e.Git.MergeBase(ctx, base); err == nil && mb != "" {
			rec.StartSHA = mb
		}
	}
	warns := []string{fmt.Sprintf("rama %s (%s)", name, how)}
	return append(warns, e.commitSpec(ctx, rec.Flow, "spec")...), nil
}

// commitSpec hace commit de la spec de la tarea, si el carril la tiene y
// cambió: la spec es lo único del flujo que vive en el repo y tiene que llegar
// al PR aunque ningún agente la commitee.
func (e *Engine) commitSpec(ctx context.Context, s flow.State, summary string) []string {
	if e.Git == nil || !slices.Contains(e.flowCfg().Lanes[s.Lane], flow.Spec) {
		return nil
	}
	paths := e.specFiles(s)
	done, err := e.Git.Commit(ctx, paths, e.commitMessage("docs", s.ID, summary))
	switch {
	case err != nil:
		return []string{"no se pudo hacer commit de la spec (" + err.Error() + "); commitéala a mano: " + strings.Join(paths, " ")}
	case done:
		return []string{"commit de la spec: " + strings.Join(paths, " ")}
	}
	return nil
}

// specFiles son los archivos versionados que bflow commitea por la tarea: la
// spec y, si ya existe, el changelog para consumidores.
func (e *Engine) specFiles(s flow.State) []string {
	paths := []string{flow.SpecPath(s.ID, s.Slug)}
	cl := e.flowCfg().ChangelogPath(s.ID, s.Slug)
	if _, err := os.Stat(filepath.Join(e.Cfg.Root, filepath.FromSlash(cl))); err == nil {
		paths = append(paths, cl)
	}
	return paths
}

// commitMessage aplica vcs.commit_style a un commit que hace bflow.
func (e *Engine) commitMessage(typ, id, summary string) string {
	return strings.NewReplacer("{type}", typ, "{id}", id, "{summary}", summary).Replace(e.Cfg.VCS.CommitStyle)
}

// syncFirst reintenta los pendientes en su propia escritura, antes de la
// transición: si la transición falla, lo sincronizado no se pierde (y no se
// duplican comentarios en el siguiente intento).
func (e *Engine) syncFirst(ctx context.Context, id string) ([]string, error) {
	rec, err := e.Store.Load(id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && len(rec.Pending) == 0) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var warns []string
	_, err = e.Store.Update(id, func(rec *store.Record, _ bool) error {
		warns = e.syncPending(ctx, rec)
		return nil
	})
	return warns, err
}
