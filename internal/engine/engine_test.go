package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"innobytes.tech/bflow/internal/config"
	"innobytes.tech/bflow/internal/flow"
	"innobytes.tech/bflow/internal/i18n"
	"innobytes.tech/bflow/internal/store"
	"innobytes.tech/bflow/internal/testutil"
	"innobytes.tech/bflow/internal/tracker"
	"innobytes.tech/bflow/internal/tracker/trackertest"
)

type fakeVerifier struct {
	ok     bool
	detail string
}

func (f *fakeVerifier) Verify(context.Context, string) (bool, string, error) {
	return f.ok, f.detail, nil
}

type env struct {
	e   *Engine
	tr  *trackertest.Memory
	ver *fakeVerifier
	now time.Time
}

func newEnv(t *testing.T, yaml string) *env {
	t.Helper()
	root := testutil.TempDir(t)
	t.Setenv("BFLOW_CONFIG_HOME", testutil.TempDir(t))
	if yaml != "" {
		if err := os.WriteFile(filepath.Join(root, "bflow.yaml"), []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.VCS.BaseBranch == "" {
		cfg.VCS.BaseBranch, cfg.VCS.Remote = "main", "origin" // diffBase() da origin/main
	}
	if !strings.Contains(t.Name(), "Scout") && cfg.Flow.Scout == nil {
		off := false // el scout es opt-out en la config; solo sus pruebas lo quieren activo
		cfg.Flow.Scout = &off
	}
	ev := &env{tr: trackertest.NewMemory(), ver: &fakeVerifier{ok: true}, now: time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)}
	st := store.Open(root)
	st.Now = func() time.Time { return ev.now }
	ev.e = &Engine{Cfg: cfg, Store: st, Tracker: ev.tr, Verifier: ev.ver, Now: func() time.Time { return ev.now }, User: "dev",
		Loc: i18n.DefaultBundle().New("es")}
	return ev
}

func (v *env) task(t *testing.T, title string) string {
	t.Helper()
	task, err := v.tr.Create(context.Background(), title, "descripción")
	if err != nil {
		t.Fatal(err)
	}
	return task.ID
}

func (v *env) tick(d time.Duration) { v.now = v.now.Add(d) }

func mustT(t *testing.T) func(Outcome, error) Outcome {
	return func(o Outcome, err error) Outcome {
		t.Helper()
		if err != nil {
			t.Fatalf("error inesperado: %v", err)
		}
		return o
	}
}

func phaseOf(t *testing.T, v *env, id string) flow.Phase {
	t.Helper()
	r, err := v.e.Store.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	return r.Flow.Phase
}

func TestFullLaneEndToEnd(t *testing.T) {
	v := newEnv(t, "")
	ctx := context.Background()
	id := v.task(t, "Crédito: reabrir por rechazo fiscal")

	o := mustT(t)(v.e.Start(ctx, id, flow.Full, "", ""))
	if o.To != flow.Discovery || o.Next.Gate != "discovery" {
		t.Fatalf("start: %+v", o)
	}
	rec, _ := v.e.Store.Load(id)
	if rec.Flow.Slug != "credito-reabrir-por-rechazo-fiscal" || rec.Title == "" {
		t.Errorf("slug/título: %q %q", rec.Flow.Slug, rec.Title)
	}

	v.tick(time.Hour)
	mustT(t)(v.e.Approve(ctx, id, ApproveOpts{Gate: "discovery", Attachment: "# Discovery\n- entra: X\n- no entra: Y"}))
	spec := filepath.Join(v.e.Cfg.Root, "specs", id+"-credito-reabrir-por-rechazo-fiscal", "spec.md")
	b, err := os.ReadFile(spec)
	if err != nil {
		t.Fatalf("el spec debe crearse al entrar a spec: %v", err)
	}
	for _, h := range []string{"## Brief", "## Discovery", "## Requirements", "## Design", "## Tasks"} {
		if !strings.Contains(string(b), h) {
			t.Errorf("plantilla sin %q", h)
		}
	}
	if strings.Contains(string(b), "## UI blueprint") {
		t.Error("sin UI no debe haber sección ui-blueprint")
	}
	if !strings.Contains(string(b), "entra: X") {
		t.Error("## Discovery debe traer el discovery completo")
	}
	if _, err := os.Stat(filepath.Join(v.e.Cfg.Root, ".bflow", "tasks", id, "discovery.md")); err == nil {
		t.Error("no debe existir discovery.md")
	}
	cs, _ := v.tr.Comments(ctx, id)
	if len(cs) != 1 || !strings.Contains(cs[0].Body, "entra: X") {
		t.Errorf("el discovery completo debe ir al tracker: %+v", cs)
	}

	o = mustT(t)(v.e.Report(ctx, id, ReportOpts{Agent: "spec-author", Verdict: flow.Ready}))
	if o.Next.Gate != "spec" || o.Next.Show[0] != "bflow show "+id+" brief" {
		t.Fatalf("spec listo: %+v", o.Next)
	}
	o = mustT(t)(v.e.Approve(ctx, id, ApproveOpts{}))
	if o.To != flow.Contract {
		t.Fatalf("aprobar spec: %+v", o)
	}
	rec, _ = v.e.Store.Load(id)
	if rec.Branch != "feature/"+id+"-credito-reabrir-por-rechazo-fiscal" {
		t.Errorf("rama: %q", rec.Branch)
	}
	if !hasWarning(o, "git") {
		t.Errorf("sin git debe avisar que no creó la rama: %v", o.Warnings)
	}

	mustT(t)(v.e.Report(ctx, id, ReportOpts{Agent: "implementer", Verdict: flow.ContractReady}))
	v.tick(30 * time.Minute)
	o = mustT(t)(v.e.Approve(ctx, id, ApproveOpts{Gate: "contract"}))
	if o.To != flow.Implementing || o.Next.Agents[0].Args["resume"] != "true" {
		t.Fatalf("aprobar contrato: %+v", o.Next)
	}
	task, _ := v.tr.Get(ctx, id)
	if task.Start == nil || !task.Start.Equal(v.now) {
		t.Errorf("start_date debe sellarse al entrar a implementing: %v", task.Start)
	}

	o = mustT(t)(v.e.Report(ctx, id, ReportOpts{Agent: "implementer", Verdict: flow.DoneV, File: ".bflow/tasks/" + id + "/impl.md"}))
	if o.To != flow.Quality || o.Gate != "" {
		t.Fatalf("DONE: %+v", o)
	}
	o = mustT(t)(v.e.Report(ctx, id, ReportOpts{Agent: "reviewer", Verdict: flow.Approved}))
	if o.To != flow.Documenting {
		t.Fatalf("el reviewer solo cierra quality: %+v", o)
	}
	o = mustT(t)(v.e.Report(ctx, id, ReportOpts{Agent: "documenter", Verdict: flow.DoneV}))
	if o.Next.Gate != "questions" {
		t.Fatalf("primero las preguntas de producto: %+v", o.Next)
	}
	o = mustT(t)(v.e.Approve(ctx, id, ApproveOpts{Note: "que rechace el RFC genérico"}))
	if o.Next.Gate != "walkthrough" || !strings.Contains(o.Next.Display, "que rechace el RFC genérico") {
		t.Fatalf("el recorrido trae las respuestas del humano: %+v", o.Next)
	}
	o = mustT(t)(v.e.Approve(ctx, id, ApproveOpts{}))
	if o.To != flow.InReview || o.Next.Action != "wait" || !hasWarning(o, "PR") {
		t.Fatalf("aprobar walkthrough sin host: %+v", o)
	}
	o = mustT(t)(v.e.Merged(ctx, id))
	if o.To != flow.Done {
		t.Fatalf("merge: %+v", o)
	}

	task, _ = v.tr.Get(ctx, id)
	if task.Phase != flow.Done {
		t.Errorf("el tracker debe quedar en done: %s", task.Phase)
	}
	log, _ := v.e.Store.Log(id)
	if len(log) < 14 {
		t.Errorf("log con %d entradas", len(log))
	}
	for _, e := range log {
		if e.By == "" || e.TS.IsZero() || e.Event == "" {
			t.Errorf("entrada incompleta: %+v", e)
		}
	}
}

func hasWarning(o Outcome, frag string) bool {
	for _, w := range o.Warnings {
		if strings.Contains(w, frag) {
			return true
		}
	}
	return false
}

func TestHotfixLane(t *testing.T) {
	v := newEnv(t, "")
	ctx := context.Background()
	id := v.task(t, "Migración mal numerada")
	o := mustT(t)(v.e.Start(ctx, id, flow.Hotfix, "migracion-124", ""))
	rec, _ := v.e.Store.Load(id)
	if o.To != flow.Implementing || rec.Branch != "hotfix/"+id+"-migracion-124" {
		t.Errorf("hotfix: to=%s rama=%s", o.To, rec.Branch)
	}
	if _, err := os.Stat(filepath.Join(v.e.Cfg.Root, "specs")); !os.IsNotExist(err) {
		t.Error("hotfix no crea spec")
	}
}

func TestTrackerFailureQueuesAndSyncs(t *testing.T) {
	v := newEnv(t, "")
	ctx := context.Background()
	id := v.task(t, "Demo")
	v.tr.FailTransitions = true
	v.tr.FailComments = true

	o := mustT(t)(v.e.Start(ctx, id, flow.Full, "", ""))
	if o.To != flow.Discovery || !hasWarning(o, "pendiente") {
		t.Fatalf("con el tracker caído el estado local avanza y avisa: %+v", o)
	}
	mustT(t)(v.e.Reject(ctx, id, RejectOpts{Note: "sigue abierto"}))
	rec, _ := v.e.Store.Load(id)
	if len(rec.Pending) != 2 {
		t.Fatalf("pendientes: %+v", rec.Pending)
	}

	v.tr.FailTransitions = false
	v.tr.FailComments = false
	n, err := v.e.Sync(ctx, id)
	if err != nil || n != 2 {
		t.Fatalf("sync: %d %v", n, err)
	}
	rec, _ = v.e.Store.Load(id)
	if len(rec.Pending) != 0 {
		t.Errorf("tras sync no deben quedar pendientes: %+v", rec.Pending)
	}
	if strings.Join(v.tr.Calls, "; ") != "transition "+id+" discovery; comment "+id {
		t.Errorf("orden de sync: %v", v.tr.Calls)
	}
	task, _ := v.tr.Get(ctx, id)
	if task.Phase != flow.Discovery {
		t.Errorf("tracker: %s", task.Phase)
	}
}

func TestPendingKeepsOrderAcrossCommands(t *testing.T) {
	v := newEnv(t, "")
	ctx := context.Background()
	id := v.task(t, "Demo")
	v.tr.FailTransitions = true
	mustT(t)(v.e.Start(ctx, id, flow.Light, "", "")) // transition spec queda pendiente
	v.tr.FailTransitions = false
	mustT(t)(v.e.Report(ctx, id, ReportOpts{Agent: "spec-author", Verdict: flow.Ready}))
	mustT(t)(v.e.Approve(ctx, id, ApproveOpts{})) // debe sincronizar spec antes de implementing
	want := "transition " + id + " spec; transition " + id + " implementing"
	if got := strings.Join(v.tr.Calls, "; "); !strings.HasPrefix(got, want) {
		t.Errorf("orden: %s", got)
	}
}

func TestDoneRequiresVerifiedCheck(t *testing.T) {
	v := newEnv(t, "")
	ctx := context.Background()
	id := v.task(t, "Demo")
	mustT(t)(v.e.Start(ctx, id, flow.Hotfix, "", ""))
	v.ver.ok, v.ver.detail = false, "cambios de código posteriores al check: internal/x.go"
	_, err := v.e.Report(ctx, id, ReportOpts{Agent: "implementer", Verdict: flow.DoneV})
	var rj *flow.Rejection
	if !errors.As(err, &rj) || rj.Code != "check_required" || !strings.Contains(rj.Reason, "internal/x.go") {
		t.Fatalf("esperaba check_required con el detalle: %v", err)
	}
	if phaseOf(t, v, id) != flow.Implementing {
		t.Error("no debe avanzar")
	}
	v.ver.ok = true
	mustT(t)(v.e.Report(ctx, id, ReportOpts{Agent: "implementer", Verdict: flow.DoneV}))
	if phaseOf(t, v, id) != flow.Quality {
		t.Error("con check verde avanza")
	}
}

func TestNotStartedAndUnknown(t *testing.T) {
	v := newEnv(t, "")
	ctx := context.Background()
	id := v.task(t, "Demo")
	_, err := v.e.Approve(ctx, id, ApproveOpts{})
	var rj *flow.Rejection
	if !errors.As(err, &rj) || rj.Code != "not_started" {
		t.Errorf("aprobar sin empezar: %v", err)
	}
	if _, err := v.e.Start(ctx, "MEM-99", flow.Full, "", ""); err == nil || !strings.Contains(err.Error(), "no existe") {
		t.Errorf("tarea inexistente: %v", err)
	}
	view, err := v.e.Status(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if view.Phase != flow.Backlog || view.Next.Gate != "lane" || view.Title != "Demo" {
		t.Errorf("status de tarea sin empezar: %+v", view)
	}
}

func TestActiveTask(t *testing.T) {
	v := newEnv(t, "")
	ctx := context.Background()
	if _, err := v.e.Active(ctx); err == nil {
		t.Error("sin tareas debe fallar")
	}
	a := v.task(t, "A")
	mustT(t)(v.e.Start(ctx, a, flow.Full, "", ""))
	if id, err := v.e.Active(ctx); err != nil || id != a {
		t.Errorf("una sola tarea activa: %q %v", id, err)
	}
	b := v.task(t, "B")
	mustT(t)(v.e.Start(ctx, b, flow.Full, "", ""))
	_, err := v.e.Active(ctx)
	var rj *flow.Rejection
	if !errors.As(err, &rj) || rj.Code != "ambiguous_task" || !strings.Contains(rj.Reason, a) || !strings.Contains(rj.Reason, b) {
		t.Errorf("dos activas: %v", err)
	}
}

func TestDecisionRecorded(t *testing.T) {
	v := newEnv(t, "")
	ctx := context.Background()
	id := v.task(t, "Demo")
	mustT(t)(v.e.Start(ctx, id, flow.Hotfix, "", ""))
	o := mustT(t)(v.e.Report(ctx, id, ReportOpts{Agent: "implementer", Verdict: flow.NeedsDecision,
		Note: "¿Reintento o fallo rápido?", Options: []string{"Reintentar 3 veces", "Fallar rápido"}}))
	if o.Next.Gate != "decision" || len(o.Next.Options) != 4 {
		t.Fatalf("decisión: %+v", o.Next)
	}
	mustT(t)(v.e.Approve(ctx, id, ApproveOpts{Choice: 2}))
	b, err := v.e.Store.ReadFile(id, "decisions.md")
	if err != nil || !strings.Contains(string(b), "¿Reintento o fallo rápido?") || !strings.Contains(string(b), "Fallar rápido") {
		t.Errorf("decisions.md: %q %v", b, err)
	}
}

func TestShowSections(t *testing.T) {
	v := newEnv(t, "stack: angular\n")
	ctx := context.Background()
	id := v.task(t, "Pantalla de cotizaciones")
	mustT(t)(v.e.Start(ctx, id, flow.Light, "", ""))
	rec, _ := v.e.Store.Load(id)
	path := filepath.Join(v.e.Cfg.Root, flow.SpecPath(id, rec.Flow.Slug))
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "## UI blueprint") {
		t.Error("stack con UI debe incluir la sección ui-blueprint")
	}
	body := strings.Replace(string(b), "## Brief\n", "## Brief\n\n**Objetivo:** cotizar rápido.\n", 1)
	_ = os.WriteFile(path, []byte(body), 0o644)

	out, err := v.e.Show(ctx, id, "brief", "")
	if err != nil || !strings.Contains(out, "cotizar rápido") || strings.Contains(out, "## Requirements") {
		t.Errorf("show brief: %q %v", out, err)
	}
	if _, err := v.e.Show(ctx, id, "spec", "nope"); err == nil || !strings.Contains(err.Error(), "brief") {
		t.Errorf("sección inexistente debe listar las válidas: %v", err)
	}
	if _, err := v.e.Show(ctx, id, "contract", ""); err == nil {
		t.Error("contrato inexistente debe fallar")
	}
	_ = v.e.Store.WriteFile(id, "contract.md", []byte("type X interface{}"))
	if out, _ := v.e.Show(ctx, id, "contract", ""); out != "type X interface{}" {
		t.Errorf("contract: %q", out)
	}
}

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Crédito: reabrir por rechazo fiscal":        "credito-reabrir-por-rechazo-fiscal",
		"  Ñandú & Pingüino  ":                       "nandu-pinguino",
		"Borrador PRE-FOLIO de cotización (WEB-108)": "borrador-pre-folio-de-cotizacion-web-108",
		strings.Repeat("palabra ", 20):               "palabra-palabra-palabra-palabra-palabra-palabra",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNewCreatesAndStarts(t *testing.T) {
	v := newEnv(t, "")
	ctx := context.Background()
	task, o, err := v.e.New(ctx, flow.Light, "Idea nueva", "Detalle de la idea")
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != "MEM-1" || o.ID != task.ID || o.To != flow.Spec {
		t.Fatalf("task %+v outcome %+v", task, o)
	}
	got, _ := v.tr.Get(ctx, task.ID)
	if got.Title != "Idea nueva" || got.Description != "Detalle de la idea" {
		t.Errorf("tarea en el tracker: %+v", got)
	}
	if rec, err := v.e.Store.Load(task.ID); err != nil || rec.Flow.Lane != flow.Light || rec.Flow.Slug != "idea-nueva" {
		t.Errorf("estado: %+v %v", rec, err)
	}
	if o.Next.Action == "" {
		t.Error("responde con el next de start")
	}
	_, o, err = v.e.New(ctx, flow.Light, "Otra", "", "mi-slug")
	if err != nil {
		t.Fatal(err)
	}
	if rec, _ := v.e.Store.Load(o.ID); rec.Flow.Slug != "mi-slug" {
		t.Errorf("slug explícito: %q", rec.Flow.Slug)
	}
}

func TestNewRejectsUnknownLaneBeforeCreate(t *testing.T) {
	v := newEnv(t, "")
	_, _, err := v.e.New(context.Background(), flow.Lane("zzz"), "x", "")
	var rj *flow.Rejection
	if !errors.As(err, &rj) || rj.Code != "unknown_lane" {
		t.Fatalf("esperaba unknown_lane, got %v", err)
	}
	if l, _ := v.tr.List(context.Background(), tracker.Filter{}); len(l) != 0 {
		t.Errorf("no debe crear la tarea: %v", l)
	}
}

func TestNewStartFailureKeepsTask(t *testing.T) {
	v := newEnv(t, "")
	// Un .bflow que es un archivo hace fallar al store: Start no puede arrancar.
	if err := os.WriteFile(filepath.Join(v.e.Cfg.Root, ".bflow"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	task, _, err := v.e.New(ctx, flow.Light, "Idea", "")
	if err == nil {
		t.Fatal("Start falla: debe haber error")
	}
	if task.ID != "MEM-1" {
		t.Errorf("la tarea creada se devuelve: %+v", task)
	}
	want := "MEM-1 creada, pero no arrancó: "
	if !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "Reintenta con bflow start MEM-1 --lane light") {
		t.Errorf("mensaje: %v", err)
	}
	if l, _ := v.tr.List(ctx, tracker.Filter{}); len(l) != 1 {
		t.Errorf("la tarea sigue en el tracker: %v", l)
	}
}

// toSpecGate lleva una tarea full hasta el gate del spec.
func toSpecGate(t *testing.T, v *env, id string) {
	t.Helper()
	ctx := context.Background()
	mustT(t)(v.e.Start(ctx, id, flow.Full, "", ""))
	mustT(t)(v.e.Approve(ctx, id, ApproveOpts{Gate: "discovery", Attachment: "# Discovery\n- entra: X"}))
	mustT(t)(v.e.Report(ctx, id, ReportOpts{Agent: "spec-author", Verdict: flow.Ready}))
}

func TestApproveClearSoloAlAvanzar(t *testing.T) {
	v := newEnv(t, "")
	ctx := context.Background()
	id := v.task(t, "Demo clear")
	toSpecGate(t, v, id)
	o := mustT(t)(v.e.Approve(ctx, id, ApproveOpts{}))
	if o.To != flow.Contract || !o.Next.Clear {
		t.Errorf("spec -> contract debe traer clear: %+v", o)
	}

	// decision: la fase no cambia.
	h := v.task(t, "Demo decision")
	mustT(t)(v.e.Start(ctx, h, flow.Hotfix, "", ""))
	mustT(t)(v.e.Report(ctx, h, ReportOpts{Agent: "implementer", Verdict: flow.NeedsDecision,
		Note: "¿A o B?", Options: []string{"A", "B"}}))
	o = mustT(t)(v.e.Approve(ctx, h, ApproveOpts{Choice: 1}))
	if o.Next.Clear {
		t.Errorf("decision no trae clear: %+v", o.Next)
	}

	// questions: pasa al gate walkthrough en la misma fase.
	q := v.task(t, "Demo questions")
	mustT(t)(v.e.Start(ctx, q, flow.Hotfix, "", ""))
	if _, err := v.e.Store.Update(q, func(r *store.Record, _ bool) error {
		r.Flow.Phase = flow.Walkthrough
		r.Flow.Gate = &flow.PendingGate{Name: flow.GateQuestions}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	o = mustT(t)(v.e.Approve(ctx, q, ApproveOpts{Note: "que rechace el RFC"}))
	if o.To != "" || o.Next.Clear {
		t.Errorf("questions no trae clear: %+v", o)
	}

	// reject no sugiere clear.
	r := v.task(t, "Demo reject")
	toSpecGate(t, v, r)
	o = mustT(t)(v.e.Reject(ctx, r, RejectOpts{Note: "falta detalle"}))
	if o.Next.Clear {
		t.Errorf("reject no trae clear: %+v", o.Next)
	}
}

func TestApproveNoteEnDecisions(t *testing.T) {
	v := newEnv(t, "")
	ctx := context.Background()

	id := v.task(t, "Con nota")
	toSpecGate(t, v, id)
	mustT(t)(v.e.Approve(ctx, id, ApproveOpts{Note: "ratifico\nel brief   tal cual"}))
	b, err := v.e.Store.ReadFile(id, "decisions.md")
	want := "- 2026-09-25 · gate spec: ratifico el brief tal cual (dev)\n"
	if err != nil || !strings.Contains(string(b), want) {
		t.Fatalf("decisions.md debe traer %q: %q %v", want, b, err)
	}

	sin := v.task(t, "Sin nota")
	toSpecGate(t, v, sin)
	mustT(t)(v.e.Approve(ctx, sin, ApproveOpts{Note: "  "}))
	if b, err := v.e.Store.ReadFile(sin, "decisions.md"); err == nil {
		t.Errorf("sin nota no se escribe decisions.md: %q", b)
	}

	q := v.task(t, "Preguntas")
	mustT(t)(v.e.Start(ctx, q, flow.Hotfix, "", ""))
	if _, err := v.e.Store.Update(q, func(r *store.Record, _ bool) error {
		r.Flow.Phase = flow.Walkthrough
		r.Flow.Gate = &flow.PendingGate{Name: flow.GateQuestions}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	mustT(t)(v.e.Approve(ctx, q, ApproveOpts{Note: "que rechace el RFC"}))
	if b, err := v.e.Store.ReadFile(q, "decisions.md"); err == nil {
		t.Errorf("la nota de questions no va a decisions.md: %q", b)
	}
}

func TestRejectBackLogsWithoutGate(t *testing.T) {
	v := newEnv(t, "")
	ctx := context.Background()
	id := v.task(t, "Demo regreso")
	mustT(t)(v.e.Start(ctx, id, flow.Hotfix, "", ""))
	mustT(t)(v.e.Report(ctx, id, ReportOpts{Agent: "implementer", Verdict: flow.DoneV}))
	mustT(t)(v.e.Reject(ctx, id, RejectOpts{To: "implementing", Note: "defecto"}))
	log, err := v.e.Store.Log(id)
	if err != nil {
		t.Fatal(err)
	}
	last := log[len(log)-1]
	if last.Event != "reject" || last.Gate != "" || last.From == last.To || last.To != flow.Implementing || last.Note != "defecto" {
		t.Errorf("entrada: %+v", last)
	}
}

func TestShowDiscoveryFromSpec(t *testing.T) {
	v := newEnv(t, "")
	ctx := context.Background()
	id := v.task(t, "Crédito: reabrir")
	mustT(t)(v.e.Start(ctx, id, flow.Full, "", ""))
	if _, err := v.e.Show(ctx, id, "discovery", ""); err == nil {
		t.Error("sin discovery debe fallar")
	}
	mustT(t)(v.e.Approve(ctx, id, ApproveOpts{Gate: "discovery", Attachment: "  # D\n- entra: X\n"}))
	got, err := v.e.Show(ctx, id, "discovery", "")
	if err != nil || got != "# D\n- entra: X" {
		t.Errorf("show discovery: %q %v", got, err)
	}
	rec, _ := v.e.Store.Load(id)
	b, _ := os.ReadFile(v.e.specAbs(rec.Flow))
	if s := string(b); !strings.Contains(s, "- entra: X") || !strings.Contains(s, "## Requirements") {
		t.Errorf("spec: %s", s)
	}
}

func TestShowDiscoveryLegacyFile(t *testing.T) {
	v := newEnv(t, "")
	ctx := context.Background()
	id := v.task(t, "Crédito: reabrir")
	mustT(t)(v.e.Start(ctx, id, flow.Full, "", ""))
	if err := v.e.Store.WriteFile(id, "discovery.md", []byte("viejo: Z")); err != nil {
		t.Fatal(err)
	}
	got, err := v.e.Show(ctx, id, "discovery", "")
	if err != nil || got != "viejo: Z" {
		t.Errorf("legacy: %q %v", got, err)
	}
}
