package flow

import (
	"strings"
	"testing"

	"innobytes.tech/bflow/internal/i18n"
	"innobytes.tech/bflow/internal/output"
)

func optIDs(n output.Next) []string {
	var ids []string
	for _, o := range n.Options {
		ids = append(ids, o.ID)
	}
	return ids
}

func TestNextByState(t *testing.T) {
	cfg := testConfig()
	cases := []struct {
		name    string
		s       State
		action  string
		gate    string
		skill   string
		options string // ids separados por coma
		show    string
		agents  string // agentes separados por coma
	}{
		{name: "backlog", s: New("T-1", "demo"), action: "ask", gate: "lane", options: "full,light,hotfix"},
		{name: "discovery", s: stateFor(row{lane: Full, from: Discovery, gate: GateDiscovery}), action: "ask", gate: "discovery", skill: "discovery", options: "approve"},
		{name: "spec trabajando", s: stateFor(row{lane: Full, from: Spec}), action: "spawn", agents: "spec-author"},
		{name: "spec gate", s: stateFor(row{lane: Full, from: Spec, gate: GateSpec}), action: "ask", gate: "spec", skill: "approve", options: "approve,changes", show: "bflow show T-1 brief"},
		{name: "split", s: stateFor(row{lane: Full, from: Spec, gate: GateSplit}), action: "ask", gate: "split", options: "approve,keep"},
		{name: "contract full", s: stateFor(row{lane: Full, from: Contract, gate: GateContract}), action: "ask", gate: "contract", options: "approve,adjust,spec", show: "bflow show T-1 contract"},
		{name: "decision", s: stateFor(row{lane: Full, from: Implementing, gate: GateDecision}), action: "ask", gate: "decision", options: "opt1,opt2,manual,other,contract,spec"},
		{name: "decision hotfix", s: stateFor(row{lane: Hotfix, from: Implementing, gate: GateDecision}), action: "ask", gate: "decision", options: "opt1,opt2,manual,other"},
		{name: "pause", s: stateFor(row{lane: Full, from: Paused, gate: GatePause}), action: "ask", gate: "pause", options: "approve,fix"},
		{name: "quality paralelo", s: stateFor(row{lane: Full, from: Quality}), action: "spawn", agents: "reviewer,security-auditor"},
		{name: "quality falta uno", s: stateFor(row{lane: Full, from: Quality, reports: ok}), action: "spawn", agents: "security-auditor"},
		{name: "rounds full", s: stateFor(row{lane: Full, from: Implementing, gate: GateRounds, round: 2}), action: "ask", gate: "rounds", options: "split,spec,approve"},
		{name: "rounds hotfix", s: stateFor(row{lane: Hotfix, from: Implementing, gate: GateRounds, round: 2}), action: "ask", gate: "rounds", options: "split,approve"},
		{name: "walkthrough full", s: stateFor(row{lane: Full, from: Walkthrough, gate: GateWalkthrough}), action: "ask", gate: "walkthrough", skill: "walkthrough", options: "approve,fix,spec", show: "bflow show T-1 review-map"},
		{name: "walkthrough hotfix", s: stateFor(row{lane: Hotfix, from: Walkthrough, gate: GateWalkthrough}), action: "ask", gate: "walkthrough", options: "approve,fix"},
		{name: "repro hotfix", s: reproGateState(), action: "ask", gate: "repro", options: "approve,test"},
		{name: "in_review", s: stateFor(row{lane: Full, from: InReview}), action: "wait"},
		{name: "done", s: stateFor(row{lane: Full, from: Done}), action: "done"},
		{name: "blocked", s: stateFor(row{lane: Full, from: Blocked}), action: "ask", gate: "blocked", options: "unblock,keep"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n := NextFor(cfg, c.s)
			if n.Action != c.action {
				t.Fatalf("action %q, want %q", n.Action, c.action)
			}
			if n.Gate != c.gate {
				t.Errorf("gate %q, want %q", n.Gate, c.gate)
			}
			if c.skill != "" && n.Skill != c.skill {
				t.Errorf("skill %q, want %q", n.Skill, c.skill)
			}
			if c.options != "" && strings.Join(optIDs(n), ",") != c.options {
				t.Errorf("opciones %v, want %s", optIDs(n), c.options)
			}
			if c.show != "" && (len(n.Show) != 1 || n.Show[0] != c.show) {
				t.Errorf("show %v, want %s", n.Show, c.show)
			}
			if c.agents != "" {
				var names []string
				for _, a := range n.Agents {
					names = append(names, a.Agent)
				}
				if strings.Join(names, ",") != c.agents {
					t.Errorf("agentes %v, want %s", names, c.agents)
				}
			}
			if n.Action == "ask" {
				if n.Question == "" {
					t.Error("una pregunta sin texto")
				}
				for _, o := range n.Options {
					if o.ID != "keep" && !strings.HasPrefix(o.Command, "bflow ") {
						t.Errorf("opción %q sin comando bflow: %q", o.ID, o.Command)
					}
					if o.NeedsNote && !strings.Contains(o.Command, "<") {
						t.Errorf("opción %q pide nota pero el comando no tiene marcador: %q", o.ID, o.Command)
					}
				}
			}
		})
	}
}

