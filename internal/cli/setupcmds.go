package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"innobytes.tech/bflow/internal/agents"
	"innobytes.tech/bflow/internal/config"
	"innobytes.tech/bflow/internal/envcheck"
	"innobytes.tech/bflow/internal/i18n"
	"innobytes.tech/bflow/internal/metrics"
	"innobytes.tech/bflow/internal/output"
	"innobytes.tech/bflow/internal/setup"
	"innobytes.tech/bflow/internal/tracker"
	"innobytes.tech/bflow/internal/vcs"
)

// prompter pregunta por stderr y lee respuestas de stdin (consola o pipe).
type prompter struct {
	r   *bufio.Reader
	w   io.Writer
	yes bool
	loc *i18n.Localizer
}

func newPrompter(c *Ctx, yes bool) *prompter {
	in := c.Stdin
	if in == nil {
		in = strings.NewReader("")
	}
	return &prompter{r: bufio.NewReader(in), w: c.Stderr, yes: yes, loc: envLoc(c.Env)}
}

func (p *prompter) line() string {
	s, _ := p.r.ReadString('\n')
	return strings.TrimSpace(s)
}

// ask pregunta un texto; vacío o --yes devuelve def.
func (p *prompter) ask(q, def string) string {
	if p.yes {
		return def
	}
	if def != "" {
		fmt.Fprintf(p.w, "%s [%s]: ", q, def)
	} else {
		fmt.Fprintf(p.w, "%s: ", q)
	}
	if s := p.line(); s != "" {
		return s
	}
	return def
}

// choose muestra opciones numeradas; devuelve el índice elegido.
func (p *prompter) choose(q string, opts []string, def int) int {
	if p.yes || len(opts) == 1 {
		return def
	}
	fmt.Fprintln(p.w, q)
	for i, o := range opts {
		fmt.Fprintf(p.w, "  %d. %s\n", i+1, o)
	}
	loc := p.loc
	if loc == nil {
		loc = i18n.DefaultLocalizer()
	}
	fmt.Fprintf(p.w, "%s", loc.T("init.prompt_choose", def+1))
	s := p.line()
	if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= len(opts) {
		return n - 1
	}
	for i, o := range opts {
		if strings.EqualFold(s, o) || strings.HasPrefix(strings.ToLower(o), strings.ToLower(s)+" ") {
			return i
		}
	}
	return def
}

