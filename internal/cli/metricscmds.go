package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
	Register(&Command{Name: "stats", Summary: "tiempo por fase (agente, humano, bloqueada), iteraciones y tokens: stats [ID]", Run: runStats,
		Setup: func(fs *flag.FlagSet) {
			fs.Bool("calls", false, "una tabla por llamada del modelo, por corrida (requiere ID)")
			fs.Bool("reads", false, "lecturas por archivo y relectura entre agentes (requiere ID)")
		}})
	Register(&Command{Name: "statusline", Summary: "una línea para la barra de estado (lee una caché: milisegundos)", Run: runStatusline})
	Register(&Command{Name: "hook tokens", Summary: "hook Stop/SubagentStop: suma los tokens nuevos a la fase y, al terminar el turno, avisa si la tarea espera a la persona", Run: runHookTokens,
		Setup: func(fs *flag.FlagSet) {
			fs.String("tool", "", "herramienta cuyo evento se lee (opencode); vacío: Claude Code")
		}})
}

func runStats(c *Ctx) output.Envelope {
	e, err := engineFor(c)
	if err != nil {
		return output.Fail("config", err)
	}
	log, err := e.Store.Log("")
	if err != nil {
		return fail(err)
	}
	now := time.Now()
	wantCalls := str(c.Flags, "calls") == "true"
	if wantCalls && len(c.Args) == 0 {
		return output.Fail("usage", fmt.Errorf("--calls requiere un ID: stats ID --calls"))
	}
	wantReads := str(c.Flags, "reads") == "true"
	if wantReads && len(c.Args) == 0 {
		return output.Fail("usage", fmt.Errorf("--reads requiere un ID: stats ID --reads"))
	}
	if len(c.Args) > 0 {
		id := strings.ToUpper(c.Args[0])
		st := metrics.Compute(id, log, now)
		if st.Phase == "" {
			return output.Fail("not_found", fmt.Errorf("%s no tiene historial en .bflow/log.jsonl", id))
		}
		runs, _ := readCalls(e, id)
		st.Prefix = metrics.PrefixByAgent(runs)
		st.ModelSet, st.ModelSource = metrics.TaskModels(id, log)
		data := map[string]any{"stats": st}
		text := renderStats(st)
		if st.ModelSource != "" {
			text += fmt.Sprintf("\n  modelos (%s): %s", st.ModelSource, st.ModelSet)
		}
		if wantCalls || wantReads {
			text = statsLine(st)
		}
		if wantCalls {
			data["calls"] = runs
			if st.TokensAvailable {
				text += "\n" + renderCalls(runs, callsNote(st, runs))
			} else {
				text += "\n  " + noTokensText
			}
		}
		if wantReads {
			rs := readReadEvents(e, id)
			data["reads"] = rs
			text += "\n" + renderReads(rs)
		}
		env := output.OK("stats", data, nil)
		env.Text = text
		return env
	}
	recs, err := e.Store.List()
	if err != nil {
		return fail(err)
	}
	var all []metrics.TaskStats
	var b strings.Builder
	for _, r := range recs {
		st := metrics.Compute(r.Flow.ID, log, now)
		all = append(all, st)
		fmt.Fprintf(&b, "%s\n", statsLine(st))
	}
	data := map[string]any{"tasks": all}
	mv := metrics.CompareModels(log, now)
	data["models"] = mv
	if mv.Verdict != "sin_datos" || len(all) > 0 {
		fmt.Fprintf(&b, "%s\n", renderModelVerdict(mv, bannerFor(c) == bannerColor))
	}
	un := metrics.UnassignedUsage(e.Store.Dir())
	if un.Total() > 0 {
		data["unassigned"] = un
		fmt.Fprintf(&b, "%s  %s\n", metrics.UnassignedLabel, metrics.Summary(un))
	}
	env := output.OK("stats", data, nil)
	env.Text = strings.TrimRight(b.String(), "\n")
	if env.Text == "" {
		env.Text = "sin historial todavía"
	}
	return env
}

func dur(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	return metrics.Duration(d)
}

