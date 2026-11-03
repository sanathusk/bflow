package cli

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"innobytes.tech/bflow/internal/engine"
	"innobytes.tech/bflow/internal/flow"
	"innobytes.tech/bflow/internal/i18n"
	"innobytes.tech/bflow/internal/output"
)

func TestUIHandler(t *testing.T) {
	since := time.Date(2026, 9, 29, 13, 0, 0, 0, time.UTC)
	v := engine.View{ID: "API-7", Lane: flow.Light, Phase: flow.Spec, Gate: "spec", GateSince: since, Since: &since,
		Phases: flow.DefaultLanes()[flow.Light], Upcoming: flow.Step{Phase: flow.Implementing, Agents: []string{"implementer"}},
		Next: output.Next{Action: output.ActionAsk}}
	var asked string
	h := uiHandler([]string{"127.0.0.1:7719"}, func(key string) (panelState, error) {
		asked = key
		return buildPanel("dev", watchData{Repo: "api", Active: &v}, since.Add(time.Minute)), nil
	})
	get := func(host, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := get("evil.example:7719", "/api/state"); w.Code != http.StatusForbidden {
		t.Errorf("Host ajeno (DNS rebinding): %d", w.Code)
	}
	if w := get("127.0.0.1:7719", "/"); w.Code != 200 || !strings.Contains(w.Body.String(), "Avisarme cuando me toque") || w.Header().Get("Content-Security-Policy") == "" {
		t.Errorf("página: %d %v", w.Code, w.Header())
	}
	if w := get("127.0.0.1:7719", "/fonts/Jersey10.woff2"); w.Code != 200 || w.Body.Len() < 1000 {
		t.Errorf("fuente: %d", w.Code)
	}
	w := get("127.0.0.1:7719", "/api/state?repo=ab12cd34")
	if asked != "ab12cd34" {
		t.Errorf("repo pedido: %q", asked)
	}
	var ps panelState
	if err := json.Unmarshal(w.Body.Bytes(), &ps); err != nil {
		t.Fatal(err)
	}
	tk := ps.Task
	if tk == nil || !tk.Waiting || tk.Now == nil || !tk.Now.Human || tk.Now.Text != "aprobar la spec" || tk.Next.Text != "implementer implementa las tareas de la spec" {
		t.Fatalf("estado: %+v", tk)
	}
	if p := tk.Phases; p[0].State != "current" || !p[0].Stop || p[1].State != "pending" || p[1].Stop || len(p) != 6 {
		t.Errorf("fases: %+v", tk.Phases)
	}
	if w := get("127.0.0.1:7719", "/otra"); w.Code != http.StatusNotFound {
		t.Errorf("ruta desconocida: %d", w.Code)
	}
}

// La página dibuja el mismo banner que la terminal.
func TestUIPageBanner(t *testing.T) {
	for _, l := range bannerWord {
		if !strings.Contains(string(uiPage), `"`+l+`"`) {
			t.Errorf("la página no tiene la línea del banner %q", l)
		}
	}
}

// R18 (GH-46): la página cabe en una pantalla, con ventana de detalle y sidebar de repos.
func TestUIPageOneScreen(t *testing.T) {
	page := string(uiPage)
	for _, want := range []string{"100dvh", `<dialog id="calls"`, `id="repos"`, `id="shell"`} {
		if !strings.Contains(page, want) {
			t.Errorf("la página debe tener %q", want)
		}
	}
	for _, bad := range []string{"<details", `id="foot"`, `id="net"`} {
		if strings.Contains(page, bad) {
			t.Errorf("la página no debe tener %q", bad)
		}
	}
}

// R1, R15, R16, R17, R19: sidebar de repos en lugar de la tira de chips.
func TestUIPageSidebar(t *testing.T) {
	page := string(uiPage)
	for _, want := range []string{`<aside class="side`, `id="repos"`, `id="repo-list"`, `id="repos-collapse"`,
		`id="repos-open"`, `id="repos-close"`, "bflow.sidebar-collapsed", "position: sticky", "(hover: none)"} {
		if !strings.Contains(page, want) {
			t.Errorf("la página debe tener %q", want)
		}
	}
	for _, bad := range []string{`<nav class="repos"`, `class="chip`} {
		if strings.Contains(page, bad) {
			t.Errorf("la página no debe tener %q", bad)
		}
	}
}

// R7-R14: ocultar repos, deshacer, lista de ocultos y estado de todos ocultos.
func TestUIPageHiddenRepos(t *testing.T) {
	page := string(uiPage)
	for _, want := range []string{"bflow.hidden-repos", `id="hidden-toggle"`, "aria-expanded", `id="hidden-list"`,
		`id="undo"`, `role="status"`, "Deshacer", "Mostrar", "Quitar", "UNDO_MS = 6000", `id="all-hidden"`,
		"function loadHidden", "function saveHidden", "function repoRank", "function sortRepos",
		"function ensureVisibleSelection"} {
		if !strings.Contains(page, want) {
			t.Errorf("la página debe tener %q", want)
		}
	}
}

// R12: todo acceso a localStorage va en try/catch, incluidas las claves nuevas.
func TestUIPageStorageGuarded(t *testing.T) {
	var hidden, collapsed bool
	for _, line := range strings.Split(string(uiPage), "\n") {
		if !strings.Contains(line, "localStorage.") {
			continue
		}
		if !strings.Contains(line, "try {") {
			t.Errorf("localStorage sin try/catch: %s", strings.TrimSpace(line))
		}
		hidden = hidden || strings.Contains(line, "HIDDEN_KEY")
		collapsed = collapsed || strings.Contains(line, "COLLAPSED_KEY")
	}
	if !hidden || !collapsed {
		t.Errorf("faltan accesos protegidos a HIDDEN_KEY (%v) o COLLAPSED_KEY (%v)", hidden, collapsed)
	}
}

func TestBrowserCmd(t *testing.T) {
	if c, err := browserCmd("windows", env(), "http://127.0.0.1:7719/"); err != nil || c[0] != "rundll32" {
		t.Errorf("windows: %v %v", c, err)
	}
	if c, err := browserCmd("linux", env("DISPLAY", ":0"), "u"); err != nil || c[0] != "xdg-open" {
		t.Errorf("linux: %v %v", c, err)
	}
	if _, err := browserCmd("darwin", env("CI", "1"), "u"); !errors.Is(err, errNoDesktop) {
		t.Errorf("CI: %v", err)
	}
}

func TestRenderPanelClosedOutside(t *testing.T) {
	got := renderPanel(engine.PanelReport{ClosedOutside: []engine.ClosedOutside{
		{ID: "GH-13", From: flow.Implementing, Reason: "tracker"},
		{ID: "GH-14", From: flow.Implementing, Reason: "pr", PR: 30},
	}})
	for _, want := range []string{"GH-13 cerrada: terminada fuera de esta copia (tracker)", "GH-14 cerrada: terminada fuera de esta copia (PR #30 mergeado)"} {
		if !strings.Contains(got, want) {
			t.Errorf("falta %q en:\n%s", want, got)
		}
	}
}

// GH-52: el panel avisa cuando el binario instalado cambió tras arrancar.
func TestStaleCheck(t *testing.T) {
	cur := "a"
	s := newStaleCheck(func() string { return cur })
	if s.Stale() {
		t.Error("recién arrancado no está viejo")
	}
	cur = "b"
	if !s.Stale() {
		t.Error("el binario cambió: debe avisar")
	}
	cur = ""
	if s.Stale() {
		t.Error("sin bflow en el PATH no se avisa")
	}
	var none *staleCheck
	if none.Stale() {
		t.Error("sin chequeo no se avisa")
	}
}

func TestPanelStaleInState(t *testing.T) {
	cur := "a"
	s := &uiServer{stale: newStaleCheck(func() string { return cur })}
	ps := buildPanel("dev", watchData{Repo: "api"}, time.Now())
	ps.Stale = s.stale.Stale()
	b, _ := json.Marshal(ps)
	if strings.Contains(string(b), `"stale"`) {
		t.Errorf("sin cambio no debe salir stale: %s", b)
	}
	cur = "b"
	ps.Stale = s.stale.Stale()
	b, _ = json.Marshal(ps)
	if !strings.Contains(string(b), `"stale":true`) {
		t.Errorf("con cambio debe salir stale: %s", b)
	}
}

func TestRenderWatchStale(t *testing.T) {
	out := renderWatch(bannerOff, "dev", watchData{Repo: "api", Stale: true}, time.Now(), 0)
	if !strings.Contains(out, "restart the panel") {
		t.Errorf("watch no avisa: %s", out)
	}

	b, _ := i18n.Load()
	outEs := renderWatch(bannerOff, "dev", watchData{Repo: "api", Stale: true}, time.Now(), 0, b.New("es"))
	if !strings.Contains(outEs, "reinicia el panel") {
		t.Errorf("watch es no avisa: %s", outEs)
	}
}

func TestHumanStopContract(t *testing.T) {
	if !humanStop(flow.Contract, true) {
		t.Error("con gate contract es parada humana")
	}
	if humanStop(flow.Contract, false) {
		t.Error("sin gate contract no es parada")
	}
	if !humanStop(flow.Spec, false) || humanStop(flow.Implementing, true) {
		t.Error("las demás paradas no cambian")
	}
}
