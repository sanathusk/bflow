package cli

import (
	"cmp"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"innobytes.tech/bflow/internal/config"
	"innobytes.tech/bflow/internal/flow"
	"innobytes.tech/bflow/internal/i18n"
	"innobytes.tech/bflow/internal/metrics"
	"innobytes.tech/bflow/internal/output"
)

// bflow ui es el panel de bflow watch en el navegador: una página local, de
// solo lectura, que pregunta el estado cada pocos segundos. Muestra todos los
// repos donde se usó bflow (repos.json), agrupados por perfil, y el detalle
// del que se elija. Escucha solo en 127.0.0.1 y rechaza cualquier otro Host
// (una página ajena no puede leerla por DNS rebinding). No usa nada de
// internet: la página y sus fuentes van en el binario.

//go:embed ui/index.html
var uiPage []byte

// La fuente de pixeles es Jersey 10, de The Soft Type Project (SIL
// Open Font License, ui/fonts/OFL.txt), solo el subconjunto latino.
//
//go:embed ui/fonts
var uiFonts embed.FS

const uiDefaultPort = 7719

func init() {
	Register(&Command{Name: "ui", Summary: "browser dashboard with all your repos: ui [--port 7719] [--no-open]",
		Setup: func(fs *flag.FlagSet) {
			fs.Int("port", uiDefaultPort, "local port (picks another if busy)")
			fs.Bool("no-open", false, "do not open browser")
		},
		Run: runUI})
}

// panelStep es AHORA o DESPUÉS: quién actúa y qué hace.
type panelStep struct {
	Human bool       `json:"human"`
	Text  string     `json:"text"`
	Since *time.Time `json:"since,omitempty"`
}

// panelPhase es una estación de la línea del carril.
type panelPhase struct {
	Name  string `json:"name"`
	State string `json:"state"`          // done | current | pending
	Stop  bool   `json:"stop,omitempty"` // al final decide la persona
	Time  string `json:"time,omitempty"` // lo que lleva o llevó
}

type panelTask struct {
	ID        string              `json:"id"`
	Title     string              `json:"title,omitempty"`
	Lane      string              `json:"lane,omitempty"`
	Round     int                 `json:"round,omitempty"`
	Phases    []panelPhase        `json:"phases"`
	Waiting   bool                `json:"waiting"` // la tarea espera a la persona
	Blocked   bool                `json:"blocked,omitempty"`
	Now       *panelStep          `json:"now,omitempty"`
	Next      *panelStep          `json:"next,omitempty"`
	Time      map[string]string   `json:"time"`
	Tokens    string              `json:"tokens,omitempty"`
	Agents    []panelAgent        `json:"agents,omitempty"` // tabla de Tokens (R5)
	Friction  string              `json:"friction,omitempty"`
	Events    []map[string]string `json:"events,omitempty"`
	CallsNote string              `json:"calls_note,omitempty"`
}

// panelAgent es una fila de agente de la tabla de Tokens (R5, R7).
type panelAgent struct {
	Key    string     `json:"key"`   // agente crudo ("main", "implementer", "")
	Name   string     `json:"name"`  // agentName(Key)
	Share  int        `json:"share"` // 0-100 del nuevo total
	Calls  int64      `json:"calls"` // 0 = sin dato (el JS deja la celda vacía)
	New    string     `json:"new"`   // metrics.Human
	Latest bool       `json:"latest,omitempty"`
	Last   *time.Time `json:"last,omitempty"`
	Runs   []panelRun `json:"runs,omitempty"`
}

// panelRun es una corrida anidada en su agente (R6).
type panelRun struct {
	Key     string     `json:"key"`
	Stage   string     `json:"stage"`
	Label   string     `json:"label"`
	Summary string     `json:"summary"` // runSummary, para el encabezado de la ventana
	Calls   int64      `json:"calls"`
	Context string     `json:"context"` // Human(FinalContext)
	Read    string     `json:"read"`    // Human(Total.CacheRead)
	New     string     `json:"new"`     // Human(Total.New())
	Rows    [][]string `json:"rows"`
	Total   []string   `json:"total"`
}

