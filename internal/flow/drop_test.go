package flow

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func dropEv(note string) Event { return Event{Kind: EvDrop, Note: note} }

func commentBodies(fx []Effect) []string {
	var out []string
	for _, f := range fx {
		if f.Kind == FxComment {
			out = append(out, f.Body)
		}
	}
	return out
}

// R1: drop retira desde cualquier fase empezada que no sea done, con gate o sin él.
func TestDropFromAnyStartedPhase(t *testing.T) {
	cfg := testConfig()
	for _, p := range Order {
		if p == Done {
			continue
		}
		for _, g := range []Gate{"", GateDecision} {
			s := stateFor(row{lane: Full, from: p, gate: g})
			res, err := Apply(cfg, s, dropEv("ya no hace falta"))
			if err != nil {
				t.Fatalf("%s/%q: %v", p, g, err)
			}
			if res.State.Phase != Dropped || res.State.Gate != nil || res.State.Block != nil {
				t.Errorf("%s/%q: %+v", p, g, res.State)
			}
			if got := kinds(res.Effects); !slices.Equal(got, []EffectKind{ts, cm}) {
				t.Errorf("%s/%q efectos %v", p, g, got)
			}
			if res.Effects[0].Phase != Dropped {
				t.Errorf("tracker_state a %q, want dropped", res.Effects[0].Phase)
			}
			if bs := commentBodies(res.Effects); len(bs) != 1 || bs[0] != "**Retirada:** ya no hace falta" {
				t.Errorf("comentario: %q", bs)
			}
		}
	}
}

func TestDropFromBlocked(t *testing.T) {
	s := stateFor(row{lane: Full, from: Blocked})
	res, err := Apply(testConfig(), s, dropEv("se cancela"))
	if err != nil {
		t.Fatal(err)
	}
	if res.State.Phase != Dropped || res.State.Block != nil {
		t.Errorf("blocked → %+v", res.State)
	}
}

// R2 y R13 en flow: sin motivo no cambia nada; en backlog se rechaza.
func TestDropNeedsNote(t *testing.T) {
	s := stateFor(row{lane: Full, from: Implementing})
	for _, note := range []string{"", "   \t"} {
		res, err := Apply(testConfig(), s, dropEv(note))
		var rj *Rejection
		if !errors.As(err, &rj) || rj.Code != "note_required" {
			t.Fatalf("nota %q: err=%v", note, err)
		}
		if res.State.Phase != Implementing {
			t.Errorf("el estado no cambia: %s", res.State.Phase)
		}
	}
	_, err := Apply(testConfig(), New("T-1", "x"), dropEv("motivo"))
	var rj *Rejection
	if !errors.As(err, &rj) || rj.Code != "not_started" {
		t.Errorf("backlog: %v", err)
	}
}

// R3: una tarea retirada rechaza cualquier evento.
func TestDroppedRejectsEvents(t *testing.T) {
	s := State{ID: "T-1", Slug: "x", Lane: Full, Phase: Dropped, Started: true}
	for _, ev := range []Event{
		{Kind: EvStart, Lane: Full}, {Kind: EvApprove}, {Kind: EvReject, Note: "x"}, rep(imp, DoneV),
		{Kind: EvBlock, Note: "x"}, {Kind: EvUnblock}, {Kind: EvMerged}, {Kind: EvClosedOutside, Reason: "tracker"}, dropEv("otra vez"),
	} {
		_, err := Apply(testConfig(), s, ev)
		var rj *Rejection
		if !errors.As(err, &rj) || rj.Code != "task_dropped" {
			t.Errorf("%s: err=%v, want task_dropped", ev.Kind, err)
		}
	}
}

func TestNextForDropped(t *testing.T) {
	n := NextFor(testConfig(), State{ID: "T-1", Phase: Dropped})
	if n.Action != "done" || (n.Reason != "task dropped" && n.Reason != "tarea retirada") {
		t.Errorf("next: %+v", n)
	}
}

// R21, R22: aprobar el split con hijas retira a la madre y lista las hijas.
func TestApproveSplitDropsWithChildren(t *testing.T) {
	s := stateFor(row{lane: Full, from: Spec, gate: GateSplit})
	ev := Event{Kind: EvApprove, Children: []string{"GH-10 · Uno", "GH-11 · Dos"}}
	res, err := Apply(testConfig(), s, ev)
	if err != nil {
		t.Fatal(err)
	}
	if res.State.Phase != Dropped || res.State.Gate != nil || res.State.Block != nil {
		t.Errorf("estado: %+v", res.State)
	}
	if got := kinds(res.Effects); !slices.Equal(got, []EffectKind{ts, cm}) || res.Effects[0].Phase != Dropped {
		t.Errorf("efectos %+v", res.Effects)
	}
	want := "**Dividida en:**\n- GH-10 · Uno\n- GH-11 · Dos"
	if bs := commentBodies(res.Effects); len(bs) != 1 || bs[0] != want {
		t.Errorf("comentario %q, want %q", bs, want)
	}
}

func TestApproveSplitNeedsChildren(t *testing.T) {
	s := stateFor(row{lane: Full, from: Spec, gate: GateSplit})
	res, err := Apply(testConfig(), s, Event{Kind: EvApprove})
	var rj *Rejection
	if !errors.As(err, &rj) || rj.Code != "split_children" {
		t.Fatalf("err=%v", err)
	}
	if res.State.Phase != Spec || res.State.Gate == nil {
		t.Errorf("la madre sigue en el gate: %+v", res.State)
	}
}

// R16: el gate split muestra el Brief y su opción dice qué hará.
func TestSplitGateShowsBrief(t *testing.T) {
	n := NextFor(testConfig(), stateFor(row{lane: Full, from: Spec, gate: GateSplit}))
	if len(n.Show) != 1 || n.Show[0] != "bflow show T-1 brief" {
		t.Errorf("show: %v", n.Show)
	}
	if len(n.Options) == 0 || n.Options[0].ID != "approve" || (n.Options[0].Label != "Split: create child tasks and drop T-1" && n.Options[0].Label != "Dividir: crear las hijas y retirar T-1") {
		t.Errorf("opciones: %+v", n.Options)
	}
	if !strings.Contains(n.Options[0].Command, "approve T-1 --gate split") {
		t.Errorf("comando: %s", n.Options[0].Command)
	}
}
