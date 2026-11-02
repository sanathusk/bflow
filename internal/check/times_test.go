package check

import (
	"strings"
	"testing"

	"innobytes.tech/bflow/internal/store"
	"innobytes.tech/bflow/internal/testutil"
)

func run(result string, secs float64, detail string) Result {
	return Result{Result: result, Steps: []StepResult{
		{Name: "vet", Cmd: "go vet ./...", Status: "pass", Secs: 3},
		{Name: "test", Cmd: "go test ./...", Status: "pass", Secs: secs, Detail: detail},
	}}
}

// Las cifras de BE-05: test pasaba en 227 s y sin DB_URI pasó en 22.7 s.
func TestCompareTimesWarnsOnSuspiciousSpeedup(t *testing.T) {
	st := store.Open(testutil.TempDir(t))
	compare := func(r Result) Result {
		t.Helper()
		if err := CompareTimes(st, &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := compare(run("PASS", 227, "")); len(r.Warnings) != 0 {
		t.Fatalf("sin referencia no avisa: %v", r.Warnings)
	}
	r := compare(run("PASS", 22.7, ""))
	if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "test tardó 23 s y la última corrida verde 227 s") {
		t.Fatalf("aviso: %v", r.Warnings)
	}
	if !strings.Contains(r.Markdown(), "Avisos") || (!strings.Contains(r.Summary(), "1 warning(s)") && !strings.Contains(r.Summary(), "1 aviso(s)")) {
		t.Errorf("el aviso va al reporte y al resumen:\n%s\n%s", r.Markdown(), r.Summary())
	}
	// Una corrida sospechosa no baja la referencia: la siguiente también avisa.
	if r := compare(run("PASS", 22, "")); len(r.Warnings) != 1 {
		t.Errorf("segunda corrida rápida: %v", r.Warnings)
	}
	// A la tercera seguida se toma como el tiempo nuevo.
	compare(run("PASS", 21, ""))
	if r := compare(run("PASS", 22, "")); len(r.Warnings) != 0 {
		t.Errorf("tras %d corridas rápidas es el tiempo nuevo: %v", acceptAfter, r.Warnings)
	}
}

func TestCompareTimesIgnoresOtherShapes(t *testing.T) {
	st := store.Open(testutil.TempDir(t))
	base := run("PASS", 227, "40 paq.")
	_ = CompareTimes(st, &base)
	for name, r := range map[string]Result{
		"otros paquetes ({affected})": run("PASS", 10, "3 paq."),
		"check que falló":             run("FAIL", 10, "40 paq."),
	} {
		if err := CompareTimes(st, &r); err != nil {
			t.Fatal(err)
		}
		if name == "otros paquetes ({affected})" && len(r.Warnings) != 0 {
			t.Errorf("%s no avisa: %v", name, r.Warnings)
		}
	}
	// El check que falló avisa, pero no guarda nada: la referencia sigue en 227 s.
	r := run("PASS", 10, "40 paq.")
	_ = CompareTimes(st, &r)
	if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "227 s") {
		t.Errorf("la referencia no cambia con un check en rojo: %v", r.Warnings)
	}
	short := Result{Result: "PASS", Steps: []StepResult{{Name: "vet", Cmd: "go vet ./...", Status: "pass", Secs: 0.5}}}
	_ = CompareTimes(st, &short)
	if len(short.Warnings) != 0 {
		t.Errorf("pasos cortos no avisan: %v", short.Warnings)
	}
}