// tokenTable arma la tabla de Tokens (R5-R7).
func tokenTable(st metrics.TaskStats, runs []metrics.Run) []panelAgent {
	byAgent := map[string][]metrics.Run{}
	sums := map[string]metrics.Usage{}
	for _, r := range runs {
		byAgent[r.Agent] = append(byAgent[r.Agent], r)
		u := sums[r.Agent]
		u.Add(r.Total)
		sums[r.Agent] = u
	}
	usage := map[string]metrics.Usage{}
	for k, u := range st.Agents {
		usage[k] = u
	}
	for k, u := range sums {
		if _, ok := usage[k]; !ok {
			usage[k] = u // solo está en calls.jsonl
		}
	}
	var latest string
	var latestAt time.Time
	for _, r := range runs {
		if r.Last.After(latestAt) {
			latest, latestAt = r.Agent, r.Last
		}
	}
	total := st.Tokens.New()
	var out []panelAgent
	for _, k := range byNew(usage) {
		u := usage[k]
		a := panelAgent{Key: k, Name: agentName(k), Calls: u.Calls, New: metrics.Human(u.New())}
		if total > 0 {
			a.Share = int(u.New() * 100 / total)
		}
		for _, r := range byAgent[k] {
			a.Runs = append(a.Runs, panelRun{Key: r.Run, Stage: r.Stage, Label: r.Label, Summary: runSummary(r),
				Calls: r.Total.Calls, Context: metrics.Human(r.FinalContext), Read: metrics.Human(r.Total.CacheRead),
				New: metrics.Human(r.Total.New()), Total: callTotal(r)})
			pr := &a.Runs[len(a.Runs)-1]
			for _, row := range r.Rows {
				pr.Rows = append(pr.Rows, callCells(row))
			}
			if a.Last == nil || r.Last.After(*a.Last) {
				l := r.Last
				a.Last = &l
			}
		}
		a.Latest = len(a.Runs) > 0 && k == latest
		out = append(out, a)
	}
	return out
}

// panelRepo es una fila del tablero de la red: un repo y su tarea activa.
type panelRepo struct {
	Key   string      `json:"key"`
	Name  string      `json:"name"`
	Open  int         `json:"open"` // tareas abiertas
	Task  *panelBrief `json:"task,omitempty"`
	Error string      `json:"error,omitempty"`
}

type panelBrief struct {
	ID      string     `json:"id"`
	Title   string     `json:"title,omitempty"`
	Lane    string     `json:"lane,omitempty"`
	Phase   string     `json:"phase"`
	Waiting bool       `json:"waiting"`
	Now     *panelStep `json:"now,omitempty"`
}

// panelGroup son los repos de un perfil (workspace); "" es sin perfil.
type panelGroup struct {
	Profile string      `json:"profile"`
	Repos   []panelRepo `json:"repos"`
}

type panelState struct {
	Repo    string              `json:"repo"`
	Key     string              `json:"key,omitempty"`
	Profile string              `json:"profile,omitempty"`
	Version string              `json:"version"`
	Stale   bool                `json:"stale,omitempty"` // el binario instalado cambió: reiniciar el panel
	Now     time.Time           `json:"now"`
	Task    *panelTask          `json:"task,omitempty"`
	Others  []map[string]string `json:"others,omitempty"`
	Network []panelGroup        `json:"network,omitempty"`
	// Sin tarea en curso: los carriles del repo, para enseñar cómo empezar.
	Lanes map[string][]string `json:"lanes,omitempty"`
}

// humanStops son las fases que terminan en una decisión de la persona.
func humanStop(p flow.Phase, contractGate bool) bool {
	if p == flow.Contract {
		return contractGate
	}
	return humanStops[p]
}

var humanStops = map[flow.Phase]bool{flow.Discovery: true, flow.Spec: true,
	flow.Paused: true, flow.Walkthrough: true, flow.InReview: true}

