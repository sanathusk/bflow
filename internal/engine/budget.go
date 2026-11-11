package engine

import (
	"context"
	"fmt"
	"strings"

	"innobytes.tech/bflow/internal/flow"
	"innobytes.tech/bflow/internal/metrics"
	"innobytes.tech/bflow/internal/output"
	"innobytes.tech/bflow/internal/store"
)

// phaseUsage suma Usage.New() de todos los eventos tokens de id en la fase p
// (agentes y sesión principal), acumulado sobre todas las entradas a la fase
// (R4).
func (e *Engine) phaseUsage(id string, p flow.Phase) (int64, error) {
	entries, err := e.Store.Log(id)
	if err != nil {
		return 0, err
	}
	var used int64
	for _, en := range entries {
		if en.Event != "tokens" {
			continue
		}
		if fmt.Sprint(en.Data["phase"]) != string(p) {
			continue
		}
		used += metrics.UsageOf(en.Data).New()
	}
	return used, nil
}

// budgetNext arma el next de la pregunta de presupuesto (R6).
func (e *Engine) budgetNext(id string, p flow.Phase, used, budget int64) output.Next {
	mult := float64(used) / float64(budget)
	return output.Next{
		Action:   output.ActionAsk,
		Gate:     string(flow.GateBudget),
		Display:  fmt.Sprintf("%s: %s / %s %s (%.1fx)", p, metrics.Human(used), metrics.Human(budget), e.loc().T("engine.budget.new_tokens"), mult),
		Question: e.loc().T("engine.budget.question", p),
		Options: []output.Option{
			{ID: "continue", Label: e.loc().T("engine.budget.option_continue"), Command: fmt.Sprintf("bflow approve %s --gate budget", id)},
			{ID: "block", Label: e.loc().T("engine.budget.option_block"), NeedsNote: true, Command: fmt.Sprintf("bflow reject %s --gate budget --note \"<motivo>\"", id)},
		},
	}
}

// next calcula el next de rec: si la fase no tiene presupuesto, el de
// siempre (sin leer el log); si lo tiene, el gasto decide si toca preguntar
// antes que cualquier otro next (R3, R5, R15).
func (e *Engine) next(rec store.Record) output.Next {
	cfg := e.flowCfg()
	budget, has := cfg.Budget[rec.Flow.Phase]
	if !has {
		return flow.NextFor(cfg, rec.Flow)
	}
	used, err := e.phaseUsage(rec.Flow.ID, rec.Flow.Phase)
	if err != nil {
		return flow.NextFor(cfg, rec.Flow)
	}
	if _, ok := flow.BudgetDue(cfg, rec.Flow, used); ok {
		return e.budgetNext(rec.Flow.ID, rec.Flow.Phase, used, budget)
	}
	return flow.NextFor(cfg, rec.Flow)
}

// Budgets devuelve el presupuesto configurado por fase, para bflow watch.
func (e *Engine) Budgets() map[flow.Phase]int64 {
	return e.flowCfg().Budget
}

// budgetDecisionLine escribe en decisions.md la respuesta a la pregunta de
// presupuesto (R12).
func (e *Engine) budgetDecisionLine(id string, phase flow.Phase, used, budget int64, outcome, note string) error {
	line := fmt.Sprintf("- %s · %s", e.now().Format("2006-01-02"),
		e.loc().T("engine.budget.decision_line", phase, metrics.Human(used), metrics.Human(budget), outcome))
	if note != "" {
		line += ": " + note
	}
	line += fmt.Sprintf(" (%s)\n", e.User)
	return e.appendFile(id, "decisions.md", "# Decisiones en vuelo\n\n", line)
}

// approveBudget registra que la persona vio la pregunta de presupuesto y
// sigue (R7, R12).
func (e *Engine) approveBudget(id string) (Outcome, error) {
	rec, err := e.Store.Load(id)
	if err != nil {
		return Outcome{ID: id}, err
	}
	cfg := e.flowCfg()
	budget, has := cfg.Budget[rec.Flow.Phase]
	if !has {
		return Outcome{ID: id}, &flow.Rejection{Code: "no_budget_gate", Reason: e.loc().T("engine.budget.no_gate_active")}
	}
	used, err := e.phaseUsage(id, rec.Flow.Phase)
	if err != nil {
		return Outcome{ID: id}, err
	}
	k, ok := flow.BudgetDue(cfg, rec.Flow, used)
	if !ok {
		return Outcome{ID: id}, &flow.Rejection{Code: "no_budget_gate", Reason: e.loc().T("engine.budget.no_gate_active")}
	}
	out, err := e.apply(context.Background(), id, flow.Event{Kind: flow.EvApprove, Gate: flow.GateBudget, Choice: k}, "")
	if err != nil {
		return out, err
	}
	if derr := e.budgetDecisionLine(id, rec.Flow.Phase, used, budget, e.loc().T("engine.budget.outcome_continue"), ""); derr != nil {
		out.Warnings = append(out.Warnings, e.loc().T("engine.commands.decisions_write_failed", derr.Error()))
	}
	return out, nil
}

// rejectBudget bloquea la tarea con el gasto y la nota de la persona (R9).
func (e *Engine) rejectBudget(id, note string) (Outcome, error) {
	rec, err := e.Store.Load(id)
	if err != nil {
		return Outcome{ID: id}, err
	}
	cfg := e.flowCfg()
	budget, has := cfg.Budget[rec.Flow.Phase]
	if !has {
		return Outcome{ID: id}, &flow.Rejection{Code: "no_budget_gate", Reason: e.loc().T("engine.budget.no_gate_active")}
	}
	used, err := e.phaseUsage(id, rec.Flow.Phase)
	if err != nil {
		return Outcome{ID: id}, err
	}
	k, ok := flow.BudgetDue(cfg, rec.Flow, used)
	if !ok {
		return Outcome{ID: id}, &flow.Rejection{Code: "no_budget_gate", Reason: e.loc().T("engine.budget.no_gate_active")}
	}
	motivo := strings.TrimSpace(note)
	if motivo == "" {
		return Outcome{ID: id}, &flow.Rejection{Code: "note_required", Reason: e.loc().T("engine.budget.note_required")}
	}
	nota := e.loc().T("engine.budget.reject_note", rec.Flow.Phase, metrics.Human(used), metrics.Human(budget), motivo)
	out, err := e.apply(context.Background(), id, flow.Event{Kind: flow.EvReject, Gate: flow.GateBudget, Choice: k, Note: nota}, "")
	if err != nil {
		return out, err
	}
	if derr := e.budgetDecisionLine(id, rec.Flow.Phase, used, budget, e.loc().T("engine.budget.outcome_block"), motivo); derr != nil {
		out.Warnings = append(out.Warnings, e.loc().T("engine.commands.decisions_write_failed", derr.Error()))
	}
	return out, nil
}