// Cada comando que Next ofrece tiene que ser aceptado por Apply: si la skill
// ejecuta la opción tal cual (con la nota rellenada), el flujo avanza.
func TestNextOptionsAreExecutable(t *testing.T) {
	cfg := testConfig()
	states := []State{
		stateFor(row{lane: Full, from: Spec, gate: GateSpec}),
		stateFor(row{lane: Full, from: Spec, gate: GateSplit}),
		stateFor(row{lane: Full, from: Contract, gate: GateContract}),
		stateFor(row{lane: Full, from: Implementing, gate: GateDecision}),
		stateFor(row{lane: Full, from: Paused, gate: GatePause}),
		stateFor(row{lane: Full, from: Implementing, gate: GateRounds, round: 2}),
		stateFor(row{lane: Light, from: Walkthrough, gate: GateWalkthrough}),
		stateFor(row{lane: Full, from: Discovery, gate: GateDiscovery}),
		New("T-1", "demo"),
	}
	for _, s := range states {
		for _, o := range NextFor(cfg, s).Options {
			ev, ok := eventFromCommand(o.Command)
			if !ok {
				continue
			}
			if ev.Kind == EvApprove && ev.Gate == GateSplit {
				ev.Children = []string{"T-2 · hija"} // las crea engine antes de aplicar el evento
			}
			if _, err := Apply(cfg, s, ev); err != nil {
				t.Errorf("%s[%s] opción %q (%s): %v", s.Phase, gateName(s), o.ID, o.Command, err)
			}
		}
	}
}

// eventFromCommand traduce un comando de opción al evento que produciría.
// Es un parser mínimo para la prueba; el real vive en la CLI.
func eventFromCommand(c string) (Event, bool) {
	f := strings.Fields(c)
	if len(f) < 3 {
		return Event{}, false
	}
	var ev Event
	switch f[1] {
	case "approve":
		ev.Kind = EvApprove
	case "reject":
		ev.Kind = EvReject
	case "start":
		ev.Kind = EvStart
	case "block":
		ev.Kind = EvBlock
	case "unblock":
		ev.Kind = EvUnblock
	default:
		return ev, false
	}
	for i := 3; i < len(f); i++ {
		val := ""
		if i+1 < len(f) {
			val = f[i+1]
		}
		switch f[i] {
		case "--gate":
			ev.Gate = Gate(val)
		case "--to":
			ev.To = Phase(val)
		case "--lane":
			ev.Lane = Lane(val)
		case "--choice":
			ev.Choice = int(val[0] - '0')
		case "--note", "--reason":
			ev.Note = "texto del humano"
		case "--manual":
			ev.Manual = true
		case "--file":
			ev.Attachment = "discovery"
		}
	}
	return ev, true
}

