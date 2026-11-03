package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"innobytes.tech/bflow/internal/config"
	"innobytes.tech/bflow/internal/engine"
	"innobytes.tech/bflow/internal/flow"
	"innobytes.tech/bflow/internal/i18n"
	"innobytes.tech/bflow/internal/metrics"
	"innobytes.tech/bflow/internal/output"
	"innobytes.tech/bflow/internal/store"
)

func init() {
	Register(&Command{Name: "watch", Summary: "live task dashboard: current step, next, timings, tokens per agent and events: watch [--interval 2s] [--once] [--open] [--web]",
		Setup: func(fs *flag.FlagSet) {
			fs.Duration("interval", 2*time.Second, "refresh interval")
			fs.Bool("once", false, "draw once and exit")
			fs.Bool("open", false, "open dashboard in another window or tab if not already open")
			fs.Bool("web", false, "also serve dashboard page and open in browser (bflow ui)")
		},
		Run: runWatch})
}

// El panel es para una persona en otra ventana: ninguna sesión lo lee, así que
// puede ser tan detallado como ayude a seguir la tarea.

const watchEvents = 8

// watchData es lo que el panel muestra, ya leído.
type watchData struct {
	Repo   string
	Active *engine.View
	Stats  metrics.TaskStats
	Events []store.Entry // de la tarea activa, en orden del log
	Others []engine.View
	// Unassigned son las llamadas del modelo sin tarea (R13).
	Unassigned metrics.Usage
	WebURL     string // la página del panel, si esta ventana la sirve
	// Calls son las corridas de calls.jsonl de la tarea activa; CallsFile dice si el archivo existe.
	Calls     []metrics.Run
	CallsFile bool
	Stale     bool // el binario instalado cambió desde que arrancó este panel
	// Budget es el presupuesto por fase configurado (GH-16); vacío = no mostrar la línea.
	Budget map[flow.Phase]int64
}

func runWatch(c *Ctx) output.Envelope {
	interval := c.Flags.Lookup("interval").Value.(flag.Getter).Get().(time.Duration)
	once := str(c.Flags, "once") == "true"
	if str(c.Flags, "open") == "true" {
		return runWatchOpen(c)
	}
	m := bannerFor(c)
	if once {
		d, err := readWatch(c)
		if err != nil {
			return fail(err)
		}
		env := output.OK("watch", map[string]any{}, nil)
		env.Text = renderWatch(m, c.Version, d, time.Now(), 0, envLoc(c.Env))
		return env
	}
	f, _ := c.Stdout.(*os.File)
	enableVT(f)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	defer signal.Stop(stop)
	fmt.Fprint(c.Stdout, "\x1b[?25l\x1b[2J") // sin cursor; se limpia una vez y después se repinta encima
	defer fmt.Fprint(c.Stdout, "\x1b[?25h\n")
	var storeDir string
	if e, err := engineFor(c); err == nil {
		storeDir = e.Store.Dir()
		_ = config.RememberRepo(e.Cfg.Root)
		defer os.Remove(alivePath(storeDir)) // al cerrar la ventana no corre: el latido envejece solo
	}
	stale := newStaleCheck(binStamp)
	var web *webPanel
	if str(c.Flags, "web") == "true" {
		web = startWeb(c)
		defer web.close()
	}
	for {
		frame := ""
		if d, err := readWatch(c); err != nil {
			frame = "bflow watch: " + err.Error()
		} else {
			if web != nil {
				web.tick()
				d.WebURL = web.url
			}
			d.Stale = stale.Stale()
			frame = renderWatch(m, c.Version, d, time.Now(), termRows(f)-1, envLoc(c.Env))
		}
		if storeDir != "" {
			beat(storeDir, interval)
		}
		// Cada línea borra lo que quedaba del cuadro anterior; así no parpadea.
		fmt.Fprint(c.Stdout, "\x1b[H"+strings.ReplaceAll(frame, "\n", "\x1b[K\n")+"\x1b[K\x1b[J")
		select {
		case <-stop:
			return output.Envelope{OK: true, Code: "watch", Quiet: true}
		case <-time.After(interval):
		}
	}
}