func statsLine(st metrics.TaskStats) string {
	l := fmt.Sprintf("%s · %s · %s (agente %s · humano %s", st.ID, st.Phase, dur(st.Total), dur(st.Agent), dur(st.Human))
	if st.Blocked > 0 {
		l += " · bloqueada " + dur(st.Blocked)
	}
	l += ")"
	var it []string
	for _, x := range []struct {
		n    int
		name string
	}{{st.Rejections, "rechazo(s)"}, {st.Rounds, "ronda(s)"}, {st.Decisions, "decisión(es)"}, {st.Splits, "división(es)"},
		{len(st.Hotfixes), "hotfix(es)"}, {st.Refused + st.Guarded + st.Nudged, "fricción"}} {
		if x.n > 0 {
			it = append(it, fmt.Sprintf("%d %s", x.n, x.name))
		}
	}
	if len(it) > 0 {
		l += " · " + strings.Join(it, ", ")
	}
	if st.NoRepro != "" {
		l += " · sin prueba de reproducción: " + st.NoRepro
	}
	if st.TokensAvailable {
		l += " · " + metrics.Summary(st.Tokens)
	}
	return l
}

func renderStats(st metrics.TaskStats) string {
	var b strings.Builder
	b.WriteString(statsLine(st) + "\n")
	row := "  %-13s %8s %8s %8s %8s %8s %8s\n"
	fmt.Fprintf(&b, row, "fase", "agente", "humano", "bloq.", "nuevos", "caché", "llamadas")
	for _, p := range st.Phases {
		fmt.Fprintf(&b, row, p.Phase, dur(p.Agent), dur(p.Human), dur(p.Blocked), tok(p.Tokens.New()), tok(p.Tokens.CacheRead), calls(p.Tokens.Calls))
	}
	if len(st.RejectionsByGate) > 0 {
		var gs []string
		for _, g := range slices.Sorted(maps.Keys(st.RejectionsByGate)) {
			gs = append(gs, fmt.Sprintf("%s %d", g, st.RejectionsByGate[g]))
		}
		b.WriteString("  rechazos por gate: " + strings.Join(gs, " · ") + "\n")
	}
	if len(st.Hotfixes) > 0 {
		b.WriteString("  hotfixes: " + strings.Join(st.Hotfixes, ", ") + "\n")
	}
	if st.Refused+st.Guarded+st.Nudged > 0 {
		fmt.Fprintf(&b, "  fricción: %d pedido(s) rechazado(s) por el flujo · %d bloqueo(s) de guard · %d fin(es) sin reporte\n", st.Refused, st.Guarded, st.Nudged)
	}
	if st.Review != nil {
		if l := st.Review.Lines(); len(l) > 0 {
			b.WriteString("  revisión: " + l[0] + "\n")
		}
	}
	usageTable(&b, "por agente", st.Agents, map[string]string{metrics.MainSession: "sesión principal", "": "sin desglose"}, st.Prefix, true)
	usageTable(&b, "por modelo", st.Models, map[string]string{"": "sin modelo"}, nil, false)
	if st.TokensAvailable {
		t := st.Tokens
		fmt.Fprintf(&b, "  tokens: %s (entrada %s · salida %s · caché escrita %s)", metrics.Summary(t),
			metrics.Human(t.Input), metrics.Human(t.Output), metrics.Human(t.CacheWrite))
	} else {
		b.WriteString("  " + noTokensText)
	}
	return b.String()
}

func calls(n int64) string {
	if n <= 0 {
		return "-"
	}
	return strconv.FormatInt(n, 10)
}

func tok(n int64) string {
	if n <= 0 {
		return "-"
	}
	return metrics.Human(n)
}

// usageTable desglosa tokens por clave, de mayor a menor gasto nuevo. Se omite
// si solo hay registros viejos sin esa clave.
func usageTable(b *strings.Builder, title string, m map[string]metrics.Usage, names map[string]string, prefix map[string]metrics.PrefixCost, withCache bool) {
	if _, legacy := m[""]; len(m) == 0 || (len(m) == 1 && legacy) {
		return
	}
	row := "  %-22s %8s %8s %8s %8s %9s\n"
	if withCache {
		row = "  %-22s %8s %8s %8s %8s %9s %7s %7s\n"
		fmt.Fprintf(b, row, title, "nuevos", "caché", "llamadas", "ctx máx", "ctx final", "escrita", "prefijo")
	} else {
		fmt.Fprintf(b, row, title, "nuevos", "caché", "llamadas", "ctx máx", "ctx final")
	}
	for _, k := range byNew(m) {
		name := k
		if n, ok := names[k]; ok {
			name = n
		}
		if withCache {
			fmt.Fprintf(b, row, name, tok(m[k].New()), tok(m[k].CacheRead), calls(m[k].Calls), tok(m[k].MaxContext), tok(m[k].LastContext),
				tok(m[k].CacheWrite), tok(prefix[k].Avg))
			continue
		}
		fmt.Fprintf(b, row, name, tok(m[k].New()), tok(m[k].CacheRead), calls(m[k].Calls), tok(m[k].MaxContext), tok(m[k].LastContext))
	}
}