func TestSpawnArgs(t *testing.T) {
	s := stateFor(row{lane: Full, from: Implementing, gate: GateDecision})
	res, err := Apply(testConfig(), s, approveChoice(1))
	if err != nil {
		t.Fatal(err)
	}
	n := res.Next
	if n.Action != "spawn" || len(n.Agents) != 1 {
		t.Fatalf("next: %+v", n)
	}
	a := n.Agents[0]
	want := map[string]string{"id": "T-1", "lane": "full", "phase": "implementing",
		"spec": "specs/T-1-demo/spec.md", "decision": "opción A", "resume": "true"}
	for k, v := range want {
		if a.Args[k] != v {
			t.Errorf("args[%s]=%q, want %q", k, a.Args[k], v)
		}
	}
	if !strings.Contains(a.Report, "--verdict DONE|NEEDS_DECISION|BLOCKED") {
		t.Errorf("report: %s", a.Report)
	}
}

func TestChangelogPath(t *testing.T) {
	c := testConfig()
	if got := c.ChangelogPath("T-1", "demo"); got != "specs/T-1-demo/consumer-changelog.md" {
		t.Errorf("por defecto va junto a la spec: %s", got)
	}
	c.Changelog = "harness/frontend/{id}-{slug}-changelog.md"
	if got := c.ChangelogPath("T-1", "demo"); got != "harness/frontend/T-1-demo-changelog.md" {
		t.Errorf("plantilla del repo: %s", got)
	}
	n := NextFor(c, State{ID: "T-1", Slug: "demo", Lane: Full, Phase: Documenting})
	if len(n.Agents) == 0 || n.Agents[0].Args["changelog"] != "harness/frontend/T-1-demo-changelog.md" {
		t.Errorf("el documenter recibe la ruta del changelog: %+v", n.Agents)
	}
}

func TestSpecGateShowsUIBlueprint(t *testing.T) {
	s := stateFor(row{lane: Full, from: Spec, gate: GateSpec})
	cfg := testConfig()
	if n := NextFor(cfg, s); len(n.Show) != 1 {
		t.Errorf("sin UI solo el brief: %v", n.Show)
	}
	cfg.UI = true
	n := NextFor(cfg, s)
	if len(n.Show) != 2 || n.Show[1] != "bflow show T-1 spec --section ui-blueprint" {
		t.Errorf("con UI el humano ve el blueprint al aprobar: %v", n.Show)
	}
}

func TestUpcoming(t *testing.T) {
	cfg := DefaultConfig()
	st := func(lane Lane, p Phase, g Gate) State {
		s := State{ID: "T-1", Lane: lane, Phase: p}
		if g != "" {
			s.Gate = &PendingGate{Name: g, Agent: "implementer"}
		}
		return s
	}
	for _, c := range []struct {
		s    State
		want string
	}{
		{st(Light, Spec, ""), "spec gate=spec"},
		{st(Light, Spec, GateSpec), "implementing agentes=implementer"},
		{st(Full, Discovery, GateDiscovery), "spec agentes=spec-author"},
		{st(Full, Spec, GateSpec), "contract agentes=implementer"},
		{st(Full, Contract, ""), "implementing agentes=implementer"},
		{st(Full, Contract, GateContract), "implementing agentes=implementer"},
		{st(Full, Implementing, ""), "quality agentes=reviewer"},
		{st(Full, Paused, GatePause), "quality agentes=reviewer"},
		{st(Light, Implementing, ""), "quality agentes=reviewer"},
		{st(Light, Implementing, GateDecision), "implementing agentes=implementer"},
		{st(Light, Quality, ""), "documenting agentes=documenter"},
		{st(Light, Documenting, ""), "walkthrough gate=questions"},
		{st(Light, Walkthrough, GateQuestions), "walkthrough gate=walkthrough"},
		{st(Light, Walkthrough, GateWalkthrough), "in_review merge"},
		{st(Light, InReview, ""), "done"},
		{st(Light, Blocked, ""), ""},
		{st(Light, Done, ""), ""},
	} {
		u := Upcoming(cfg, c.s)
		got := string(u.Phase)
		switch {
		case u.Gate != "":
			got += " gate=" + string(u.Gate)
		case len(u.Agents) > 0:
			got += " agentes=" + strings.Join(u.Agents, ",")
		case u.Merge:
			got += " merge"
		}
		if got != c.want {
			t.Errorf("%s/%s gate %v: %q, want %q", c.s.Lane, c.s.Phase, c.s.Gate, got, c.want)
		}
	}
}