func readWatch(c *Ctx) (watchData, error) {
	e, err := engineFor(c)
	if err != nil {
		return watchData{}, err
	}
	return readWatchIn(e)
}

// readWatchIn lee el panel del repo de e.
func readWatchIn(e *engine.Engine) (watchData, error) {
	ctx := context.Background()
	d := watchData{Repo: filepath.Base(e.Cfg.Root), Unassigned: metrics.UnassignedUsage(e.Store.Dir()), Budget: e.Budgets()}
	views, err := e.Views(ctx)
	if err != nil {
		return d, err
	}
	active, _ := e.Active(ctx)
	for i := range views {
		if views[i].ID == active {
			d.Active = &views[i]
		} else {
			d.Others = append(d.Others, views[i])
		}
	}
	if d.Active == nil {
		return d, nil
	}
	log, err := e.Store.Log(active)
	if err != nil {
		return d, err
	}
	d.Stats = metrics.Compute(active, log, time.Now())
	d.Calls, d.CallsFile = readCalls(e, active)
	for _, en := range log {
		if en.Event != "tokens" {
			d.Events = append(d.Events, en)
		}
	}
	return d, nil
}

// Colores del panel: el agente en cian, la persona en amarillo, lo hecho en
// verde y lo pendiente atenuado. Sin color (NO_COLOR, un pipe) queda texto
// plano con la fase actual entre corchetes.
const (
	cAgent = "1;36"
	cHuman = "1;33"
	cDone  = "32"
	cDim   = "2"
	cBold  = "1"
)