// runStatusline no carga config ni toca git ni el tracker: solo lee la caché.
func runStatusline(c *Ctx) output.Envelope {
	dir := c.Dir
	if c.Stdin != nil {
		if f, ok := c.Stdin.(*os.File); !ok || !isTerminal(f) {
			raw, _ := io.ReadAll(io.LimitReader(c.Stdin, 1<<16))
			var in struct {
				Cwd       string `json:"cwd"`
				Workspace struct {
					CurrentDir string `json:"current_dir"`
				} `json:"workspace"`
			}
			if len(raw) > 0 && json.Unmarshal(raw, &in) == nil {
				if in.Workspace.CurrentDir != "" {
					dir = in.Workspace.CurrentDir
				} else if in.Cwd != "" {
					dir = in.Cwd
				}
			}
		}
	}
	sc := engine.ReadStatusCache(config.FindRoot(dir))
	env := output.OK("statusline", map[string]any{}, nil)
	if sc == nil {
		env.Text = "bflow"
		return env
	}
	env.Data["status"] = sc
	env.Text = StatuslineText(*sc, time.Now())
	return env
}

// StatuslineText arma la línea: API-171 · implementing · 1h42m · ronda 1 · 145k nuevos · 2.9M caché
func StatuslineText(sc engine.StatusCache, now time.Time) string {
	parts := []string{sc.ID, string(sc.Phase)}
	if sc.Gate != "" {
		parts[1] += " (" + sc.Gate + ")"
	}
	if !sc.Since.IsZero() && sc.Phase != flow.Done && sc.Phase != flow.Dropped {
		parts = append(parts, metrics.Duration(now.Sub(sc.Since)))
	}
	if sc.Round > 0 {
		parts = append(parts, fmt.Sprintf("ronda %d", sc.Round))
	}
	if sc.New+sc.Cached > 0 {
		parts = append(parts, metrics.Tokens(sc.New, sc.Cached))
	}
	return strings.Join(parts, " · ")
}

// nextLabel resume en pocas palabras lo que sigue en una tarea.
func nextLabel(v engine.View) string {
	if v.Gate != "" {
		return "decidir " + v.Gate
	}
	if len(v.Next.Agents) > 0 {
		var as []string
		for _, a := range v.Next.Agents {
			as = append(as, a.Agent)
		}
		return "agentes: " + strings.Join(as, ", ")
	}
	return v.Next.Action
}

// runHookTokens suma los tokens nuevos de un transcript a la tarea activa. En
// Stop lee solo la sesión principal y reparte cada respuesta en la fase en que
// ocurrió; en SubagentStop lee el transcript de ese agente (a la fase donde
// trabajó) y también el de la sesión principal, sin avisar ni podar. Al
// terminar el turno de la sesión principal, además avisa a la persona si la
// tarea la espera. Nunca falla ni imprime: es un hook.
func runHookTokens(c *Ctx) output.Envelope {
	quiet := output.Envelope{OK: true, Code: "tokens", Quiet: true}
	raw, _ := io.ReadAll(c.Stdin)
	if tool := str(c.Flags, "tool"); tool != "" {
		h := c.hooks(tool)
		if h == nil {
			return quiet
		}
		e, err := engineFor(c)
		if err != nil {
			return quiet
		}
		cacheDir := filepath.Join(e.Store.Dir(), "cache", h.Name())
		srcs, main := h.TokenSources(raw, cacheDir)
		if len(srcs) == 0 {
			return quiet
		}
		var prune func(*metrics.Cursor)
		if main {
			defer notifyActive(e)
			prune = func(cur *metrics.Cursor) { h.Prune(cacheDir, cur, 7*24*time.Hour) }
		}
		return tokensFrom(e, h.Name(), srcs, h.ReadUsage, prune, main)
	}
	if c.Agent == nil {
		return quiet
	}
	srcs := c.Agent.TokenSources(raw)
	if len(srcs) == 0 {
		return quiet
	}
	e, err := engineFor(c)
	if err != nil {
		return quiet
	}
	main := true
	for _, s := range srcs {
		if s.Agent != "" {
			main = false
		}
	}
	if main {
		defer notifyActive(e)
	}
	return tokensFrom(e, c.Agent.Name(), srcs, c.Agent.ReadUsage, nil, main)
}

