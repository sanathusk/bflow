package engine

import (
	"context"
	"errors"
	"slices"
	"strings"

	"innobytes.tech/bflow/internal/flow"
	"innobytes.tech/bflow/internal/store"
)

// MaxNudges son las veces que bflow hace seguir a un agente que terminó sin
// reportar. Después bloquea la tarea para que decida una persona.
const MaxNudges = 2

// Nudge decide qué pasa cuando un agente de bflow termina. Un fin de turno
// sin reporte no prueba que el trabajo terminó: si el agente trabaja en la
// fase actual y no reportó, devuelve la instrucción para que siga. Al agotar
// los intentos bloquea la tarea y devuelve "" para dejarlo terminar.
func (e *Engine) Nudge(ctx context.Context, agent string) (string, error) {
	id, err := e.Active(ctx)
	if err != nil {
		return "", nil // sin tarea en curso no hay a quién atribuirlo
	}
	fc := e.flowCfg()
	var (
		reason string
		block  bool
		n      int
	)
	_, err = e.Store.Update(id, func(rec *store.Record, exists bool) error {
		if !exists {
			return store.ErrNotFound
		}
		s := rec.Flow
		if agent == flow.ScoutAgent {
			if s.Scout != flow.ScoutPending || s.Phase == flow.Blocked {
				return nil
			}
		} else {
			if s.Gate != nil || !slices.Contains(fc.Agents[s.Phase], agent) {
				return nil
			}
			if _, reported := s.Reports[agent]; reported {
				return nil
			}
		}
		if rec.Nudges == nil {
			rec.Nudges = map[string]int{}
		}
		n = rec.Nudges[agent] + 1
		if n > MaxNudges {
			block = true
			return nil
		}
		rec.Nudges[agent] = n
		reason = e.nudgeReason(*rec, agent, n)
		return nil
	})
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	if err != nil || (reason == "" && !block) {
		return "", err
	}
	_ = e.Store.Append(store.Entry{TS: e.now(), ID: id, Event: "nudge", Agent: agent, By: e.User, Data: map[string]any{"n": n}})
	if block {
		note := e.loc().T("engine.nudge.block_note", agent, n, id)
		_, err := e.Block(ctx, id, note)
		return "", err
	}
	return reason, nil
}

func (e *Engine) nudgeReason(rec store.Record, agent string, n int) string {
	s := rec.Flow
	if agent == flow.ScoutAgent {
		return e.loc().T("engine.nudge.scout_reason", n, MaxNudges, s.ID)
	}
	vs := flow.Verdicts(s.Phase)
	names := make([]string, len(vs))
	for i, v := range vs {
		names[i] = string(v)
	}
	var b strings.Builder
	b.WriteString(e.loc().T("engine.nudge.generic_lead", n, MaxNudges, s.ID, agent, strings.Join(names, "|")))
	if s.Phase == flow.Implementing {
		if open := openTasks(e, rec); len(open) > 0 {
			b.WriteString(e.loc().T("engine.nudge.continue_tasks", strings.Join(open, "\n")))
		}
	} else {
		b.WriteString(e.loc().T("engine.nudge.continue_generic"))
	}
	b.WriteString(e.loc().T("engine.nudge.final_response_hint"))
	return b.String()
}

// openTasks devuelve hasta 5 tareas sin marcar de la spec.
func openTasks(e *Engine, rec store.Record) []string {
	tasks, err := e.specSection(rec, "tasks")
	if err != nil {
		return nil
	}
	var open []string
	for _, l := range strings.Split(tasks, "\n") {
		if t := strings.TrimSpace(l); strings.HasPrefix(t, "- [ ]") {
			open = append(open, t)
			if len(open) == 5 {
				break
			}
		}
	}
	return open
}
