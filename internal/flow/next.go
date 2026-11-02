package flow

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"innobytes.tech/bflow/internal/output"
)

// SpecPath es la ruta del spec de una tarea, relativa a la raíz del repo.
func SpecPath(id, slug string) string {
	if slug == "" {
		return "specs/" + id + "/spec.md"
	}
	return "specs/" + id + "-" + slug + "/spec.md"
}

// TaskDir es la carpeta de trabajo (ignorada por git) de una tarea.
func TaskDir(id string) string { return ".bflow/tasks/" + id }

// AllowRe encuentra `freeze --allow <archivo>` en el texto de una decisión.
var AllowRe = regexp.MustCompile("freeze\\s+--allow\\s+[`'\"]?([^\\s`'\"]+)")

// AllowFiles son los archivos que el texto pide dejar cambiar con
// `freeze --allow`, con `/`, sin repetidos y en orden de aparición.
func AllowFiles(text string) []string {
	var out []string
	for _, m := range AllowRe.FindAllStringSubmatch(text, -1) {
		f := strings.ReplaceAll(m[1], `\`, "/")
		if !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out
}

// NextFor dice qué debe hacer la sesión principal con la tarea en el estado s.
func NextFor(cfg Config, s State) output.Next {
	switch s.Phase {
	case Done:
		return output.Next{Action: output.ActionDone, Reason: cfg.t("flow.task_closed")}
	case Dropped:
		return output.Next{Action: output.ActionDone, Reason: cfg.t("flow.task_dropped")}
	case InReview:
		return output.Next{Action: output.ActionWait,
			Reason: cfg.t("flow.wait_merge")}
	case Blocked:
		reason := ""
		if s.Block != nil {
			reason = s.Block.Reason
		}
		unblock := output.Option{ID: "unblock", Label: cfg.t("flow.option_unblock"), Command: cmd("unblock", s.ID)}
		if s.Block != nil && s.Block.Manual {
			unblock = output.Option{ID: "unblock", Label: cfg.t("flow.option_unblock_manual"), NeedsNote: true, Command: cmd("unblock", s.ID, `--note "<resultado>"`)}
		}
		return output.Next{Action: output.ActionAsk, Gate: "blocked",
			Question: cfg.t("flow.blocked_question", s.ID, reason),
			Options: []output.Option{
				unblock,
				{ID: "keep", Label: cfg.t("flow.option_keep_blocked")},
			}}
	case Backlog:
		return output.Next{Action: output.ActionAsk, Gate: string(GateLane),
			Question: cfg.t("flow.gate_lane_question", s.ID),
			Options:  laneOptions(cfg, s.ID)}
	}
	if s.Scout == ScoutPending {
		return scoutNext(s)
	}
	if s.Gate != nil {
		return gateNext(cfg, s)
	}
	return spawnNext(cfg, s)
}

func cmd(verb, id string, extra ...string) string {
	return strings.Join(append([]string{"bflow", verb, id}, extra...), " ")
}

func laneHelp(cfg Config, l Lane) string {
	switch l {
	case Full:
		return cfg.t("flow.lane_full_desc")
	case Light:
		return cfg.t("flow.lane_light_desc")
	case Hotfix:
		return cfg.t("flow.lane_hotfix_desc")
	}
	return ""
}

func laneOptions(cfg Config, id string) []output.Option {
	var out []output.Option
	for _, l := range []Lane{Full, Light, Hotfix} {
		if _, ok := cfg.Lanes[l]; ok {
			out = append(out, output.Option{ID: string(l), Label: string(l), Description: laneHelp(cfg, l),
				Command: cmd("start", id, "--lane", string(l))})
		}
	}
	for l := range cfg.Lanes {
		if l != Full && l != Light && l != Hotfix {
			out = append(out, output.Option{ID: string(l), Label: string(l), Command: cmd("start", id, "--lane", string(l))})
		}
	}
	return out
}

// IntakeGate es la pregunta de entrada de una idea sin tarea; no es una Gate
// de la máquina de estados.
const IntakeGate = "intake"

// IntakeNext pregunta si la idea va a una tarea nueva o a una que ya existe.
func IntakeNext(cfg Config) output.Next {
	var lanes []string
	for _, o := range laneOptions(cfg, "") {
		lanes = append(lanes, o.ID)
	}
	desc := cfg.t("flow.intake_lanes_prefix") + strings.Join(lanes, ", ")
	return output.Next{Action: output.ActionAsk, Gate: IntakeGate, Skill: "intake",
		Question: cfg.t("flow.intake_question"),
		Options: []output.Option{
			{ID: "new", Label: cfg.t("flow.intake_new_label"), Description: desc,
				Command: `bflow new --lane <carril> --title "<título>" --file <idea.md>`},
			{ID: "existing", Label: cfg.t("flow.intake_existing_label"), Description: desc,
				Command: "bflow start <ID> --lane <carril>"},
		}}
}

func approveOpt(id string, g Gate, label string) output.Option {
	return output.Option{ID: "approve", Label: label, Command: cmd("approve", id, "--gate", string(g))}
}

func rejectOpt(id string, g Gate, optID, label string, to Phase) output.Option {
	extra := []string{"--gate", string(g)}
	if to != "" {
		extra = append(extra, "--to", string(to))
	}
	extra = append(extra, `--note "<motivo>"`)
	return output.Option{ID: optID, Label: label, NeedsNote: true, Command: cmd("reject", id, extra...)}
}

func gateNext(cfg Config, s State) output.Next {
	id, g := s.ID, s.Gate
	n := output.Next{Action: output.ActionAsk, Gate: string(g.Name)}
	switch g.Name {
	case GateDiscovery:
		n.Skill = "discovery"
		n.Question = cfg.t("flow.gate_discovery_question")
		n.Options = []output.Option{{ID: "approve", Label: cfg.t("flow.gate_discovery_close"),
			Command: cmd("approve", id, "--gate", "discovery", "--stdin")}}
	case GateSpec:
		n.Skill = "approve"
		n.Show = []string{cmd("show", id, "brief")}
		if cfg.UI {
			n.Show = append(n.Show, cmd("show", id, "spec", "--section", "ui-blueprint"))
		}
		n.Question = cfg.t("flow.gate_spec_question")
		n.Options = []output.Option{
			approveOpt(id, g.Name, cfg.t("flow.option_approve")),
			rejectOpt(id, g.Name, "changes", cfg.t("flow.option_changes"), ""),
		}
	case GateSplit:
		n.Question = cfg.t("flow.gate_split_question", g.Note)
		n.Show = []string{cmd("show", id, "brief")}
		n.Options = []output.Option{
			approveOpt(id, g.Name, cfg.t("flow.option_split_approve", id)),
			rejectOpt(id, g.Name, "keep", cfg.t("flow.option_split_keep"), ""),
		}
	case GateContract:
		n.Show = []string{cmd("show", id, "contract")}
		n.Question = cfg.t("flow.gate_contract_question")
		n.Options = []output.Option{
			approveOpt(id, g.Name, cfg.t("flow.option_continue")),
			rejectOpt(id, g.Name, "adjust", cfg.t("flow.option_contract_adjust"), ""),
		}
		if cfg.has(s.Lane, Spec) {
			n.Options = append(n.Options, rejectOpt(id, g.Name, "spec", cfg.t("flow.option_back_to_spec"), Spec))
		}
	case GateDecision:
		n.Question = cfg.t("flow.gate_decision_question", g.Agent, g.Note)
		for i, o := range g.Options {
			opt := output.Option{ID: "opt" + strconv.Itoa(i+1), Label: o,
				Command: cmd("approve", id, "--gate", "decision", "--choice", strconv.Itoa(i+1))}
			var lines []string
			for _, f := range AllowFiles(o) {
				lines = append(lines, cfg.t("flow.gate_decision_allow_instruction", f))
			}
			opt.Description = strings.Join(lines, "\n")
			n.Options = append(n.Options, opt)
		}
		n.Options = append(n.Options, output.Option{ID: "manual", Label: cfg.t("flow.option_manual_decision"), NeedsNote: true,
			Command: cmd("approve", id, "--gate", "decision", "--manual", `--note "<qué hacer>"`)})
		n.Options = append(n.Options, output.Option{ID: "other", Label: cfg.t("flow.option_other_decision"), NeedsNote: true,
			Command: cmd("approve", id, "--gate", "decision", `--note "<decisión>"`)})
		for _, p := range decisionTargets(cfg, s.Lane, s.Phase) {
			label := cfg.t("flow.option_contract_redo")
			if p == Spec {
				label = cfg.t("flow.option_back_to_spec")
			}
			n.Options = append(n.Options, rejectOpt(id, GateDecision, string(p), label, p))
		}
	case GatePause:
		n.Question = cfg.t("flow.gate_pause_question")
		n.Options = []output.Option{
			approveOpt(id, g.Name, cfg.t("flow.option_launch_review")),
			rejectOpt(id, g.Name, "fix", cfg.t("flow.option_back_to_implement"), ""),
		}
	case GateRounds:
		n.Question = cfg.t("flow.gate_rounds_question", s.Round)
		n.Options = []output.Option{
			{ID: "split", Label: cfg.t("flow.option_split_feature"), Command: cmd("block", id, `--reason "dividir la feature"`)},
		}
		if cfg.has(s.Lane, Spec) {
			n.Options = append(n.Options, rejectOpt(id, g.Name, "spec", cfg.t("flow.option_back_to_spec"), ""))
		}
		n.Options = append(n.Options, approveOpt(id, g.Name, cfg.t("flow.option_one_more_round")))
	case GateRepro:
		n.Question = cfg.t("flow.gate_repro_question", g.Agent, g.Note)
		n.Options = []output.Option{
			approveOpt(id, g.Name, cfg.t("flow.option_accept_no_test")),
			rejectOpt(id, g.Name, "test", cfg.t("flow.option_request_repro_test"), ""),
		}
	case GateQuestions:
		n.Skill = "walkthrough"
		n.Show = []string{cmd("show", id, "questions")}
		n.Question = cfg.t("flow.gate_questions_question")
		n.Options = []output.Option{
			{ID: "answer", Label: cfg.t("flow.option_already_answered"), NeedsNote: true, Command: cmd("approve", id, "--gate", string(g.Name), `--note "<respuestas>"`)},
			approveOpt(id, g.Name, cfg.t("flow.option_skip_questions")),
		}
	case GateWalkthrough:
		n.Skill = "walkthrough"
		n.Show = []string{cmd("show", id, "review-map")}
		n.Question = cfg.t("flow.gate_walkthrough_question")
		n.Options = []output.Option{
			approveOpt(id, g.Name, cfg.t("flow.option_approve_and_open_pr")),
			rejectOpt(id, g.Name, "fix", cfg.t("flow.option_not_convinced"), ""),
		}
		if cfg.has(s.Lane, Spec) {
			n.Options = append(n.Options, rejectOpt(id, g.Name, "spec", cfg.t("flow.option_back_to_spec"), Spec))
		}
	}
	return n
}

func scoutNext(s State) output.Next {
	args := map[string]string{"id": s.ID, "lane": string(s.Lane), "phase": string(s.Phase)}
	return output.Next{Action: output.ActionSpawn, Agents: []output.AgentCall{{
		Agent: ScoutAgent, Subagent: SubagentPrefix + ScoutAgent, Args: args,
		Report: cmd("report", s.ID, "--agent", ScoutAgent, "--verdict", joinVerdicts(ScoutVerdicts), "--stdin")}}}
}

func spawnNext(cfg Config, s State) output.Next {
	agents := cfg.Agents[s.Phase]
	var pending []string
	for _, a := range agents {
		if _, done := s.Reports[a]; !done {
			pending = append(pending, a)
		}
	}
	if len(pending) == 0 {
		return output.Next{Action: output.ActionDone, Reason: cfg.t("flow.no_work_pending_phase", string(s.Phase))}
	}
	parallel := s.Phase == Quality
	if !parallel {
		pending = pending[:1]
	}
	n := output.Next{Action: output.ActionSpawn, Parallel: parallel && len(pending) > 1}
	for _, a := range pending {
		args := map[string]string{
			"id":    s.ID,
			"lane":  string(s.Lane),
			"phase": string(s.Phase),
		}
		if cfg.has(s.Lane, Spec) {
			args["spec"] = SpecPath(s.ID, s.Slug)
		}
		if s.Round > 0 {
			args["round"] = strconv.Itoa(s.Round)
		}
		if s.Note != "" {
			args["note"] = s.Note
		}
		if s.Decision != "" {
			args["decision"] = s.Decision
		}
		if s.Phase == Documenting {
			args["changelog"] = cfg.ChangelogPath(s.ID, s.Slug)
		}
		if s.Resume && !parallel {
			args["resume"] = "true"
		}
		n.Agents = append(n.Agents, output.AgentCall{Agent: a, Subagent: SubagentPrefix + a, Args: args,
			Report: cmd("report", s.ID, "--agent", a, "--verdict", joinVerdicts(Verdicts(s.Phase)))})
	}
	return n
}

// Step es lo que viene después del paso actual, para el panel de una persona:
// una gate que decide ella, agentes que trabajan en una fase, o el merge.
type Step struct {
	Phase  Phase
	Gate   Gate     // decide la persona
	Agents []string // o trabajan estos agentes
	Merge  bool     // o se espera el merge del PR
}

// Upcoming dice qué viene después del paso actual si todo sale bien. Vacío
// si no hay un siguiente paso claro (terminada, bloqueada).
func Upcoming(cfg Config, s State) Step {
	enter := func(p Phase) Step {
		switch p {
		case Discovery:
			return Step{Phase: p, Gate: GateDiscovery}
		case Paused:
			return Step{Phase: p, Gate: GatePause}
		case Walkthrough:
			return Step{Phase: p, Gate: GateQuestions}
		case InReview:
			return Step{Phase: p, Merge: true}
		case Done:
			return Step{Phase: p}
		}
		return Step{Phase: p, Agents: cfg.Agents[p]}
	}
	if s.Scout == ScoutPending {
		return enter(s.Phase)
	}
	if g := s.Gate; g != nil {
		switch g.Name {
		case GateDiscovery:
			return enter(cfg.after(s.Lane, Discovery))
		case GateSpec:
			return enter(cfg.after(s.Lane, Spec))
		case GateContract:
			return enter(cfg.after(s.Lane, Contract))
		case GatePause:
			return enter(cfg.afterPause(s.Lane))
		case GateQuestions:
			return Step{Phase: Walkthrough, Gate: GateWalkthrough}
		case GateWalkthrough:
			return enter(cfg.after(s.Lane, Walkthrough))
		case GateSplit:
			return Step{}
		}
		agents := cfg.Agents[s.Phase] // decision, rounds: el agente retoma
		if g.Agent != "" {
			agents = []string{g.Agent}
		}
		return Step{Phase: s.Phase, Agents: agents}
	}
	switch s.Phase {
	case Spec:
		return Step{Phase: Spec, Gate: GateSpec}
	case Contract:
		if cfg.ContractGate {
			return Step{Phase: Contract, Gate: GateContract}
		}
		return enter(cfg.after(s.Lane, Contract))
	case InReview:
		return Step{Phase: Done}
	case Implementing:
		if s.PauseRequested {
			return enter(Paused)
		}
		return enter(cfg.after(s.Lane, Implementing))
	case Quality, Documenting:
		return enter(cfg.after(s.Lane, s.Phase))
	}
	return Step{}
}

// SuggestClear dice si, tras un approve que llevó la tarea a la fase to
// (vacío si no cambió), conviene descartar la conversación.
func SuggestClear(to Phase, n output.Next) bool {
	return to != "" && n.Action != output.ActionDone
}