// tokensFrom lee las líneas nuevas de cada fuente bajo el lock del cursor y
// reparte cada llamada a la tarea que marcó su sesión (R5-R7); las que no
// tienen tarea van a .bflow/metrics/calls.jsonl (R9). prune (si hay) limpia la
// caché y main borra las marcas viejas, ambos antes de guardar el cursor, que
// solo se guarda si todas las escrituras salieron bien (R10).
func tokensFrom(e *engine.Engine, tool string, srcs []TokenSource,
	read func(string, *metrics.Cursor) ([]metrics.Sample, error), prune func(*metrics.Cursor), main bool) output.Envelope {
	quiet := output.Envelope{OK: true, Code: "tokens", Quiet: true}
	curPath := filepath.Join(e.Store.Dir(), "cache", "tokens-cursor.json")
	_ = store.EnsureDirFor(curPath)
	unlock, err := store.LockFile(curPath+".lock", 5*time.Second, time.Minute)
	if err != nil {
		return quiet
	}
	defer unlock()
	var cur metrics.Cursor
	if b, err := os.ReadFile(curPath); err == nil {
		_ = json.Unmarshal(b, &cur)
	}
	type batch struct {
		src     TokenSource
		run     string
		samples []metrics.Sample
	}
	var batches []batch
	for _, src := range srcs {
		samples, err := read(src.Path, &cur)
		if err != nil {
			return quiet
		}
		if len(samples) > 0 {
			batches = append(batches, batch{src, metrics.RunKey(tool, src.Path), samples})
		}
	}
	if len(batches) > 0 {
		marks := metrics.ReadMarks(metrics.MarksPath(e.Store.Dir()))
		exists := map[string]bool{}
		for _, b := range batches {
			groups := map[string][]metrics.Sample{}
			var order []string
			for _, sm := range b.samples {
				id := metrics.TaskFor(marks, b.src, sm.TS)
				if id != "" {
					ok, seen := exists[id]
					if !seen {
						_, lerr := e.Store.Load(id)
						ok = lerr == nil
						exists[id] = ok
					}
					if !ok {
						id = ""
					}
				}
				if _, ok := groups[id]; !ok {
					order = append(order, id)
				}
				groups[id] = append(groups[id], sm)
			}
			for _, id := range order {
				if id == "" {
					if addUnassigned(e, tool, b.run, b.src.Agent, groups[id]) != nil {
						return quiet
					}
					continue
				}
				u, name, err := addTokens(e, id, tool, b.run, groups[id], b.src.Agent)
				if err != nil {
					return quiet
				}
				quiet.Data = map[string]any{"id": id, "agent": name, "tokens": u}
			}
		}
	}
	if prune != nil {
		prune(&cur)
	}
	if main {
		_ = metrics.PruneMarks(metrics.MarksPath(e.Store.Dir()), 7*24*time.Hour, time.Now())
	}
	if len(cur.Seen) > 5000 { // los offsets evitan releer; los ids viejos ya no hacen falta
		cur.Seen = nil
	}
	if b, err := json.Marshal(cur); err == nil {
		_ = store.WriteAtomic(curPath, b)
	}
	return quiet
}