// buildPanel arma lo que muestra la página, con los mismos textos que watch.
func buildPanel(version string, d watchData, now time.Time) panelState {
	ps := panelState{Repo: d.Repo, Version: version, Now: now}
	for _, o := range d.Others {
		ps.Others = append(ps.Others, map[string]string{"id": o.ID, "phase": string(o.Phase), "next": nextLabel(o)})
	}
	if d.Active == nil {
		return ps
	}
	v, st := *d.Active, d.Stats
	t := &panelTask{ID: v.ID, Title: v.Title, Lane: string(v.Lane), Round: v.Round, Waiting: v.Gate != "" || v.Blocked != "", Blocked: v.Blocked != ""}
	spent := map[flow.Phase]time.Duration{}
	for _, p := range st.Phases {
		spent[p.Phase] += p.Agent + p.Human + p.Blocked
	}
	phase := v.Phase
	if phase == flow.Blocked && v.BlockedFrom != "" {
		phase = v.BlockedFrom // la línea muestra dónde se quedó
	}
	cur := slices.Index(v.Phases, phase)
	for i, p := range v.Phases {
		if p == flow.Done {
			continue
		}
		pp := panelPhase{Name: string(p), State: "pending", Stop: humanStop(p, v.ContractGate)}
		switch {
		case i == cur:
			pp.State = "current"
		case cur >= 0 && i < cur:
			pp.State = "done"
		}
		if x := spent[p]; x > 0 {
			pp.Time = dur(x)
		}
		t.Phases = append(t.Phases, pp)
	}
	if s, ok := nowStep(v, d.Events); ok {
		t.Now = &panelStep{Human: s.human, Text: s.text}
		if !s.since.IsZero() {
			t.Now.Since = &s.since
		}
	}
	if s, ok := nextStep(v); ok {
		t.Next = &panelStep{Human: s.human, Text: s.text}
	}
	t.Time = map[string]string{"total": dur(st.Total), "agents": dur(st.Agent), "human": dur(st.Human)}
	if st.Blocked > 0 {
		t.Time["blocked"] = dur(st.Blocked)
	}
	for k, x := range map[string]time.Duration{"agents_pct": st.Agent, "human_pct": st.Human, "blocked_pct": st.Blocked} {
		if st.Total > 0 {
			t.Time[k] = strconv.Itoa(int(x * 100 / st.Total))
		}
	}
	if st.TokensAvailable {
		t.Tokens = metrics.Summary(st.Tokens)
		t.Agents = tokenTable(st, d.Calls)
	}
	if n := st.Refused + st.Guarded + st.Nudged; n > 0 {
		t.Friction = fmt.Sprintf("%d rechazo(s) del flujo · %d bloqueo(s) de guard · %d fin(es) sin reporte", st.Refused, st.Guarded, st.Nudged)
	}
	evs := d.Events[max(0, len(d.Events)-2*watchEvents):]
	for i := len(evs) - 1; i >= 0; i-- {
		t.Events = append(t.Events, map[string]string{"time": eventTime(evs[i].TS, now), "text": describeEvent(evs[i])})
	}
	t.CallsNote = callsNote(st, d.Calls)
	ps.Task = t
	return ps
}

// brief es la fila del tablero de la red para un repo ya leído.
func brief(d watchData) panelRepo {
	r := panelRepo{Name: d.Repo, Open: len(d.Others)}
	if d.Active == nil {
		return r
	}
	v := *d.Active
	r.Open++
	b := &panelBrief{ID: v.ID, Title: v.Title, Lane: string(v.Lane), Phase: string(v.Phase), Waiting: v.Gate != "" || v.Blocked != ""}
	if s, ok := nowStep(v, d.Events); ok {
		b.Now = &panelStep{Human: s.human, Text: s.text}
		if !s.since.IsZero() {
			b.Now.Since = &s.since
		}
	}
	r.Task = b
	return r
}

// repoKey identifica un repo en la página sin mostrar su ruta.
func repoKey(root string) string {
	if goos == "windows" {
		root = strings.ToLower(root)
	}
	h := sha256.Sum256([]byte(filepath.Clean(root)))
	return hex.EncodeToString(h[:4])
}

// uiServer es el estado de la página: el repo desde donde se abrió y la red.
type uiServer struct {
	c     *Ctx
	root  string
	mu    sync.Mutex
	net   map[string]panelRepo // por raíz
	prof  map[string]string    // perfil de cada raíz
	netAt time.Time
	stale *staleCheck
}

const uiNetworkTTL = 5 * time.Second // los demás repos se releen cada tanto, no en cada pregunta

