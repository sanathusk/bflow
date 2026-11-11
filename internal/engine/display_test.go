package engine

import (
	"slices"
	"strings"
	"testing"

	"innobytes.tech/bflow/internal/i18n"
)

func TestProductQuestionsHideAnswers(t *testing.T) {
	v := newEnv(t, "")
	if err := v.e.Store.WriteFile("T-1", "reports/review-map.md", []byte(`# Review map

## 🔴 Decisión
- internal/x.go: valida el RFC

## Preguntas de producto
1. ¿Qué pasa si el RFC es genérico?
   - Lo acepta como cualquier otro
   - Lo rechaza con 422
   el código responde: lo rechaza (internal/x.go:12)
2. ¿Y si viene vacío?
   El código responde: error 400 (internal/x.go:20)

## 🟢 Mecánico
3 archivos
`)); err != nil {
		t.Fatal(err)
	}
	q, err := v.e.productQuestions("T-1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(q), "código responde") {
		t.Errorf("muestra la respuesta: %s", q)
	}
	for _, w := range []string{"1. ¿Qué pasa si el RFC es genérico?", "   - Lo acepta como cualquier otro", "   - Lo rechaza con 422", "2. ¿Y si viene vacío?"} {
		if !strings.Contains(q, w) {
			t.Errorf("falta %q en: %s", w, q)
		}
	}
	if err := v.e.Store.WriteFile("T-2", "reports/review-map.md", []byte("## 🔴 Decisión\n- algo\n")); err != nil {
		t.Fatal(err)
	}
	if q, _ := v.e.productQuestions("T-2"); !strings.Contains(q, "no trae preguntas") {
		t.Errorf("sin sección: %q", q)
	}
}

func TestClipDisplay(t *testing.T) {
	esLoc := i18n.DefaultBundle().New("es")
	long := strings.Repeat("línea\n", maxDisplayLines+10)
	got := clip(strings.TrimSpace(long), "bflow show T-1 review-map", esLoc)
	if n := strings.Count(got, "\n"); n != maxDisplayLines || !strings.HasSuffix(got, "(10 líneas más: bflow show T-1 review-map)") {
		t.Errorf("recorte: %d saltos\n%s", n, got[len(got)-60:])
	}
	if clip("corto", "x") != "corto" {
		t.Error("lo corto no se toca")
	}
}

const shuffleMap = `## Preguntas de producto
1. ¿Pregunta uno?
   - uno A
   - uno B
   - uno C
2. ¿Pregunta dos?
   - dos A
   - dos B
   - dos C
3. ¿Pregunta tres?
   - tres A
   - tres B
   - tres C
4. ¿Pregunta cuatro?
   - cuatro A
   - cuatro B
   - cuatro C
   el código responde: A (x.go:1)
`

func TestProductQuestionsShuffleStable(t *testing.T) {
	v := newEnv(t, "")
	if err := v.e.Store.WriteFile("T-1", "reports/review-map.md", []byte(shuffleMap)); err != nil {
		t.Fatal(err)
	}
	q1, _ := v.e.productQuestions("T-1")
	q2, _ := v.e.productQuestions("T-1")
	if q1 != q2 {
		t.Fatalf("el orden cambia entre llamadas:\n%s\n---\n%s", q1, q2)
	}
	if strings.Contains(q1, "código responde") {
		t.Error("muestra la respuesta")
	}
	changed := false
	lines := strings.Split(q1, "\n")
	for _, name := range []string{"uno", "dos", "tres", "cuatro"} {
		var got []string
		for _, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), "- "+name+" ") {
				got = append(got, strings.TrimSpace(l))
			}
		}
		want := []string{"- " + name + " A", "- " + name + " B", "- " + name + " C"}
		if len(got) != 3 {
			t.Fatalf("%s: %q", name, got)
		}
		if !slices.Equal(got, want) {
			changed = true
		}
		s := slices.Clone(got)
		slices.Sort(s)
		if !slices.Equal(s, want) {
			t.Errorf("%s no es permutación: %q", name, got)
		}
	}
	if !changed {
		t.Error("ninguna pregunta quedó en orden distinto")
	}
	for _, h := range []string{"1. ¿Pregunta uno?", "4. ¿Pregunta cuatro?"} {
		if !strings.Contains(q1, h) {
			t.Errorf("falta %q", h)
		}
	}
}

func TestShuffleOptionsPermutation(t *testing.T) {
	in := []string{"a", "b", "c", "d"}
	a := shuffleOptions(7, in)
	if !slices.Equal(a, shuffleOptions(7, in)) {
		t.Error("misma semilla, distinto orden")
	}
	s := slices.Clone(a)
	slices.Sort(s)
	if !slices.Equal(s, in) || !slices.Equal(in, []string{"a", "b", "c", "d"}) {
		t.Errorf("no es permutación o muta la entrada: %v", a)
	}
}

func TestProductQuestionsShuffleFlatFormat(t *testing.T) {
	v := newEnv(t, "")
	md := "## Preguntas de producto\n"
	for _, q := range []string{"uno", "dos", "tres", "cuatro"} {
		md += "¿Pregunta " + q + "?\n- " + q + " A\n- " + q + " B\n- " + q + " C\nel código responde: A (x.go:1)\n\n"
	}
	if err := v.e.Store.WriteFile("T-1", "reports/review-map.md", []byte(md)); err != nil {
		t.Fatal(err)
	}
	q1, _ := v.e.productQuestions("T-1")
	q2, _ := v.e.productQuestions("T-1")
	if q1 != q2 || strings.Contains(q1, "código responde") {
		t.Fatalf("inestable o con respuesta:\n%s", q1)
	}
	ordered := "- uno A\n- uno B\n- uno C\n"
	changed := !strings.Contains(q1, ordered)
	for _, q := range []string{"dos", "tres", "cuatro"} {
		changed = changed || !strings.Contains(q1, "- "+q+" A\n- "+q+" B\n- "+q+" C\n")
	}
	if !changed {
		t.Errorf("ningún orden cambió:\n%s", q1)
	}
	for _, q := range []string{"uno", "dos", "tres", "cuatro"} {
		if strings.Count(q1, "- "+q+" ") != 3 || !strings.Contains(q1, "¿Pregunta "+q+"?") {
			t.Errorf("pregunta %s mal: %s", q, q1)
		}
	}
}
