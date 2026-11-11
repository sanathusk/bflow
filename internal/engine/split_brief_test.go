package engine

import (
	"reflect"
	"testing"

	"innobytes.tech/bflow/internal/i18n"
)

// La `### División` no tiene que ser lo último del Brief: un párrafo sin
// sangría (Riesgos, Tamaño) cierra la lista.
func TestParseSplitBeforeOtherBriefBlocks(t *testing.T) {
	brief := "**Objetivo:** x\n\n### División\n- **A**: uno\n  más\n- **B**: dos\n\n**Riesgos**\n- r1\n\n**Tamaño:** grande\n"
	got, err := ParseSplit(brief)
	if err != nil {
		t.Fatal(err)
	}
	want := []SplitPart{{"A", "uno\nmás"}, {"B", "dos"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestCreatedListKeepsOrder(t *testing.T) {
	ids := map[string]string{"Uno": "GH-1", "Dos": "GH-2", "Tres": "GH-3"}
	for range 20 {
		if got := createdList(ids, []string{"Uno", "Dos", "Tres"}); got != "GH-1 · Uno, GH-2 · Dos, GH-3 · Tres" {
			t.Fatalf("orden inestable: %s", got)
		}
	}
	esLoc := i18n.DefaultBundle().New("es")
	if got := createdList(nil, nil, esLoc); got != "ninguna" {
		t.Fatalf("got %q", got)
	}
}
