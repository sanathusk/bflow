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

func getPanelChoices(loc *i18n.Localizer) []panelChoice {
	if loc == nil {
		loc = i18n.DefaultLocalizer()
	}
	return []panelChoice{
		{loc.T("init.panel_choice_both"), loc.T("init.panel_choice_both_summary"), true, true},
		{loc.T("init.panel_choice_terminal"), loc.T("init.panel_choice_terminal_summary"), true, false},
		{loc.T("init.panel_choice_none"), loc.T("init.panel_choice_none_summary"), false, false},
	}
}

func panelLabels(choices []panelChoice) []string {
	var out []string
	for _, p := range choices {
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
		loc := envLoc(c.Env)
		return output.RejectedLoc(loc, "exists", fmt.Sprintf(loc.T("init.already_exists"), path))
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
			choices := getPanelChoices(loc)
			pc := choices[p.choose(loc.T("init.prompt_panel_startup"), panelLabels(choices), 0)]
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
			panelText = fmt.Sprintf(loc.T("init.panel_summary_created"), panel.summary, config.GlobalPath())
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
	loc := envLoc(c.Env)
	e, err := engineFor(c)
	if err != nil {
		add("config", "fail", "%v", err)
		return doctorEnvelope(items)
	}
	if e.Loc != nil {
		loc = e.Loc
	}
	cfg := e.Cfg
	src := cfg.Sources.Repo
	if src == "" {
		src = loc.T("doctor.config_default")
	}
	if cfg.Sources.Profile != "" {
		src = fmt.Sprintf(loc.T("doctor.config_profile"), src, cfg.Sources.Profile)
	}
	add("config", "ok", "%s", src)

	if cfg.Sources.Repo != "" && cfg.UI.Lang == "" {
		add("idioma", "warn", loc.T("doctor.ui_lang_missing"), cfg.Sources.Repo, cfg.Lang())
	}

	if e.Git == nil {
		add("git", "warn", "%s", loc.T("doctor.git_not_repo"))
	} else if r, err := e.Git.RemoteURL(ctx); err != nil {
		add("git", "warn", loc.T("doctor.git_no_remote"), cfg.VCS.Remote)
	} else {
		add("git", "ok", loc.T("doctor.git_remote_base"), r, orDash(cfg.VCS.BaseBranch))
	}

	if log, err := e.Store.Log(""); err != nil {
		add("veredicto-modelos", "warn", loc.T("doctor.models_log_read_error"), err)
	} else {
		v := metrics.CompareModels(log, time.Now())
		status := "ok"
		if v.Verdict == "sube" {
			status = "warn"
		}
		add("veredicto-modelos", status, "%s", modelVerdictLine(v, loc))
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
		add("herramientas", "warn", "%s", loc.T("doctor.tools_no_steps"))
	case len(missing) > 0:
		add("herramientas", "fail", loc.T("doctor.tools_missing_required"), strings.Join(missing, ", "))
	case len(optional) > 0:
		add("herramientas", "warn", loc.T("doctor.tools_missing_optional"), strings.Join(optional, ", "))
	default:
		add("herramientas", "ok", loc.T("doctor.tools_ok"), len(cfg.Check.Steps))
	}

	doctorTracker(ctx, e.Tracker, cfg, add, loc)

	if e.Host != nil {
		if u, ok := e.Host.(interface {
			User(context.Context) (string, error)
		}); ok {
			if login, err := u.User(ctx); errors.Is(err, vcs.ErrNoCredentials) {
				add("host", "warn", loc.T("doctor.host_no_token"), e.Host.Name(), e.Host.Name())
			} else if err != nil {
				add("host", "fail", "%v", err)
			} else if err := checkRepo(ctx, e.Host); err != nil {
				add("host", "fail", "%v", err)
			} else {
				add("host", "ok", loc.T("doctor.host_ok"), e.Host.Name(), login)
			}
		}
	} else if cfg.VCS.Host == "" {
		add("host", "ok", "%s", loc.T("doctor.host_none"))
	}

	var runs []string
	for _, st := range cfg.Check.Steps {
		runs = append(runs, st.Run)
	}
	if n, gaps := setup.CIGaps(cfg.Root, runs); len(gaps) > 0 {
		for _, g := range gaps {
			add("ci", "warn", loc.T("doctor.ci_gap_warn"), g)
		}
	} else if n > 0 {
		add("ci", "ok", loc.T("doctor.ci_covered"), n)
	}

	ec := cfg.Env
	if len(ec.HealthPaths)+len(ec.TCP)+len(ec.RequireEnv) > 0 {
		res := envcheck.Run(ctx, ec, os.Getenv)
		if n := envcheck.Failed(res); n > 0 {
			add("entorno", "warn", loc.T("doctor.env_failed"), n, len(res))
		} else {
			add("entorno", "ok", loc.T("doctor.env_ok"), len(res))
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
			add("agente", "warn", "%s", loc.T("doctor.claude_missing_settings"))
		case len(missing) > 0:
			add("agente", "warn", loc.T("doctor.claude_missing_hooks"), strings.Join(missing, ", "))
		case !strings.Contains(string(b), "bflow guard --reads"):
			add("agente", "warn", "%s", loc.T("doctor.claude_guard_no_reads"))
		default:
			add("agente", "ok", "%s", loc.T("doctor.claude_hooks_ok"))
		}
		if home, err := os.UserHomeDir(); err == nil {
			switch path, state := c.Agent.SkillState(home); state {
			case "ok":
				add("skill", "ok", loc.T("doctor.claude_skill_ok"), path)
			case "missing":
				add("skill", "warn", loc.T("doctor.claude_skill_missing"), path)
			default:
				add("skill", "warn", loc.T("doctor.claude_skill_differs"), path)
			}
		}
		switch have, min, ok, err := c.Agent.Version(); {
		case err != nil:
			add("agente", "warn", loc.T("doctor.claude_version_read_err"), err, min)
		case !ok:
			add("agente", "warn", loc.T("doctor.claude_version_too_low"), have, min)
		default:
			add("agente", "ok", loc.T("doctor.claude_version_ok"), have)
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
			add("skills", "warn", loc.T("doctor.claude_process_skills"), strings.Join(procs, "; "))
		}
		status, detail := startupCheck(c.Agent.StartupContext(cfg.Root), loc)
		add("contexto", status, "%s", detail)
		if cfg.Guard.ForbidCoauthor && !c.Agent.CoauthorOff(cfg.Root) {
			add("agente", "warn", "%s", loc.T("doctor.claude_coauthor_conflict"))
		}
	}

	if len(cfg.Agent) > 0 {
		switch p, err := planRender(c, cfg); {
		case err != nil:
			add("agentes", "fail", "%v", err)
		case len(p.Conflicts) > 0:
			add("agentes", "fail", loc.T("doctor.agents_conflict"), strings.Join(p.Conflicts, ", "))
		case len(p.Changed)+len(p.Stale) > 0:
			add("agentes", "warn", loc.T("doctor.agents_outdated"), strings.Join(append(p.Changed, p.Stale...), ", "))
		default:
			add("agentes", "ok", loc.T("doctor.agents_ok"), len(p.Files))
		}
	}
	if cfg.Agent.Has("opencode") {
		doctorOpenCode(c, cfg, add, loc)
	}

	if cfg.Tracker.Adapter == "local" {
		seen := map[string]bool{}
		for _, e := range strings.Fields(gitOut(cfg.Root, "log", "--since=90.days", "--format=%ae")) {
			seen[strings.ToLower(e)] = true
		}
		if len(seen) >= 2 {
			add("tracker", "warn", loc.T("doctor.local_tracker_authors"), len(seen))
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
			add("arranque", "warn", loc.T("doctor.startup_slow"), best.Milliseconds(), filepath.Dir(exe))
		} else {
			add("arranque", "ok", loc.T("doctor.startup_ok"), best.Milliseconds())
		}
	}
	return doctorEnvelope(items)
}

func doctorTracker(ctx context.Context, t tracker.Tracker, cfg *config.Config, add func(string, string, string, ...any), locs ...*i18n.Localizer) {
	loc := i18n.DefaultLocalizer()
	if len(locs) > 0 && locs[0] != nil {
		loc = locs[0]
	}
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
				add("tracker", "fail", loc.T("doctor.tracker_project_not_found_gh"), cfg.Tracker.Project)
			} else {
				add("tracker", "fail", loc.T("doctor.tracker_project_not_found"), cfg.Tracker.Project, cfg.Tracker.Workspace)
			}
			return
		}
	}
	msg := fmt.Sprintf(loc.T("doctor.tracker_msg"), cfg.Tracker.Adapter, cfg.Tracker.Project)
	if gh {
		msg = loc.T("doctor.tracker_msg_gh_no_project")
		if cfg.Tracker.Project != "" {
			msg = fmt.Sprintf(loc.T("doctor.tracker_msg_gh_project"), cfg.Tracker.Project)
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
			add("tracker", "warn", loc.T("doctor.tracker_no_state"), msg, strings.Join(lack, ", "))
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
func doctorOpenCode(c *Ctx, cfg *config.Config, add func(string, string, string, ...any), locs ...*i18n.Localizer) {
	loc := i18n.DefaultLocalizer()
	if len(locs) > 0 && locs[0] != nil {
		loc = locs[0]
	}
	t := c.tool("opencode")
	if t == nil {
		add("agente", "fail", "%s", loc.T("doctor.opencode_no_adapter"))
		return
	}
	switch have, _, _, err := t.Version(); {
	case err != nil:
		add("agente", "warn", loc.T("doctor.opencode_version_read_err"), err)
	default:
		plug := ".opencode/plugins/bflow2/"
		if n, _ := strconv.Atoi(strings.SplitN(strings.TrimPrefix(have, "v"), ".", 2)[0]); n == 1 {
			plug = ".opencode/plugins/bflow.js"
		}
		add("agente", "ok", loc.T("doctor.opencode_version_ok"), have, plug)
	}
	if home, err := os.UserHomeDir(); err == nil || os.Getenv("XDG_CONFIG_HOME") != "" {
		switch path, state := t.SkillState(home); state {
		case "ok":
			add("comando", "ok", loc.T("doctor.opencode_cmd_ok"), path)
		case "missing":
			add("comando", "warn", "%s", loc.T("doctor.opencode_cmd_missing"))
		default:
			add("comando", "warn", loc.T("doctor.opencode_cmd_differs"), path)
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
			add("modelos", "warn", loc.T("doctor.opencode_unresolved_alias"), strings.Join(parts, "; "), config.GlobalPath())
		} else if !regionBad {
			add("modelos", "ok", "%s", loc.T("doctor.opencode_models_resolved"))
		}
		for _, f := range logFs {
			if f.Area == "modelos" {
				add(f.Area, f.Level, "%s", f.Msg)
			}
		}
		if cfg.ProfileModelsIgnored {
			add("modelos", "warn", loc.T("doctor.opencode_profile_models_ignored"), cfg.Profile, config.GlobalPath())
		}
		if mo, ok := t.(ModelOverrider); ok {
			home, _ := os.UserHomeDir()
			switch sy, state := mo.ModelsState(home, p.Overrides["opencode"]); state {
			case "stale":
				add("modelos", "warn", loc.T("doctor.opencode_models_stale"), sy.Path)
			case "manual":
				add("modelos", "warn", loc.T("doctor.opencode_models_manual"), sy.Path, sy.Paste)
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
			add("plugin", "fail", loc.T("doctor.opencode_plugin_conflict"), strings.Join(bad, ", "))
		case len(stale) > 0:
			add("plugin", "warn", loc.T("doctor.opencode_plugin_stale"), strings.Join(stale, ", "))
		default:
			add("plugin", "ok", "%s", loc.T("doctor.opencode_plugin_ok"))
		}
	}
	for _, f := range logFs {
		if f.Area != "modelos" {
			msg := f.Msg
			if strings.HasPrefix(msg, "opencode 2.x (opencode.log): versión ") && strings.HasSuffix(msg, ", usa .opencode/plugins/bflow2/") {
				ver := strings.TrimPrefix(msg, "opencode 2.x (opencode.log): versión ")
				ver = strings.TrimSuffix(ver, ", usa .opencode/plugins/bflow2/")
				msg = loc.T("doctor.opencode_log_v2", ver, ".opencode/plugins/bflow2/")
			} else if strings.HasPrefix(msg, "opencode 2.x (opencode.log): versión ") && strings.Contains(msg, "; formato de plugin no probado") {
				parts := strings.SplitN(msg, ";", 2)
				ver := strings.TrimPrefix(parts[0], "opencode 2.x (opencode.log): versión ")
				ver = strings.TrimSuffix(ver, ", usa .opencode/plugins/bflow2/")
				msg = loc.T("doctor.opencode_log_v2", ver, ".opencode/plugins/bflow2/") + loc.T("doctor.opencode_log_future_warn")
			} else if strings.HasPrefix(msg, "opencode: sin fallos de carga del plugin en ") {
				dir := strings.TrimPrefix(msg, "opencode: sin fallos de carga del plugin en ")
				msg = loc.T("doctor.opencode_log_plugin_ok", dir)
			} else if strings.HasPrefix(msg, "opencode: no hay log en ") {
				dir := strings.TrimPrefix(msg, "opencode: no hay log en ")
				msg = loc.T("doctor.opencode_log_missing", dir)
			} else if msg == "certificado autofirmado (antivirus o proxy con inspección HTTPS): define NODE_EXTRA_CA_CERTS con su CA" {
				msg = loc.T("doctor.opencode_log_cert")
			}
			add(f.Area, f.Level, "%s", msg)
		}
	}
	add("cobertura", "ok", "%s", loc.T("doctor.opencode_coverage"))
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
func startupCheck(srcs []agents.ContextSource, locs ...*i18n.Localizer) (status, detail string) {
	loc := i18n.DefaultBundle().New("es")
	if len(locs) > 0 && locs[0] != nil {
		loc = locs[0]
	}
	if len(srcs) == 0 {
		return "ok", loc.T("doctor.context_empty")
	}
	total := 0
	var notes []string
	for _, s := range srcs {
		total += s.Bytes
		if t := agents.EstimateTokens(s.Bytes); t > fileBudget && s.Note == "" {
			s.Note = loc.T("doctor.context_file_over_budget", metrics.Human(int64(t)), metrics.Human(fileBudget))
		}
		if s.Note != "" {
			notes = append(notes, s.Label+" "+s.Note)
		}
	}
	top := slices.Clone(srcs)
	slices.SortStableFunc(top, func(a, b agents.ContextSource) int { return b.Bytes - a.Bytes })
	var parts []string
	for _, s := range top[:min(3, len(top))] {
		lbl := s.Label
		if lbl == "descripciones de skills y agentes" {
			lbl = loc.T("doctor.context_label_skills_agents")
		}
		parts = append(parts, fmt.Sprintf("%s ≈%s", lbl, metrics.Human(int64(agents.EstimateTokens(s.Bytes)))))
	}
	tok := agents.EstimateTokens(total)
	detail = loc.T("doctor.context_detail", metrics.Human(int64(tok)), strings.Join(parts, ", "))
	if tok > startupBudget {
		notes = append(notes, loc.T("doctor.context_total_over_budget", metrics.Human(startupBudget)))
	}
	if len(notes) == 0 {
		return "ok", detail
	}
	return "warn", detail + ". " + strings.Join(notes, "; ") + loc.T("doctor.context_warn_advice")
}

// checkRepo confirma que el token ve el repositorio, en los hosts que saben
// comprobarlo: un token válido sin acceso al repo falla recién al abrir el PR.
func checkRepo(ctx context.Context, h vcs.Host) error {
	if r, ok := h.(interface{ CheckRepo(context.Context) error }); ok {
		return r.CheckRepo(ctx)
	}
	return nil
}