// addUnassigned agrega las llamadas sin tarea a .bflow/metrics/calls.jsonl (R9).
// Corre bajo el lock del cursor, que ya está tomado.
func addUnassigned(e *engine.Engine, tool, run, agent string, samples []metrics.Sample) error {
	name := metrics.MainSession
	if agent != "" {
		name = strings.TrimPrefix(agent, flow.SubagentPrefix)
	}
	var buf []byte
	for _, s := range samples {
		r := run
		if s.Run != "" {
			r = s.Run
		}
		b, err := json.Marshal(metrics.Call{TS: s.TS, Run: r, Tool: tool, Agent: name, Model: s.Model, Msg: s.Msg,
			Input: s.Input, CacheWrite: s.CacheWrite, CacheRead: s.CacheRead, Output: s.Output, Final: s.Final})
		if err != nil {
			return err
		}
		buf = append(append(buf, b...), '\n')
	}
	p := metrics.UnassignedPath(e.Store.Dir())
	if err := store.EnsureDirFor(p); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(buf)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// addTokens reparte las muestras en las fases donde ocurrieron, las anota como
// entradas `tokens` de la tarea y suma el total a su caché. agent "" es la sesión principal.
// Un error de escritura se devuelve para que el cursor no avance.
func addTokens(e *engine.Engine, id, tool, run string, samples []metrics.Sample, agent string) (metrics.Usage, string, error) {
	var u metrics.Usage
	log, err := e.Store.Log(id)
	if err != nil {
		return u, "", err
	}
	name := metrics.MainSession
	if agent != "" {
		name = strings.TrimPrefix(agent, flow.SubagentPrefix)
	}
	var finals []metrics.Sample // cifras finales: solo van a calls.jsonl, no suman
	var counted []metrics.Sample
	for _, s := range samples {
		if s.Final {
			finals = append(finals, s)
		} else {
			counted = append(counted, s)
		}
	}
	samples = counted
	shares := metrics.Allot(log, id, strings.TrimPrefix(agent, flow.SubagentPrefix), samples)
	now := time.Now()
	var entries []store.Entry
	for _, s := range shares {
		d := map[string]any{"phase": string(s.Phase), "tool": tool,
			"input": s.Input, "output": s.Output, "cache_read": s.CacheRead, "cache_write": s.CacheWrite}
		if s.Calls > 0 {
			d["calls"], d["max_context"] = s.Calls, s.MaxContext
			if s.LastContext > 0 {
				d["last_context"] = s.LastContext
			}
		}
		if s.Model != "" {
			d["model"] = s.Model
		}
		entries = append(entries, store.Entry{TS: now, ID: id, Event: "tokens", Agent: name, By: e.User, Data: d})
		u.Add(s.Usage)
	}
	if err := e.Store.Append(entries...); err != nil {
		return u, "", err
	}
	var lines [][]byte
	phases := metrics.Phases(log, id, strings.TrimPrefix(agent, flow.SubagentPrefix), samples)
	for i, s := range samples {
		b, err := json.Marshal(metrics.Call{TS: s.TS, Run: run, Tool: tool, Agent: name, Phase: phases[i], Model: s.Model, Msg: s.Msg,
			Input: s.Input, CacheWrite: s.CacheWrite, CacheRead: s.CacheRead, Output: s.Output})
		if err == nil {
			lines = append(lines, b)
		}
	}
	for _, s := range finals {
		if b, err := json.Marshal(metrics.Call{TS: s.TS, Run: s.Run, Tool: tool, Agent: name, Msg: s.Msg,
			Input: s.Input, CacheWrite: s.CacheWrite, CacheRead: s.CacheRead, Output: s.Output, Final: true}); err == nil {
			lines = append(lines, b)
		}
	}
	if err := e.Store.AppendFile(id, metrics.CallsFile, lines); err != nil {
		return u, "", err
	}
	e.AddTokens(id, u.New(), u.CacheRead)
	return u, name, nil
}

// modelVerdictLine es la línea sin color del veredicto de modelos (stats y doctor).
func modelVerdictLine(v metrics.ModelVerdict, locs ...*i18n.Localizer) string {
	loc := i18n.DefaultBundle().New("es")
	if len(locs) > 0 && locs[0] != nil {
		loc = locs[0]
	}
	switch v.Verdict {
	case "sin_datos":
		return loc.T("doctor.models_no_data")
	case "sube":
		return loc.T("doctor.models_rises", strings.Join(v.Rose, " · "), modelCounts(v, loc))
	case "sin_regresion":
		return loc.T("doctor.models_no_regression", modelCounts(v, loc))
	}
	return loc.T("doctor.models_no_verdict", modelCounts(v, loc))
}

func modelCounts(v metrics.ModelVerdict, locs ...*i18n.Localizer) string {
	loc := i18n.DefaultBundle().New("es")
	if len(locs) > 0 && locs[0] != nil {
		loc = locs[0]
	}
	before := 0
	if v.Previous != nil {
		before = len(v.Previous.Tasks)
	}
	return loc.T("doctor.models_counts", len(v.Current.Tasks), metrics.ModelWindow, before, metrics.ModelWindow)
}

// renderModelVerdict da la línea (y las de vuelta) del veredicto de modelos. El
// rojo solo va en "sube" y en las líneas de vuelta, y solo con color.
func renderModelVerdict(v metrics.ModelVerdict, color bool) string {
	red := func(s string) string {
		if color {
			return "\x1b[31m" + s + "\x1b[0m"
		}
		return s
	}
	if v.Verdict != "sube" {
		return "modelos: " + modelVerdictLine(v)
	}
	var b strings.Builder
	b.WriteString("modelos: " + red("sube · "+strings.Join(v.Rose, " · ")) + " · " + modelCounts(v))
	for _, ch := range v.Changes {
		b.WriteString("\n" + red(fmt.Sprintf("  %s: %s → volver a %s", ch.Agent, ch.To, ch.From)))
	}
	b.WriteString("\n  cambia bflow.yaml (agents) y corre bflow render")
	return b.String()
}