func paint(m bannerMode, code, s string) string {
	if m != bannerColor || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// who etiqueta quién actúa, con el mismo ancho para que el texto se alinee.
func who(m bannerMode, human bool) string {
	if human {
		return paint(m, cHuman, "TÚ    ")
	}
	return paint(m, cAgent, "AGENTE")
}

// agentActivity es lo que hace cada agente, en palabras llanas.
var agentActivity = map[string]string{
	"spec-author":      "escribe la spec (brief, requisitos, diseño y tareas)",
	"ui-designer":      "diseña el UI blueprint",
	"reviewer":         "revisa el diff: pruebas, arquitectura y seguridad",
	"security-auditor": "audita la seguridad del diff",
	"ux-auditor":       "audita la interfaz contra el blueprint",
	"documenter":       "documenta el cambio y escribe el walkthrough",
}

func activity(agents []string, phase flow.Phase) string {
	if len(agents) == 0 {
		return string(phase)
	}
	if len(agents) > 1 {
		return strings.Join(agents, " y ") + " revisan en paralelo"
	}
	a := agents[0]
	switch {
	case a == "implementer" && phase == flow.Contract:
		return a + " escribe el contrato: firmas y pruebas con su cuerpo"
	case a == "implementer":
		return a + " implementa las tareas de la spec"
	}
	if s, ok := agentActivity[a]; ok {
		return a + " " + s
	}
	return a + " trabaja en " + string(phase)
}

// step es una línea de AHORA o DESPUÉS: quién actúa y qué hace.
type step struct {
	human bool
	text  string
	since time.Time // cuándo empezó (solo AHORA)
}

// nowStep dice quién tiene la tarea en este momento.
func nowStep(v engine.View, evs []store.Entry) (step, bool) {
	switch {
	case v.Blocked != "" || v.Gate != "":
		t := v.GateSince
		if t.IsZero() {
			t = since(v, evs)
		}
		return step{human: true, text: humanAction(v), since: t}, true
	case v.Next.Action == output.ActionSpawn:
		var as []string
		for _, a := range v.Next.Agents {
			as = append(as, a.Agent)
		}
		s := activity(as, v.Phase)
		if len(v.Reported) > 0 {
			s += " · ya reportó " + strings.Join(v.Reported, ", ")
		}
		return step{text: s, since: since(v, evs)}, true
	case v.Phase == flow.InReview:
		return step{human: true, text: "revisar y hacer merge del PR", since: since(v, evs)}, true
	}
	return step{}, false
}

// nextStep dice qué viene después si todo sale bien.
func nextStep(v engine.View) (step, bool) {
	u := v.Upcoming
	switch {
	case u.Gate != "":
		return step{human: true, text: gateAction[string(u.Gate)]}, true
	case len(u.Agents) > 0:
		return step{text: activity(u.Agents, u.Phase)}, true
	case u.Merge:
		return step{human: true, text: "revisar y hacer merge del PR"}, true
	}
	return step{}, false
}

// progress es la línea del carril: lo hecho, la fase actual y lo que falta,
// partida para que quepa en width columnas.
func progress(m bannerMode, v engine.View, width int) []string {
	cur := slices.Index(v.Phases, v.Phase)
	waiting := v.Gate != "" || v.Blocked != ""
	var parts []string
	var widths []int
	for i, p := range v.Phases {
		if p == flow.Done {
			continue
		}
		name := string(p)
		switch {
		case i == cur && m != bannerColor:
			name = "[" + name + "]"
		case i == cur && waiting:
			name = paint(m, cHuman, name)
		case i == cur:
			name = paint(m, cAgent, name)
		case cur >= 0 && i < cur:
			name = paint(m, cDone, name)
		default:
			name = paint(m, cDim, name)
		}
		parts = append(parts, name)
		widths = append(widths, len([]rune(string(p)))+map[bool]int{true: 2}[i == cur && m != bannerColor])
	}
	sep := " " + paint(m, cDim, ">") + " "
	var lines []string
	line, w := "", 0
	for i, p := range parts {
		add := widths[i]
		if w > 0 {
			add += 3
		}
		if w > 0 && w+add > width {
			lines = append(lines, line+sep)
			line, w = "", 0
			add = widths[i]
		}
		if w > 0 {
			line += sep
		}
		line += p
		w += add
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// renderWatch arma el cuadro. Con rows > 0 lo ajusta a ese alto: primero
// cambia el banner por una línea y después quita los eventos más viejos; si
// el cuadro no cabe, el repintado deja basura.
func renderWatch(m bannerMode, version string, d watchData, now time.Time, rows int, loc ...*i18n.Localizer) string {
	var b, tail strings.Builder
	line := func(s string) { b.WriteString("  " + s + "\n") }
	label := func(l, v string) { dimLabel(&b, m, l, v) }
	where := d.Repo + " · " + now.Format("15:04:05")
	if d.WebURL != "" {
		where += " · página: " + d.WebURL
	}
	var head string // el banner completo, si cabe
	if m != bannerOff {
		head = renderBanner(m, version) + "\n  " + paint(m, cDim, where) + "\n\n"
	}
	brand := "bflow"
	if m == bannerColor {
		brand = fmt.Sprintf("\x1b[1;38;2;%d;%d;%dmbflow\x1b[0m", gradTo[0], gradTo[1], gradTo[2])
	}
	if d.Stale {
		where += " · " + staleNotice(loc...)
	}
	compact := "  " + brand + paint(m, cDim, " by innobytes.tech · "+version+" · "+where) + "\n\n"
	if head == "" {
		head = compact
	}
	var events []string
	if d.Active == nil {
		line("Ninguna tarea en curso. Empieza una con bflow start <ID> --lane full|light|hotfix")
	} else {
		v, st := *d.Active, d.Stats
		task := paint(m, cBold, v.ID)
		if v.Lane != "" {
			task += " · carril " + string(v.Lane)
		}
		if v.Round > 0 {
			task += fmt.Sprintf(" · ronda %d", v.Round)
		}
		line(task)
		if v.Title != "" {
			line(truncate(v.Title, 76))
		}
		b.WriteString("\n")
		for _, l := range progress(m, v, 74) {
			line(l)
		}
		b.WriteString("\n")
		if s, ok := nowStep(v, d.Events); ok {
			text := s.text
			if !s.since.IsZero() {
				text += paint(m, cDim, " · "+map[bool]string{true: "esperando ", false: ""}[s.human]+"hace "+dur(max(now.Sub(s.since), time.Second)))
			}
			if s.human {
				text = paint(m, cHuman, s.text) + strings.TrimPrefix(text, s.text)
			}
			line(paint(m, cBold, "AHORA  ") + "  " + who(m, s.human) + "  " + text)
		}
		if s, ok := nextStep(v); ok {
			line(paint(m, cBold, "DESPUÉS") + "  " + who(m, s.human) + "  " + s.text)
		}
		b.WriteString("\n")
		t := dur(st.Total) + " · agentes " + dur(st.Agent) + " · tú " + dur(st.Human)
		if st.Blocked > 0 {
			t += " · bloqueada " + dur(st.Blocked)
		}
		label("tiempo", t)
		var phases []string
		for _, p := range st.Phases {
			if d := p.Agent + p.Human + p.Blocked; d > 0 {
				phases = append(phases, string(p.Phase)+" "+dur(d))
			}
		}
		if len(phases) > 0 {
			label("", strings.Join(phases, " · "))
		}
		if st.TokensAvailable {
			label("tokens", metrics.Summary(st.Tokens))
			for _, a := range byNew(st.Agents) {
				u := st.Agents[a]
				label("", fmt.Sprintf("%-18s %s", agentName(a), metrics.Detail(u)))
			}
		}
		if len(d.Budget) > 0 {
			for i, l := range budgetLines(m, v, d.Budget, st) {
				lbl := "presupuesto"
				if i > 0 {
					lbl = ""
				}
				label(lbl, l)
			}
		}
		if n := st.Refused + st.Guarded + st.Nudged; n > 0 {
			label("fricción", fmt.Sprintf("%d rechazo(s) del flujo · %d bloqueo(s) de guard · %d fin(es) sin reporte", st.Refused, st.Guarded, st.Nudged))
		}
		if len(d.Events) > 0 {
			evs := d.Events[max(0, len(d.Events)-watchEvents):]
			w := 5 // hora; con fecha si alguno no es de hoy
			if !sameDay(evs[0].TS, now) {
				w = 11
			}
			for i := len(evs) - 1; i >= 0; i-- {
				events = append(events, fmt.Sprintf("  %s %s", paint(m, cDim, fmt.Sprintf("%-*s", w, eventTime(evs[i].TS, now))), describeEvent(evs[i])))
			}
		}
	}
	if d.Unassigned.Total() > 0 {
		tail.WriteString("\n")
		dimLabel(&tail, m, metrics.UnassignedLabel, " "+metrics.Summary(d.Unassigned))
	}
	watchOthers(&tail, m, d.Others)

	lines := func() int {
		n := strings.Count(head, "\n") + strings.Count(b.String(), "\n") + strings.Count(tail.String(), "\n")
		if len(events) > 0 {
			n += len(events) + 1
		}
		return n
	}
	if rows > 0 && lines() > rows {
		head = compact
	}
	for rows > 0 && lines() > rows && len(events) > 0 {
		events = events[:len(events)-1]
	}
	out := head + b.String()
	if len(events) > 0 {
		out += "\n" + strings.Join(events, "\n") + "\n"
	}
	out = strings.TrimRight(out+tail.String(), "\n")
	if all := strings.Split(out, "\n"); rows > 0 && len(all) > rows { // último recurso
		out = strings.Join(all[:rows], "\n")
	}
	return out
}

// budgetLines arma, en orden del carril, una línea "<fase> <gasto> / <presupuesto>
// nuevos" por cada fase presupuestada (R14); la fase que ya pasó su
// presupuesto se pinta en ámbar (cHuman), igual que el resto de avisos a la
// persona.
func budgetLines(m bannerMode, v engine.View, budget map[flow.Phase]int64, stats metrics.TaskStats) []string {
	used := map[flow.Phase]int64{}
	for _, ps := range stats.Phases {
		used[ps.Phase] = ps.Tokens.New()
	}
	var out []string
	for _, p := range v.Phases {
		b, ok := budget[p]
		if !ok || b <= 0 {
			continue
		}
		u := used[p]
		line := fmt.Sprintf("%s %s / %s nuevos", p, metrics.Human(u), metrics.Human(b))
		if u > b {
			line = paint(m, cHuman, line)
		}
		out = append(out, line)
	}
	return out
}

func watchOthers(b *strings.Builder, m bannerMode, others []engine.View) {
	if len(others) == 0 {
		return
	}
	b.WriteString("\n")
	for i, o := range others {
		l := ""
		if i == 0 {
			l = "otras"
		}
		dimLabel(b, m, l, o.ID+" · "+string(o.Phase)+" · "+nextLabel(o))
	}
}

// since es cuándo empezó el paso actual: la entrada a la fase o, si después
// se resolvió una gate sin cambiar de fase (una decisión), esa resolución.
func since(v engine.View, evs []store.Entry) time.Time {
	var t time.Time
	if v.Since != nil {
		t = *v.Since
	}
	for _, e := range evs {
		if (e.Event == "approve" || e.Event == "unblock" || e.Event == "block") && e.TS.After(t) {
			t = e.TS
		}
	}
	return t
}

func sameDay(a, b time.Time) bool {
	a, b = a.Local(), b.Local()
	return a.YearDay() == b.YearDay() && a.Year() == b.Year()
}

func eventTime(ts, now time.Time) string {
	if sameDay(ts, now) {
		return ts.Local().Format("15:04")
	}
	return ts.Local().Format("02/01 15:04")
}

// describeEvent resume un evento del log en una línea.
func describeEvent(e store.Entry) string {
	arrow := ""
	if e.To != "" && e.To != e.From {
		arrow = " → " + string(e.To)
	}
	if g, ok := e.Data["gate_opened"].(string); ok {
		arrow += " · gate " + g
	}
	note := ""
	if n := strings.TrimSpace(e.Note); n != "" {
		note = ": " + truncate(n, 60)
	}
	switch e.Event {
	case "start":
		return fmt.Sprintf("inicio en carril %v", e.Data["lane"]) + arrow
	case "report":
		return e.Agent + " " + e.Verdict + arrow
	case "approve":
		return "aprobada gate " + e.Gate + arrow
	case "reject":
		if e.Gate == "" {
			return "regresada" + note + arrow
		}
		return "rechazada gate " + e.Gate + note + arrow
	case "block":
		return "bloqueada" + note
	case "unblock":
		return "desbloqueada" + arrow
	case "guard":
		who := ""
		if e.Agent != "" {
			who = " a " + e.Agent
		}
		if e.Data["rule"] == "loop" {
			return fmt.Sprintf("ciclo: %s repitió %v %v veces", e.Agent, e.Data["tool"], e.Data["repeats"])
		}
		return fmt.Sprintf("guard bloqueó%s: %v", who, e.Data["rule"])
	case "refused":
		return fmt.Sprintf("el flujo rechazó %v (%v)", e.Data["event"], e.Data["code"])
	case "nudge":
		return e.Agent + " terminó sin reportar"
	case "refreeze":
		return "pruebas congeladas otra vez (bflow freeze)"
	}
	return e.Event + note + arrow
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func agentName(a string) string {
	switch a {
	case metrics.MainSession:
		return "sesión principal"
	case "":
		return "sin desglose"
	}
	return a
}

// byNew ordena las claves de mayor a menor gasto nuevo.
func byNew(m map[string]metrics.Usage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(x, y string) int {
		if d := m[y].New() - m[x].New(); d != 0 {
			return int(max(-1, min(1, d)))
		}
		return strings.Compare(x, y)
	})
	return keys
}

func runWatchOpen(c *Ctx) output.Envelope {
	e, err := engineFor(c)
	if err != nil {
		return output.Fail("config", err)
	}
	st, err := openWatch(e.Cfg.Root, e.Store.Dir(), e.Cfg.UI.Web || str(c.Flags, "web") == "true")
	if err != nil {
		return output.Fail("no_terminal", fmt.Errorf("no se pudo abrir el panel: %w; córrelo a mano en otra terminal: bflow watch", err))
	}
	env := output.OK("watch", map[string]any{"watch": st}, nil)
	env.Text = "panel abierto en otra ventana"
	if st == "already" {
		env.Text = "el panel ya está abierto"
	}
	return env
}