func (s *uiServer) roots() []string {
	roots := config.KnownRepos()
	if !slices.ContainsFunc(roots, func(r string) bool { return repoKey(r) == repoKey(s.root) }) {
		roots = append([]string{s.root}, roots...)
	}
	return roots
}

// repoRead es lo leído de un repo para la página.
type repoRead struct {
	d       watchData
	profile string
	lanes   map[string][]string
}

func (s *uiServer) read(root string) (repoRead, error) {
	e, err := s.c.Build(root)
	if err != nil {
		return repoRead{d: watchData{Repo: filepath.Base(root)}}, err
	}
	d, err := readWatchIn(e)
	r := repoRead{d: d, profile: e.Cfg.Profile, lanes: map[string][]string{}}
	for lane, phases := range e.Cfg.Flow.Core().Lanes {
		for _, p := range phases {
			if p != flow.Done {
				r.lanes[string(lane)] = append(r.lanes[string(lane)], string(p))
			}
		}
	}
	return r, err
}

func (s *uiServer) state(key string) (panelState, error) {
	now := time.Now()
	roots := s.roots()
	sel := s.root
	for _, r := range roots {
		if repoKey(r) == key {
			sel = r
		}
	}
	rr, err := s.read(sel)
	d, profile := rr.d, rr.profile
	ps := buildPanel(s.c.Version, d, now)
	ps.Key, ps.Profile = repoKey(sel), profile
	ps.Stale = s.stale.Stale()
	if ps.Task == nil {
		ps.Lanes = rr.lanes
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.net == nil || now.Sub(s.netAt) > uiNetworkTTL {
		s.net, s.prof, s.netAt = map[string]panelRepo{}, map[string]string{}, now
		for _, r := range roots {
			if r == sel {
				continue
			}
			o, rerr := s.read(r)
			row := brief(o.d)
			if rerr != nil {
				row.Error = rerr.Error()
			}
			s.net[r], s.prof[r] = row, o.profile
		}
	}
	row := brief(d) // el elegido, recién leído
	if err != nil {
		row.Error = err.Error()
	}
	s.net[sel], s.prof[sel] = row, profile
	groups := map[string][]panelRepo{}
	for _, r := range roots {
		row, ok := s.net[r]
		if !ok {
			continue // anotado después de la última lectura: sale en la siguiente
		}
		row.Key = repoKey(r)
		groups[s.prof[r]] = append(groups[s.prof[r]], row)
	}
	for p, rs := range groups {
		slices.SortFunc(rs, func(a, b panelRepo) int { return cmp.Compare(a.Name, b.Name) })
		ps.Network = append(ps.Network, panelGroup{Profile: p, Repos: rs})
	}
	slices.SortFunc(ps.Network, func(a, b panelGroup) int {
		if (a.Profile == "") != (b.Profile == "") {
			return cmp.Compare(b.Profile, a.Profile) // sin perfil al final
		}
		return cmp.Compare(a.Profile, b.Profile)
	})
	return ps, err
}

// uiHandler sirve la página, sus fuentes y el estado. allowed son los Host aceptados.
func uiHandler(allowed []string, state func(key string) (panelState, error), loc ...*i18n.Localizer) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; font-src 'self'; img-src data:; connect-src 'self'")
		w.Write(uiPage)
	})
	fonts, _ := fs.Sub(uiFonts, "ui/fonts")
	mux.Handle("GET /fonts/", http.StripPrefix("/fonts/", http.FileServerFS(fonts)))
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) {
		ps, err := state(r.URL.Query().Get("repo"))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if err != nil && ps.Version == "" {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(ps)
	})
	l := i18n.DefaultLocalizer()
	if len(loc) > 0 && loc[0] != nil {
		l = loc[0]
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !slices.Contains(allowed, r.Host) {
			http.Error(w, l.T("ui.host_forbidden"), http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// serveUI levanta la página en 127.0.0.1:port; con fallback, si está ocupado,
// en otro puerto. Devuelve su dirección y cómo cerrarla.
func serveUI(c *Ctx, port int, fallback bool) (string, func(), error) {
	e, err := engineFor(c)
	if err != nil {
		return "", nil, err
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil && fallback {
		ln, err = net.Listen("tcp", "127.0.0.1:0") // ocupado: otro puerto
	}
	if err != nil {
		return "", nil, err
	}
	_ = config.RememberRepo(e.Cfg.Root)
	addr := ln.Addr().(*net.TCPAddr)
	allowed := []string{fmt.Sprintf("127.0.0.1:%d", addr.Port), fmt.Sprintf("localhost:%d", addr.Port)}
	s := &uiServer{c: c, root: e.Cfg.Root, stale: newStaleCheck(binStamp)}
	srv := &http.Server{Handler: uiHandler(allowed, s.state, envLoc(c.Env)), ReadHeaderTimeout: 5 * time.Second}
	go srv.Serve(ln)
	stop := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
	return fmt.Sprintf("http://127.0.0.1:%d/", addr.Port), stop, nil
}

// bflowAt dice si en port ya sirve la página otra ventana de bflow.
func bflowAt(port int) bool {
	cl := http.Client{Timeout: time.Second}
	resp, err := cl.Get(fmt.Sprintf("http://127.0.0.1:%d/api/state", port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var ps panelState
	return resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(&ps) == nil && ps.Version != ""
}

// webPanel es la página servida desde la ventana del panel (watch --web). Si
// otra ventana ya la sirve en el puerto de siempre, no abre otra pestaña: esa
// página ya muestra todos los repos. Si esa ventana se cierra, esta toma el
// puerto en su siguiente vuelta y la pestaña abierta se reconecta sola.
type webPanel struct {
	c    *Ctx
	url  string
	stop func()
}

func startWeb(c *Ctx) *webPanel {
	w := &webPanel{c: c}
	switch {
	case w.serve(false):
		_ = openBrowser(w.url)
	case bflowAt(uiDefaultPort):
		w.url = fmt.Sprintf("http://127.0.0.1:%d/", uiDefaultPort)
	case w.serve(true):
		_ = openBrowser(w.url)
	}
	return w
}

func (w *webPanel) serve(fallback bool) bool {
	url, stop, err := serveUI(w.c, uiDefaultPort, fallback)
	if err != nil {
		return false
	}
	w.url, w.stop = url, stop
	return true
}

// tick toma el puerto si la ventana que servía la página se cerró.
func (w *webPanel) tick() {
	if w.stop == nil && w.url != "" {
		w.serve(false)
	}
}

func (w *webPanel) close() {
	if w.stop != nil {
		w.stop()
	}
}

func runUI(c *Ctx) output.Envelope {
	if _, err := engineFor(c); err != nil {
		return output.Fail("config", err)
	}
	port, _ := strconv.Atoi(str(c.Flags, "port"))
	url, stopUI, err := serveUI(c, port, true)
	if err != nil {
		return fail(err)
	}
	defer stopUI()
	loc := envLoc(c.Env)
	fmt.Fprintln(c.Stdout, loc.T("ui.running_banner", url))
	if str(c.Flags, "no-open") != "true" {
		if err := openBrowser(url, loc); err != nil {
			fmt.Fprintln(c.Stdout, loc.T("ui.open_browser_hint", err.Error()))
		}
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	<-stop
	return output.Envelope{OK: true, Code: "ui", Quiet: true}
}

// browserCmd abre url en el navegador del sistema.
func browserCmd(goos string, getenv func(string) string, url string, loc ...*i18n.Localizer) ([]string, error) {
	if getenv("CI") != "" {
		return nil, fmt.Errorf("%w: CI", errNoDesktop)
	}
	switch goos {
	case "windows":
		return []string{"rundll32", "url.dll,FileProtocolHandler", url}, nil
	case "darwin":
		return []string{"open", url}, nil
	}
	if getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == "" {
		l := i18n.DefaultLocalizer()
		if len(loc) > 0 && loc[0] != nil {
			l = loc[0]
		}
		return nil, fmt.Errorf("%w: %s", errNoDesktop, l.T("ui.no_display"))
	}
	return []string{"xdg-open", url}, nil
}

func openBrowser(url string, loc ...*i18n.Localizer) error {
	args, err := browserCmd(goos, os.Getenv, url, loc...)
	if err != nil {
		return err
	}
	cmd := exec.Command(args[0], args[1:]...)
	if err := cmd.Start(); err != nil {
		return errors.New(strings.TrimSpace(err.Error()))
	}
	return cmd.Process.Release()
}