func TestIntakeNext(t *testing.T) {
	n := IntakeNext(testConfig())
	if n.Action != "ask" || n.Gate != "intake" || n.Skill != "intake" {
		t.Fatalf("next: %+v", n)
	}
	if !strings.Contains(n.Question, "¿Creamos una tarea nueva") && !strings.Contains(n.Question, "Create a new task") {
		t.Errorf("pregunta: %q", n.Question)
	}
	if got := strings.Join(optIDs(n), ","); got != "new,existing" {
		t.Fatalf("opciones %q", got)
	}
	if c := n.Options[0].Command; c != `bflow new --lane <carril> --title "<título>" --file <idea.md>` {
		t.Errorf("comando new: %q", c)
	}
	if c := n.Options[1].Command; c != "bflow start <ID> --lane <carril>" {
		t.Errorf("comando existing: %q", c)
	}
	for _, o := range n.Options {
		if !strings.Contains(o.Description, "full, light, hotfix") {
			t.Errorf("%s: la descripción lista los carriles en orden: %q", o.ID, o.Description)
		}
	}
	light := DefaultConfig()
	delete(light.Lanes, Full)
	if d := IntakeNext(light).Options[0].Description; strings.Contains(d, "full") {
		t.Errorf("solo carriles configurados: %q", d)
	}
}

func TestSuggestClear(t *testing.T) {
	ask := output.Next{Action: output.ActionAsk}
	if !SuggestClear(Contract, ask) {
		t.Error("cambio de fase con next ask: clear")
	}
	if !SuggestClear(Implementing, output.Next{Action: output.ActionSpawn}) {
		t.Error("cambio de fase con spawn: clear")
	}
	if SuggestClear("", ask) {
		t.Error("sin cambio de fase: no clear")
	}
	if SuggestClear(Done, output.Next{Action: output.ActionDone}) {
		t.Error("next done: no clear")
	}
}

func TestDecisionOptionWarnsFreezeAllow(t *testing.T) {
	s := stateFor(row{lane: Light, from: Implementing, gate: GateDecision})
	s.Gate.Options = []string{"Aprobar el cambio con `bflow freeze --allow internal/x_test.go`", "Dejarlo así"}
	n := NextFor(testConfig(), s)
	if d := n.Options[0].Description; !strings.Contains(d, "internal/x_test.go") || !strings.Contains(d, "bflow freeze --allow") {
		t.Errorf("description: %q", d)
	}
	if d := n.Options[1].Description; d != "" {
		t.Errorf("sin comando no hay aviso: %q", d)
	}
}

func TestAllowFiles(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"bflow freeze --allow internal/a_test.go", "internal/a_test.go"},
		{"corre `bflow freeze --allow 'internal/a_test.go'` ya", "internal/a_test.go"},
		{`freeze --allow "internal\cli\a_test.go"`, "internal/cli/a_test.go"},
		{"freeze --allow a_test.go y freeze  --allow b_test.go y freeze --allow a_test.go", "a_test.go,b_test.go"},
		{"sin comando, solo --allow", ""},
	}
	for _, c := range cases {
		if got := strings.Join(AllowFiles(c.in), ","); got != c.want {
			t.Errorf("%q: %q, want %q", c.in, got, c.want)
		}
	}
}