func gitOut(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func init() {
	Register(&Command{Name: "init", Summary: "crea bflow.yaml detectando stack, remoto, rama base y pasos de check: init [--yes] [--profile p] [--tracker local|plane|github] [--project P (github: owner/N)]|plane] [--project P]",
		Setup: func(fs *flag.FlagSet) {
			for _, f := range []string{"stack", "tracker", "project", "url", "workspace", "profile", "host", "base", "agent", "lang"} {
				fs.String(f, "", "")
			}
			fs.Bool("yes", false, "no preguntar: usar lo detectado y los valores por defecto")
			fs.Bool("force", false, "sobrescribir un bflow.yaml existente")
		},
		Run: runInit})

	Register(&Command{Name: "profile add", Summary: "crea o completa un perfil global: profile add <nombre> [--tracker --url --workspace --host --base --agent]",
		Setup: func(fs *flag.FlagSet) {
			for _, f := range []string{"tracker", "url", "workspace", "host", "base", "agent"} {
				fs.String(f, "", "")
			}
		},
		Run: runProfileAdd})
	Register(&Command{Name: "profile list", Summary: "lista los perfiles globales", Run: runProfileList})
	Register(&Command{Name: "profile use", Summary: "usa un perfil en este repo: profile use <nombre>", Run: runProfileUse})
	Register(&Command{Name: "doctor", Summary: "valida config, herramientas, conexiones, entorno y hooks", Run: runDoctor})
}

// panelChoice es la respuesta a qué abrir al empezar una tarea. La página
// la sirve la ventana del panel, así que no hay navegador sin terminal.
type panelChoice struct {
	label, summary string
	watch, web     bool
}

var panelChoices = []panelChoice{
	{"el panel en otra terminal y su página en el navegador", "panel y navegador", true, true},
	{"solo el panel en otra terminal", "solo el panel", true, false},
	{"nada: los abro yo (bflow watch --open, bflow ui)", "nada", false, false},
}

func panelLabels() []string {
	var out []string
	for _, p := range panelChoices {
		out = append(out, p.label)
	}
	return out
}

// parseAgentFlag separa --agent por comas y lo valida contra las herramientas conocidas.
func parseAgentFlag(v string) ([]string, error) {
	if v == "" {
		return nil, nil
	}
	var out []string
	for _, n := range strings.Split(v, ",") {
		n = strings.TrimSpace(n)
		switch {
		case !slices.Contains(config.Known.Agents, n):
			return nil, fmt.Errorf("--agent %q no existe (disponibles: %s)", n, strings.Join(config.Known.Agents, ", "))
		case slices.Contains(out, n):
			return nil, fmt.Errorf("--agent repite %q", n)
		}
		out = append(out, n)
	}
	return out, nil
}

func runInit(c *Ctx) output.Envelope {
	ctx := context.Background()
	root := config.FindRoot(c.Dir)
	path := filepath.Join(root, config.RepoFile)
	if _, err := os.Stat(path); err == nil && str(c.Flags, "force") != "true" && !c.DryRun {
		return output.Rejected("exists", path+" ya existe (usa --force para regenerarlo, o edita el archivo)")
	}
	flagAgents, err := parseAgentFlag(str(c.Flags, "agent"))
	if err != nil {
		return output.Fail("usage", err)
	}
	p := newPrompter(c, str(c.Flags, "yes") == "true")

	// Idioma / Language
	langFlag := str(c.Flags, "lang")
	if langFlag != "" && !slices.Contains(i18n.Supported, langFlag) {
		return output.Fail("usage", fmt.Errorf("idioma/lang %q no existe (disponibles: %s)", langFlag, strings.Join(i18n.Supported, ", ")))
	}
	if langFlag == "" {
		if !p.yes {
			detected := i18n.DetectLang("")
			defIdx := 0
			if detected == "es" {
				defIdx = 1
			}
			langOpts := []string{"en (English)", "es (Español)"}
			langIdx := p.choose("Language / Idioma", langOpts, defIdx)
			langFlag = i18n.Supported[langIdx]
		} else {
			langFlag = i18n.DetectLang("")
		}
	}
	loc := i18n.DefaultBundle().New(langFlag)
	if c.Env != nil {
		c.Env.Loc = loc
	}
	p.loc = loc

	det := setup.Detect(root, setup.Tools{Has: func(n string) bool { _, err := exec.LookPath(n); return err == nil }})

	a := setup.Answers{Stack: det.Stack, Steps: det.Steps, Quick: det.Quick, RequireEnv: det.DBEnv, Lang: langFlag}
	if s := str(c.Flags, "stack"); s != "" {
		a.Stack = s
	}

	// Perfil
	profiles, err := config.LoadProfiles()
	if err != nil {
		return output.Fail("config", err)
	}
	a.Profile = str(c.Flags, "profile")
	if a.Profile == "" && len(profiles) > 0 {
		noneLabel := loc.T("init.profile_none")
		names := []string{noneLabel}
		for n := range profiles {
			names = append(names, n)
		}
		sort.Strings(names[1:])
		if i := p.choose(loc.T("init.prompt_profile"), names, 0); i > 0 {
			a.Profile = names[i]
		}
	}
	var prof setup.Profile
	if a.Profile != "" {
		pc, ok := profiles[a.Profile]
		if !ok {
			return output.Fail("profile", fmt.Errorf("el perfil %q no existe (bflow profile list)", a.Profile))
		}
		prof = setup.Profile{Tracker: pc.Tracker.Adapter, URL: pc.Tracker.URL, Workspace: pc.Tracker.Workspace, Host: pc.VCS.Host, BaseBranch: pc.VCS.BaseBranch, Agent: pc.Agent}
	}

	// Host desde el remoto (el tracker github depende de él)
	remote := gitOut(root, "remote", "get-url", "origin")
	detHost, repo := setup.HostFromRemote(remote)
	a.Host = firstNonEmpty(str(c.Flags, "host"), prof.Host, detHost)
	if a.Host == "none" {
		a.Host = ""
	}

	// Tracker
	a.Tracker = firstNonEmpty(str(c.Flags, "tracker"), prof.Tracker)
	if a.Tracker == "" {
		names, labels := []string{"local", "plane"}, []string{loc.T("init.tracker_local_label"), "plane"}
		if a.Host == "github" {
			names, labels = append(names, "github"), append(labels, loc.T("init.tracker_github_label"))
		}
		a.Tracker = names[p.choose(loc.T("init.prompt_tracker"), labels, 0)]
	}
	if a.Tracker == "github" && a.Host != "github" {
		return output.FailLoc(loc, "incomplete", errors.New(loc.T("init.github_needs_github_host")))
	}
	if a.Tracker == "plane" {
		a.TrackerURL = firstNonEmpty(str(c.Flags, "url"), prof.URL)
		if a.TrackerURL == "" {
			a.TrackerURL = p.ask(loc.T("init.prompt_plane_url"), "")
		}
		a.Workspace = firstNonEmpty(str(c.Flags, "workspace"), prof.Workspace)
		if a.Workspace == "" {
			a.Workspace = p.ask(loc.T("init.prompt_plane_workspace"), "")
		}
		a.Project = strings.ToUpper(str(c.Flags, "project"))
		if a.Project == "" {
			var ids []string
			if c.Projects != nil {
				if ps, err := c.Projects(ctx, "plane", a.TrackerURL, a.Workspace); err == nil {
					for _, pr := range ps {
						ids = append(ids, pr.ID+" · "+pr.Name)
					}
				} else {
					fmt.Fprintln(c.Stderr, "no se pudieron listar los proyectos:", err)
				}
			}
			if len(ids) > 0 && !p.yes {
				a.Project = strings.Fields(ids[p.choose(loc.T("init.prompt_plane_project"), ids, 0)])[0]
			} else {
				a.Project = strings.ToUpper(p.ask(loc.T("init.prompt_plane_project_id"), ""))
			}
		}
		if a.TrackerURL == "" || a.Workspace == "" || a.Project == "" {
			return output.FailLoc(loc, "incomplete", errors.New(loc.T("init.error_plane_incomplete")))
		}
	} else if a.Tracker == "github" {
		a.Project = str(c.Flags, "project")
		if a.Project == "" && c.Projects != nil {
			ps, err := c.Projects(ctx, "github", "", repo)
			if err != nil {
				fmt.Fprintln(c.Stderr, "no se pudieron listar los Projects:", err)
			}
			if len(ps) > 0 && !p.yes {
				ids := []string{loc.T("init.github_project_none")}
				for _, pr := range ps {
					ids = append(ids, pr.ID+" · "+pr.Name)
				}
				if i := p.choose(loc.T("init.prompt_github_project"), ids, 0); i > 0 {
					a.Project = strings.Fields(ids[i])[0]
				}
			}
		}
	} else if a.Tracker == "local" {
		a.Project = strings.ToUpper(str(c.Flags, "project"))
	}

	// Rama base desde el remoto
	head := strings.TrimPrefix(gitOut(root, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"), "origin/")
	var branches []string
	for _, b := range strings.Split(gitOut(root, "branch", "-r", "--format=%(refname:short)"), "\n") {
		branches = append(branches, strings.TrimPrefix(strings.TrimSpace(b), "origin/"))
	}
	if head == "" {
		head = gitOut(root, "rev-parse", "--abbrev-ref", "HEAD")
	}
	a.BaseBranch = firstNonEmpty(str(c.Flags, "base"), prof.BaseBranch)
	if a.BaseBranch == "" {
		a.BaseBranch = p.ask(loc.T("init.prompt_base_branch"), setup.PickBase(head, branches))
	}
	a.Agent = prof.Agent
	if len(flagAgents) > 0 {
		a.Agent = flagAgents
	}
	if len(a.Agent) == 0 {
		if usesClaude(root) {
			a.Agent = append(a.Agent, "claude")
		}
		if usesOpenCode(root) {
			a.Agent = append(a.Agent, "opencode")
		}
	}

	if len(a.Steps) > 0 && !p.yes {
		fmt.Fprintln(c.Stderr, loc.T("init.proposed_check_steps"))
		for _, s := range a.Steps {
			opt := ""
			if s.Optional {
				opt = loc.T("init.check_step_optional", s.Needs)
			}
			fmt.Fprintf(c.Stderr, "  - %s: %s%s\n", s.Name, s.Run, opt)
		}
		defAns := loc.T("init.prompt_use_steps_def")
		ans := strings.ToLower(p.ask(loc.T("init.prompt_use_steps"), defAns))
		if strings.HasPrefix(ans, "n") {
			a.Steps, a.Quick = nil, nil
		}
	}

	// Qué se abre al empezar una tarea es de la persona, no del repo: se
	// pregunta una vez por máquina y va a la config global.
	var panel *panelChoice
	if !p.yes && !c.DryRun && desktop(goos, os.Getenv) == nil {
		if decided, err := config.UIDecided(); err == nil && !decided {
			pc := panelChoices[p.choose("Al empezar una tarea, ¿qué abro? (es tuya: va a tu config global, no al repo)", panelLabels(), 0)]
			panel = &pc
		}
	}

	y, err := setup.RenderYAML(a, prof)
	if err != nil {
		return output.Fail("render", err)
	}
	data := map[string]any{"path": path, "stack": a.Stack, "tracker": a.Tracker, "host": a.Host, "base": a.BaseBranch, "steps": len(a.Steps), "yaml": y}
	if repo != "" {
		data["repo"] = repo
	}
	if c.DryRun {
		env := output.OK("init_plan", data, nil)
		env.Text = y
		return env
	}
	if err := os.WriteFile(path, []byte(y), 0o644); err != nil {
		return output.Fail("write", err)
	}
	if _, err := config.Load(root); err != nil {
		return output.Fail("invalid", fmt.Errorf("%s", loc.T("init.yaml_generated_invalid", err)))
	}
	_ = config.RememberRepo(root)
	panelText := ""
	if panel != nil {
		if err := config.SaveUI(map[string]any{"watch": panel.watch, "web": panel.web}); err != nil {
			fmt.Fprintln(c.Stderr, "no se pudo guardar qué abrir al empezar una tarea:", err)
		} else {
			data["ui"] = map[string]bool{"watch": panel.watch, "web": panel.web}
			panelText = "\nal empezar una tarea: " + panel.summary + " (ui: en " + config.GlobalPath() + ")"
		}
	}
	var next []string
	if a.Tracker == "plane" {
		next = append(next, "bflow connect plane (si aún no hay token)", "bflow tracker setup --dry-run")
	}
	if a.Tracker == "github" {
		next = append(next, "bflow connect github (token del tracker y de los PR)", "bflow tracker setup --dry-run")
	} else if a.Host == "github" {
		next = append(next, "bflow connect github (para abrir PR solo)")
	}
	if slices.Contains(a.Agent, "claude") {
		next = append(next, "bflow render (genera los agentes en .claude/agents; commitéalos)")
	}
	if slices.Contains(a.Agent, "opencode") {
		next = append(next, "bflow install opencode (comando /bflow y agentes en .opencode/agents; define models.opencode en bflow.yaml (equipo) o en "+config.GlobalPath()+" (solo tú; va a tu opencode.json, no al repo))")
	}
	next = append(next, "bflow doctor")
	env := output.OK("initialized", data, nil)
	env.Text = fmt.Sprintf("%s\n%s\n  %s",
		loc.T("init.created", orDash(a.Stack), a.Tracker, orDash(a.BaseBranch), len(a.Steps), panelText),
		loc.T("init.next_label"),
		strings.Join(next, "\n  "))
	if m := bannerFor(c); m != bannerOff {
		env.Text = renderBanner(m, c.Version) + "\n" + env.Text
	}
	return env
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func runProfileAdd(c *Ctx) output.Envelope {
	if len(c.Args) != 1 {
		return output.Fail("usage", errors.New("uso: bflow profile add <nombre> [--tracker --url --workspace --host --base --agent]"))
	}
	v := map[string]any{}
	tr := map[string]any{}
	for flagName, key := range map[string]string{"tracker": "adapter", "url": "url", "workspace": "workspace"} {
		if s := str(c.Flags, flagName); s != "" {
			tr[key] = s
		}
	}
	if len(tr) > 0 {
		v["tracker"] = tr
	}
	vc := map[string]any{}
	if s := str(c.Flags, "host"); s != "" {
		vc["host"] = s
	}
	if s := str(c.Flags, "base"); s != "" {
		vc["base_branch"] = s
	}
	if len(vc) > 0 {
		v["vcs"] = vc
	}
	if s := str(c.Flags, "agent"); s != "" {
		v["agent"] = s
	}
	if len(v) == 0 {
		return output.Fail("usage", errors.New("indica al menos un valor (--tracker, --url, --workspace, --host, --base, --agent)"))
	}
	if err := config.SaveProfile(c.Args[0], v); err != nil {
		return output.Fail("profile", err)
	}
	env := output.OK("profile_saved", map[string]any{"name": c.Args[0], "path": config.GlobalPath()}, nil)
	env.Text = fmt.Sprintf("perfil %s guardado en %s", c.Args[0], config.GlobalPath())
	return env
}

func runProfileList(c *Ctx) output.Envelope {
	ps, err := config.LoadProfiles()
	if err != nil {
		return output.Fail("config", err)
	}
	var names []string
	for n := range ps {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		p := ps[n]
		fmt.Fprintf(&b, "%s · tracker %s %s · host %s · base %s\n", n, orDash(p.Tracker.Adapter), p.Tracker.URL, orDash(p.VCS.Host), orDash(p.VCS.BaseBranch))
	}
	env := output.OK("profiles", map[string]any{"profiles": names, "path": config.GlobalPath()}, nil)
	env.Text = strings.TrimRight(b.String(), "\n")
	if env.Text == "" {
		env.Text = "sin perfiles (bflow profile add <nombre> …)"
	}
	return env
}

func runProfileUse(c *Ctx) output.Envelope {
	if len(c.Args) != 1 {
		return output.Fail("usage", errors.New("uso: bflow profile use <nombre>"))
	}
	ps, err := config.LoadProfiles()
	if err != nil {
		return output.Fail("config", err)
	}
	if _, ok := ps[c.Args[0]]; !ok {
		return output.Fail("profile", fmt.Errorf("el perfil %q no existe (bflow profile list)", c.Args[0]))
	}
	root := config.FindRoot(c.Dir)
	if err := config.SetRepoProfile(root, c.Args[0]); err != nil {
		return output.Fail("write", err)
	}
	if _, err := config.Load(root); err != nil {
		return output.Fail("invalid", err)
	}
	env := output.OK("profile_used", map[string]any{"name": c.Args[0]}, nil)
	env.Text = "este repo usa el perfil " + c.Args[0]
	return env
}

// ---------- doctor ----------

type docItem struct {
	Area   string `json:"area"`
	Status string `json:"status"` // ok | warn | fail
	Detail string `json:"detail"`
}

func runDoctor(c *Ctx) output.Envelope {
	ctx := context.Background()
	var items []docItem
	add := func(area, status, format string, a ...any) {
		items = append(items, docItem{Area: area, Status: status, Detail: fmt.Sprintf(format, a...)})
	}
	e, err := engineFor(c)
	if err != nil {
		add("config", "fail", "%v", err)
		return doctorEnvelope(items)
	}
	cfg := e.Cfg
	src := cfg.Sources.Repo
	if src == "" {
		src = "sin bflow.yaml (valores por defecto; bflow init)"
	}
	if cfg.Sources.Profile != "" {
		src += " · perfil " + cfg.Sources.Profile
	}
	add("config", "ok", "%s", src)

	if e.Git == nil {
		add("git", "warn", "no es un repo git: sin ramas, PR ni check")
	} else if r, err := e.Git.RemoteURL(ctx); err != nil {
		add("git", "warn", "sin remoto %s: no hay push ni PR", cfg.VCS.Remote)
	} else {
		add("git", "ok", "remoto %s · base %s", r, orDash(cfg.VCS.BaseBranch))
	}

	if log, err := e.Store.Log(""); err != nil {
		add("veredicto-modelos", "warn", "no se pudo leer el log: %v", err)
	} else {
		v := metrics.CompareModels(log, time.Now())
		status := "ok"
		if v.Verdict == "sube" {
			status = "warn"
		}
		add("veredicto-modelos", status, "%s", modelVerdictLine(v))
	}

	var missing, optional []string
	seen := map[string]bool{}
	for _, s := range cfg.Check.Steps {
		tool := s.Needs
		if tool == "" {
			if f := strings.Fields(s.Run); len(f) > 0 {
				tool = f[0]
			}
		}
		if tool == "" || seen[tool] {
			continue
		}
		seen[tool] = true
		if _, err := exec.LookPath(tool); err != nil {
			if s.Optional {
				optional = append(optional, tool)
			} else {
				missing = append(missing, tool)
			}
		}
	}
	switch {
	case len(cfg.Check.Steps) == 0:
		add("herramientas", "warn", "sin pasos de check: DONE se acepta sin verificar (bflow init los propone)")
	case len(missing) > 0:
		add("herramientas", "fail", "faltan %s (pasos obligatorios del check)", strings.Join(missing, ", "))
	case len(optional) > 0:
		add("herramientas", "warn", "check degradado: faltan %s (opcionales)", strings.Join(optional, ", "))
	default:
		add("herramientas", "ok", "%d pasos de check con sus herramientas instaladas", len(cfg.Check.Steps))
	}

	doctorTracker(ctx, e.Tracker, cfg, add)

	if e.Host != nil {
		if u, ok := e.Host.(interface {
			User(context.Context) (string, error)
		}); ok {
			if login, err := u.User(ctx); errors.Is(err, vcs.ErrNoCredentials) {
				add("host", "warn", "%s sin token: el PR se deja listo con URL de compare (bflow connect %s)", e.Host.Name(), e.Host.Name())
			} else if err != nil {
				add("host", "fail", "%v", err)
			} else if err := checkRepo(ctx, e.Host); err != nil {
				add("host", "fail", "%v", err)
			} else {
				add("host", "ok", "%s como %s", e.Host.Name(), login)
			}
		}
	} else if cfg.VCS.Host == "" {
		add("host", "ok", "sin host remoto configurado: push y PR a mano")
	}

	var runs []string
	for _, st := range cfg.Check.Steps {
		runs = append(runs, st.Run)
	}
	if n, gaps := setup.CIGaps(cfg.Root, runs); len(gaps) > 0 {
		for _, g := range gaps {
			add("ci", "warn", "%s: agrégalo a check.steps para verlo antes del PR", g)
		}
	} else if n > 0 {
		add("ci", "ok", "el check cubre los %d comandos de verificación de CI", n)
	}

	ec := cfg.Env
	if len(ec.HealthPaths)+len(ec.TCP)+len(ec.RequireEnv) > 0 {
		res := envcheck.Run(ctx, ec, os.Getenv)
		if n := envcheck.Failed(res); n > 0 {
			add("entorno", "warn", "%d de %d verificaciones fallan (bflow env check)", n, len(res))
		} else {
			add("entorno", "ok", "%d verificaciones", len(res))
		}
	}

	if cfg.Agent.Has("claude") {
		b, err := os.ReadFile(filepath.Join(cfg.Root, ".claude", "settings.json"))
		var missing []string
		for _, h := range []string{"bflow guard", "bflow hook session-start", "bflow hook subagent-stop"} {
			if !strings.Contains(string(b), h) {
				missing = append(missing, h)
			}
		}
		switch {
		case err != nil:
			add("agente", "warn", "claude: falta .claude/settings.json con los hooks de bflow (corre bflow install claude)")
		case len(missing) > 0:
			add("agente", "warn", "claude: .claude/settings.json no llama a %s (corre bflow install claude)", strings.Join(missing, ", "))
		case !strings.Contains(string(b), "bflow guard --reads"):
			add("agente", "warn", "claude: el guard no ve Read; la cobertura del reviewer no se mide (corre bflow install claude)")
		default:
			add("agente", "ok", "claude: hooks de bflow instalados")
		}
		if home, err := os.UserHomeDir(); err == nil {
			switch path, state := c.Agent.SkillState(home); state {
			case "ok":
				add("skill", "ok", "claude: skill de bflow al día (%s)", path)
			case "missing":
				add("skill", "warn", "claude: falta la skill de bflow (%s); corre bflow install claude", path)
			default:
				add("skill", "warn", "claude: la skill instalada (%s) difiere de la de este bflow; corre bflow install claude", path)
			}
		}
		switch have, min, ok, err := c.Agent.Version(); {
		case err != nil:
			add("agente", "warn", "claude: no se pudo leer la versión (%v); bflow necesita %s o posterior", err, min)
		case !ok:
			add("agente", "warn", "claude: versión %s; bflow necesita %s o posterior (claude update)", have, min)
		default:
			add("agente", "ok", "claude: versión %s", have)
		}
		var procs []string
		for _, s := range c.Agent.Skills(cfg.Root) {
			if s.Name == "bflow" || slices.Contains(cfg.Doctor.IgnoreSkills, s.Name) {
				continue
			}
			if t := agents.ProcessTerms(s.Description); t != nil {
				procs = append(procs, fmt.Sprintf("%s (%s)", s.Name, strings.Join(t, ", ")))
			}
		}
		if len(procs) > 0 {
			add("skills", "warn", "parecen skills de proceso y pueden chocar con bflow (ramas, PR, tracker, specs): %s. Si no chocan, agrégalas a doctor.ignore_skills", strings.Join(procs, "; "))
		}
		status, detail := startupCheck(c.Agent.StartupContext(cfg.Root))
		add("contexto", status, "%s", detail)
		if cfg.Guard.ForbidCoauthor && !c.Agent.CoauthorOff(cfg.Root) {
			add("agente", "warn", `claude agrega Co-Authored-By a sus commits y guard.forbid_coauthor los bloquea: cada commit se rechaza y se repite. Corre bflow install claude, o pon "attribution": { "commit": "", "pr": "" } en .claude/settings.json`)
		}
	}

	if len(cfg.Agent) > 0 {
		switch p, err := planRender(c, cfg); {
		case err != nil:
			add("agentes", "fail", "%v", err)
		case len(p.Conflicts) > 0:
			add("agentes", "fail", "%s existe y no lo generó bflow: renómbralo y corre bflow render", strings.Join(p.Conflicts, ", "))
		case len(p.Changed)+len(p.Stale) > 0:
			add("agentes", "warn", "desactualizados: %s (bflow render y commitea)", strings.Join(append(p.Changed, p.Stale...), ", "))
		default:
			add("agentes", "ok", "%d archivos generados y al día", len(p.Files))
		}
	}
	if cfg.Agent.Has("opencode") {
		doctorOpenCode(c, cfg, add)
	}

	if cfg.Tracker.Adapter == "local" {
		seen := map[string]bool{}
		for _, e := range strings.Fields(gitOut(cfg.Root, "log", "--since=90.days", "--format=%ae")) {
			seen[strings.ToLower(e)] = true
		}
		if len(seen) >= 2 {
			add("tracker", "warn", ".bflow/ no se comparte entre personas y hay %d autores en los últimos 90 días: usa tracker github o plane para que todos vean las mismas tareas", len(seen))
		}
	}

	if exe, err := os.Executable(); err == nil {
		var best time.Duration
		for i := 0; i < 2; i++ {
			t0 := time.Now()
			_ = exec.Command(exe, "version").Run()
			if d := time.Since(t0); best == 0 || d < best {
				best = d
			}
		}
		if best > 250*time.Millisecond {
			add("arranque", "warn", "bflow tarda %dms en arrancar: suele ser el antivirus escaneando un ejecutable sin firmar; el guard corre antes de cada herramienta (considera excluir %s)", best.Milliseconds(), filepath.Dir(exe))
		} else {
			add("arranque", "ok", "%dms", best.Milliseconds())
		}
	}
	return doctorEnvelope(items)
}

func doctorTracker(ctx context.Context, t tracker.Tracker, cfg *config.Config, add func(string, string, string, ...any)) {
	if cfg.Tracker.Adapter == "local" {
		add("tracker", "ok", "local")
		return
	}
	gh := cfg.Tracker.Adapter == "github"
	if pl, ok := t.(tracker.ProjectLister); ok && (!gh || cfg.Tracker.Project != "") {
		ps, err := pl.Projects(ctx)
		if err != nil {
			add("tracker", "fail", "%v", err)
			return
		}
		if !slices.ContainsFunc(ps, func(p tracker.Project) bool { return strings.EqualFold(p.ID, cfg.Tracker.Project) }) {
			if gh {
				add("tracker", "fail", "el project %s no aparece entre los que ve el token (¿falta el permiso de Projects? bflow connect github)", cfg.Tracker.Project)
			} else {
				add("tracker", "fail", "el proyecto %s no está en %s", cfg.Tracker.Project, cfg.Tracker.Workspace)
			}
			return
		}
	}
	msg := fmt.Sprintf("%s · proyecto %s", cfg.Tracker.Adapter, cfg.Tracker.Project)
	if gh {
		msg = "github · etiquetas bflow:* (sin project)"
		if cfg.Tracker.Project != "" {
			msg = "github · project " + cfg.Tracker.Project + " (campo Status)"
		}
	}
	if sl, ok := t.(tracker.StateLister); ok {
		sts, err := sl.StateMap(ctx)
		if err != nil {
			add("tracker", "fail", "%v", err)
			return
		}
		written := map[string]bool{}
		for _, s := range sts {
			for _, w := range s.Writes {
				written[w] = true
			}
		}
		var lack []string
		for _, p := range tracker.PhaseOrder {
			if !written[string(p)] {
				lack = append(lack, string(p))
			}
		}
		if len(lack) > 0 {
			add("tracker", "warn", "%s · sin estado para: %s (bflow tracker setup)", msg, strings.Join(lack, ", "))
			return
		}
	}
	add("tracker", "ok", "%s", msg)
}

func doctorEnvelope(items []docItem) output.Envelope {
	var b strings.Builder
	fails := 0
	for _, it := range items {
		mark := map[string]string{"ok": "✅", "warn": "⚠️ ", "fail": "❌"}[it.Status]
		if it.Status == "fail" {
			fails++
		}
		fmt.Fprintf(&b, "%s %-13s %s\n", mark, it.Area, it.Detail)
	}
	env := output.OK("doctor", map[string]any{"checks": items, "failed": fails}, nil)
	env.Text = strings.TrimRight(b.String(), "\n")
	if fails > 0 {
		env.OK = false
		env = env.WithExit(output.ExitError)
	}
	return env
}

// doctorOpenCode revisa la integración con OpenCode: versión, comando /bflow,
// alias de modelo sin resolver, el plugin de guard y tokens y lo que OpenCode no cubre.
func doctorOpenCode(c *Ctx, cfg *config.Config, add func(string, string, string, ...any)) {
	t := c.tool("opencode")
	if t == nil {
		add("agente", "fail", "opencode: no hay adaptador en este bflow")
		return
	}
	switch have, _, _, err := t.Version(); {
	case err != nil:
		add("agente", "warn", "opencode: no se pudo leer la versión (%v); ¿está instalado y en el PATH?", err)
	default:
		plug := ".opencode/plugins/bflow2/"
		if n, _ := strconv.Atoi(strings.SplitN(strings.TrimPrefix(have, "v"), ".", 2)[0]); n == 1 {
			plug = ".opencode/plugins/bflow.js"
		}
		add("agente", "ok", "opencode: versión %s, usa %s", have, plug)
	}
	if home, err := os.UserHomeDir(); err == nil || os.Getenv("XDG_CONFIG_HOME") != "" {
		switch path, state := t.SkillState(home); state {
		case "ok":
			add("comando", "ok", "opencode: comando /bflow al día (%s)", path)
		case "missing":
			add("comando", "warn", "opencode: falta el comando /bflow; corre bflow install opencode")
		default:
			add("comando", "warn", "opencode: el comando /bflow instalado (%s) difiere del de este bflow; corre bflow install opencode", path)
		}
	}
	var logFs []LogFinding
	if ld, ok := t.(LogDoctor); ok {
		logFs = ld.DoctorLog(cfg.Root, time.Now())
	}
	regionBad := false
	for _, f := range logFs {
		regionBad = regionBad || f.Area == "modelos" && f.Level == "warn"
	}
	if p, err := planRender(c, cfg); err == nil {
		if un := p.Unresolved["opencode"]; len(un) > 0 {
			var parts []string
			for _, u := range un {
				parts = append(parts, fmt.Sprintf("%s (%s)", u.Alias, strings.Join(u.Agents, ", ")))
			}
			add("modelos", "warn", "opencode: alias sin equivalente, esos agentes salen sin model: %s; define models.opencode.<alias> en bflow.yaml (equipo) o en %s (solo tú)", strings.Join(parts, "; "), config.GlobalPath())
		} else if !regionBad {
			add("modelos", "ok", "opencode: modelos de los agentes resueltos")
		}
		for _, f := range logFs {
			if f.Area == "modelos" {
				add(f.Area, f.Level, "%s", f.Msg)
			}
		}
		if cfg.ProfileModelsIgnored {
			add("modelos", "warn", "opencode: los models del perfil %s no se aplican; ponlos en bflow.yaml o en %s", cfg.Profile, config.GlobalPath())
		}
		if mo, ok := t.(ModelOverrider); ok {
			home, _ := os.UserHomeDir()
			switch sy, state := mo.ModelsState(home, p.Overrides["opencode"]); state {
			case "stale":
				add("modelos", "warn", "opencode: %s no tiene los modelos personales al día; corre bflow render", sy.Path)
			case "manual":
				add("modelos", "warn", "opencode: %s no se puede editar; bflow render te da el bloque a pegar: %s", sy.Path, sy.Paste)
			}
		}
	}
	switch p, err := planRender(c, cfg); {
	case err != nil:
	default:
		var bad, stale []string
		const pluginDir = ".opencode/plugins/" // los archivos del plugin de 1.x y de 2.x
		for _, f := range p.Conflicts {
			if strings.HasPrefix(f, pluginDir) {
				bad = append(bad, f)
			}
		}
		for _, f := range p.Changed {
			if strings.HasPrefix(f, pluginDir) {
				stale = append(stale, f)
			}
		}
		switch {
		case len(bad) > 0:
			add("plugin", "fail", "opencode: %s existe y no lo generó bflow; renómbralo o bórralo y corre bflow render", strings.Join(bad, ", "))
		case len(stale) > 0:
			add("plugin", "warn", "opencode: el plugin %s falta o difiere del de este bflow (guard y tokens); corre bflow render", strings.Join(stale, ", "))
		default:
			add("plugin", "ok", "opencode: plugin de guard y tokens al día (1.x y 2.x)")
		}
	}
	for _, f := range logFs {
		if f.Area != "modelos" {
			add(f.Area, f.Level, "%s", f.Msg)
		}
	}
	add("cobertura", "ok", "opencode no tiene nudge de subagente ni hook de inicio de sesión; lo cubren bflow report, bflow check --verify y el bloque de AGENTS.md")
}

// usesClaude dice si el repo ya trabaja con Claude Code.
func usesClaude(root string) bool { return anyExists(root, ".claude", "CLAUDE.md") }

// usesOpenCode dice si el repo ya trabaja con OpenCode.
func usesOpenCode(root string) bool {
	return anyExists(root, ".opencode", "opencode.json", "opencode.jsonc")
}

func anyExists(root string, paths ...string) bool {
	for _, p := range paths {
		if _, err := os.Stat(filepath.Join(root, p)); err == nil {
			return true
		}
	}
	return false
}

// Presupuesto del contexto de archivos al iniciar. Se paga en cada llamada de
// la sesión principal (en la piloto de ms-sys arrancaba con ~50k tokens). Un
// solo archivo que pasa de fileBudget, como un MEMORY.md de 124 entradas, casi
// siempre arrastra cosas que ya no aplican.
const (
	startupBudget = 10_000
	fileBudget    = 4_000
)

// startupCheck resume lo que se carga al iniciar cada sesión: el total, las
// tres fuentes más pesadas y lo que conviene podar.
func startupCheck(srcs []agents.ContextSource) (status, detail string) {
	if len(srcs) == 0 {
		return "ok", "sin CLAUDE.md, memoria ni skills que cargar al iniciar"
	}
	total := 0
	var notes []string
	for _, s := range srcs {
		total += s.Bytes
		if t := agents.EstimateTokens(s.Bytes); t > fileBudget && s.Note == "" {
			s.Note = fmt.Sprintf("≈%s tokens, más de %s en un solo archivo", metrics.Human(int64(t)), metrics.Human(fileBudget))
		}
		if s.Note != "" {
			notes = append(notes, s.Label+" "+s.Note)
		}
	}
	top := slices.Clone(srcs)
	slices.SortStableFunc(top, func(a, b agents.ContextSource) int { return b.Bytes - a.Bytes })
	var parts []string
	for _, s := range top[:min(3, len(top))] {
		parts = append(parts, fmt.Sprintf("%s ≈%s", s.Label, metrics.Human(int64(agents.EstimateTokens(s.Bytes)))))
	}
	tok := agents.EstimateTokens(total)
	detail = fmt.Sprintf("≈%s tokens de archivos en cada llamada de la sesión principal (%s)", metrics.Human(int64(tok)), strings.Join(parts, ", "))
	if tok > startupBudget {
		notes = append(notes, "el total pasa de "+metrics.Human(startupBudget))
	}
	if len(notes) == 0 {
		return "ok", detail
	}
	return "warn", detail + ". " + strings.Join(notes, "; ") + ". Poda lo que ya no aplica o muévelo a archivos que se lean al necesitarlos"
}

// checkRepo confirma que el token ve el repositorio, en los hosts que saben
// comprobarlo: un token válido sin acceso al repo falla recién al abrir el PR.
func checkRepo(ctx context.Context, h vcs.Host) error {
	if r, ok := h.(interface{ CheckRepo(context.Context) error }); ok {
		return r.CheckRepo(ctx)
	}
	return nil
}