func TestManualDecisionBlocksInsteadOfRelaunching(t *testing.T) {
	cfg := testConfig()
	s := stateFor(row{lane: Full, from: Implementing, gate: GateDecision})
	res, err := Apply(cfg, s, Event{Kind: EvApprove, Gate: GateDecision, Choice: 2, Manual: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.State.Phase != Blocked || res.Next.Action == "spawn" {
		t.Fatalf("debía bloquear sin lanzar agente: %s %+v", res.State.Phase, res.Next)
	}
	if !strings.Contains(res.State.Block.Reason, "opción B") {
		t.Errorf("el motivo no dice la decisión: %q", res.State.Block.Reason)
	}
	if !res.State.Block.Manual {
		t.Error("el bloqueo de decisión manual debe quedar con Manual")
	}
	u, err := Apply(cfg, res.State, Event{Kind: EvUnblock, Note: "el reviewer aprobó"})
	if err != nil {
		t.Fatal(err)
	}
	a := u.Next.Agents[0]
	if u.Next.Action != "spawn" || a.Args["note"] != "el reviewer aprobó" || a.Args["decision"] != "opción B" || a.Args["resume"] != "true" {
		t.Errorf("args tras unblock: %+v", a.Args)
	}
	if _, err := Apply(cfg, stateFor(row{lane: Full, from: Contract, gate: GateContract}), Event{Kind: EvApprove, Manual: true}); err == nil {
		t.Error("--manual fuera de decision debe fallar")
	}
}

func TestUnblockWithoutNoteKeepsNoteEmpty(t *testing.T) {
	cfg := testConfig()
	b, _ := Apply(cfg, stateFor(row{lane: Full, from: Implementing}), Event{Kind: EvBlock, Note: "x"})
	u, err := Apply(cfg, b.State, Event{Kind: EvUnblock})
	if err != nil {
		t.Fatal(err)
	}
	if u.State.Note != "" {
		t.Errorf("note %q", u.State.Note)
	}
}

func TestBlockedUnblockNote(t *testing.T) {
	cfg := testConfig()
	find := func(n output.Next) output.Option {
		for _, o := range n.Options {
			if o.ID == "unblock" {
				return o
			}
		}
		t.Fatal("sin opción unblock")
		return output.Option{}
	}
	b, err := Apply(cfg, stateFor(row{lane: Full, from: Implementing}), Event{Kind: EvBlock, Note: "falta acceso"})
	if err != nil {
		t.Fatal(err)
	}
	if b.State.Block.Manual {
		t.Error("bflow block no es manual")
	}
	o := find(NextFor(cfg, b.State))
	if o.NeedsNote || o.Command != "bflow unblock T-1" {
		t.Errorf("bloqueo normal: %+v", o)
	}
	m, err := Apply(cfg, stateFor(row{lane: Full, from: Implementing, gate: GateDecision}), Event{Kind: EvApprove, Gate: GateDecision, Choice: 2, Manual: true})
	if err != nil {
		t.Fatal(err)
	}
	o = find(NextFor(cfg, m.State))
	if !o.NeedsNote || !strings.Contains(o.Command, "--note") {
		t.Errorf("bloqueo manual: %+v", o)
	}
}

func TestNextLocalization(t *testing.T) {
	b, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfgEn := testConfig()
	cfgEn.Loc = b.New("en")
	nextEn := NextFor(cfgEn, State{ID: "T-1", Phase: Done})
	if nextEn.Reason != "task closed" {
		t.Errorf("en reason: %q, want 'task closed'", nextEn.Reason)
	}

	cfgEs := testConfig()
	cfgEs.Loc = b.New("es")
	nextEs := NextFor(cfgEs, State{ID: "T-1", Phase: Done})
	if nextEs.Reason != "tarea cerrada" {
		t.Errorf("es reason: %q, want 'tarea cerrada'", nextEs.Reason)
	}
}
