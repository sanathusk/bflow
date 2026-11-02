package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"innobytes.tech/bflow/internal/config"
	"innobytes.tech/bflow/internal/engine"
	"innobytes.tech/bflow/internal/flow"
	"innobytes.tech/bflow/internal/i18n"
	"innobytes.tech/bflow/internal/output"
)

// multi es una bandera repetible (--option a --option b).
type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

var looksLikeID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*-\d+$`)

// engineFor construye el engine del repo actual.
func engineFor(c *Ctx) (*engine.Engine, error) {
	if c.Build == nil {
		return nil, errors.New("bflow se compiló sin adaptadores")
	}
	e, err := c.Build(c.Dir)
	if err == nil && e != nil && e.Loc != nil && c.Env != nil {
		c.Env.Loc = e.Loc
	}
	return e, err
}

// resolveID toma el ID de los argumentos o, si no hay, la tarea activa.
func resolveID(ctx context.Context, e *engine.Engine, args []string) (string, []string, error) {
	if len(args) > 0 && looksLikeID.MatchString(args[0]) {
		return strings.ToUpper(args[0]), args[1:], nil
	}
	id, err := e.Active(ctx)
	return id, args, err
}

// fail convierte un error en envelope: los rechazos del flujo son exit 2.
func fail(err error) output.Envelope {
	var rj *flow.Rejection
	if errors.As(err, &rj) {
		return output.Rejected(rj.Code, rj.Reason)
	}
	return output.Fail("error", err)
}

func outcomeEnvelope(o engine.Outcome, loc *i18n.Localizer) output.Envelope {
	data := map[string]any{"id": o.ID, "phase": o.Phase}
	if o.Round > 0 {
		data["round"] = o.Round
	}
	if o.Gate != "" {
		data["gate"] = o.Gate
	}
	if o.Branch != "" {
		data["branch"] = o.Branch
	}
	if o.PR != nil {
		data["pr"] = o.PR
	}
	if len(o.Warnings) > 0 {
		data["warnings"] = o.Warnings
	}
	code := "unchanged"
	if o.To != "" {
		code = "advanced"
	}
	next := o.Next
	env := output.OK(code, data, &next)
	env.Text = renderOutcome(o, loc)
	return env
}

// flowCommand envuelve el patrón común: construir engine, resolver ID, ejecutar.
func flowCommand(run func(ctx context.Context, e *engine.Engine, id string, c *Ctx, rest []string) (engine.Outcome, error)) func(*Ctx) output.Envelope {
	return func(c *Ctx) output.Envelope {
		ctx := context.Background()
		e, err := engineFor(c)
		if err != nil {
			return output.Fail("config", err)
		}
		id, rest, err := resolveID(ctx, e, c.Args)
		if err != nil {
			return fail(err)
		}
		o, err := run(ctx, e, id, c, rest)
		if err != nil {
			return fail(err)
		}
		return outcomeEnvelope(o, c.Env.Loc)
	}
}

// afterStart hace lo que sigue a un start correcto: recordar el repo y abrir el panel.
func afterStart(e *engine.Engine, o *engine.Outcome) {
	_ = config.RememberRepo(e.Cfg.Root) // para la red de la página del panel
	if e.Cfg.UI.Watch || e.Cfg.UI.Web {
		// Sin escritorio (CI, SSH) no se abre y no se avisa: es lo esperado.
		if _, werr := openWatch(e.Cfg.Root, e.Store.Dir(), e.Cfg.UI.Web); werr != nil && !errors.Is(werr, errNoDesktop) {
			o.Warnings = append(o.Warnings, "no se pudo abrir el panel (bflow watch): "+werr.Error())
		}
	}
}

func str(fs *flag.FlagSet, name string) string { return fs.Lookup(name).Value.String() }

func init() {
	Register(&Command{Name: "start", Summary: "empieza una tarea en un carril: start <ID> --lane full|light|hotfix [--slug s] [--fixes ID]",
		Setup: func(fs *flag.FlagSet) {
			fs.String("lane", "", "carril")
			fs.String("slug", "", "slug para rama y carpeta del spec")
			fs.String("fixes", "", "hotfix: ID de la feature que corrige")
		},
		Run: func(c *Ctx) output.Envelope {
			if len(c.Args) == 0 {
				return output.Fail("usage", errors.New("uso: bflow start <ID> --lane full|light|hotfix"))
			}
			lane := str(c.Flags, "lane")
			if lane == "" {
				return output.Fail("usage", errors.New("falta --lane (full, light o hotfix); bflow status <ID> muestra las opciones"))
			}
			return flowCommand(func(ctx context.Context, e *engine.Engine, id string, c *Ctx, _ []string) (engine.Outcome, error) {
				o, err := e.Start(ctx, id, flow.Lane(lane), str(c.Flags, "slug"), str(c.Flags, "fixes"))
				if err == nil {
					afterStart(e, &o)
				}
				return o, err
			})(c)
		}})

	Register(&Command{Name: "new", Summary: "crea una tarea y la arranca: new --lane full|light|hotfix --title \"t\" [--file idea.md] [--slug s]",
		Setup: func(fs *flag.FlagSet) {
			fs.String("lane", "", "carril")
			fs.String("title", "", "título de la tarea")
			fs.String("file", "", "archivo Markdown con la idea (descripción)")
			fs.String("slug", "", "slug para rama y carpeta del spec")
		},
		Run: func(c *Ctx) output.Envelope {
			lane, title := strings.TrimSpace(str(c.Flags, "lane")), strings.TrimSpace(str(c.Flags, "title"))
			if lane == "" || title == "" {
				return output.Fail("usage", errors.New(`uso: bflow new --lane full|light|hotfix --title "título" [--file idea.md]`))
			}
			e, err := engineFor(c)
			if err != nil {
				return output.Fail("config", err)
			}
			desc := ""
			if f := str(c.Flags, "file"); f != "" {
				b, err := os.ReadFile(abs(c.Dir, f))
				if err != nil {
					return fail(err)
				}
				desc = string(b)
			}
			task, o, err := e.New(context.Background(), flow.Lane(lane), title, desc, str(c.Flags, "slug"))
			if err != nil {
				env := fail(err)
				if task.ID != "" {
					env.Data["id"] = task.ID
				}
				return env
			}
			afterStart(e, &o)
			env := outcomeEnvelope(o, c.Env.Loc)
			env.Data["title"] = task.Title
			return env
		}})

	Register(&Command{Name: "task list", Summary: "lista las tareas abiertas del tracker: task list",
		Run: func(c *Ctx) output.Envelope {
			e, err := engineFor(c)
			if err != nil {
				return output.Fail("config", err)
			}
			tasks, err := e.OpenTasks(context.Background())
			if err != nil {
				return fail(err)
			}
			rows := make([]map[string]any, 0, len(tasks))
			var lines []string
			for _, t := range tasks {
				rows = append(rows, map[string]any{"id": t.ID, "title": t.Title, "phase": t.Phase})
				lines = append(lines, fmt.Sprintf("%s · %s · %s", t.ID, t.Phase, t.Title))
			}
			env := output.OK("task_list", map[string]any{"tasks": rows}, nil)
			env.Text = strings.Join(lines, "\n")
			if len(lines) == 0 {
				env.Text = "bflow: sin tareas abiertas"
			}
			return env
		}})

	Register(&Command{Name: "approve", Summary: "aprueba el gate pendiente: approve [ID] [--gate g] [--choice n] [--note t] [--file f | --stdin]",
		Setup: func(fs *flag.FlagSet) {
			fs.String("gate", "", "gate que se aprueba (por seguridad; vacío = el pendiente)")
			fs.Int("choice", 0, "opción elegida en un gate decision")
			fs.String("note", "", "decisión libre o comentario")
			fs.Bool("manual", false, "decision: la hace la sesión principal (bloquea hasta unblock --note)")
			fs.String("file", "", "archivo adjunto (discovery completo)")
			fs.Bool("stdin", false, "el discovery completo llega por stdin (solo gate discovery)")
		},
		Run: flowCommand(func(ctx context.Context, e *engine.Engine, id string, c *Ctx, _ []string) (engine.Outcome, error) {
			o := engine.ApproveOpts{Gate: str(c.Flags, "gate"), Note: str(c.Flags, "note"), Manual: str(c.Flags, "manual") == "true"}
			fmt.Sscan(str(c.Flags, "choice"), &o.Choice)
			if str(c.Flags, "stdin") == "true" {
				if str(c.Flags, "file") != "" {
					return engine.Outcome{}, &flow.Rejection{Code: "usage", Reason: "usa --stdin o --file, no los dos"}
				}
				if g := o.Gate; g != "" && g != "discovery" {
					return engine.Outcome{}, &flow.Rejection{Code: "bad_attachment", Reason: "--stdin solo aplica al gate discovery"}
				}
				const limit = 64 << 10
				b, err := io.ReadAll(io.LimitReader(c.Stdin, limit+1))
				if err != nil {
					return engine.Outcome{}, err
				}
				if len(b) > limit {
					return engine.Outcome{}, &flow.Rejection{Code: "discovery_too_long", Reason: "el discovery pasa de 64 KiB; recórtalo y ciérralo otra vez"}
				}
				o.Attachment = string(b)
			} else if f := str(c.Flags, "file"); f != "" {
				b, err := os.ReadFile(abs(c.Dir, f))
				if err != nil {
					return engine.Outcome{}, err
				}
				o.Attachment = string(b)
			}
			return e.Approve(ctx, id, o)
		})})

	Register(&Command{Name: "reject", Summary: "rechaza el gate pendiente: reject [ID] --note \"motivo\" [--gate g] [--to fase]",
		Setup: func(fs *flag.FlagSet) {
			fs.String("gate", "", "gate que se rechaza")
			fs.String("to", "", "fase destino alternativa")
			fs.String("note", "", "motivo (obligatorio)")
		},
		Run: flowCommand(func(ctx context.Context, e *engine.Engine, id string, c *Ctx, _ []string) (engine.Outcome, error) {
			return e.Reject(ctx, id, engine.RejectOpts{Gate: str(c.Flags, "gate"), To: str(c.Flags, "to"), Note: str(c.Flags, "note")})
		})})

	Register(&Command{Name: "report", Summary: "un agente reporta su veredicto: report [ID] --agent a --verdict V [--note] [--file] [--option o]... [--stdin: reporte del scout]",
		Setup: func(fs *flag.FlagSet) {
			fs.String("agent", "", "nombre del agente")
			fs.String("verdict", "", "veredicto")
			fs.String("note", "", "detalle (problema en NEEDS_DECISION, motivo en BLOCKED)")
			fs.String("file", "", "archivo del reporte")
			fs.Bool("stdin", false, "el reporte llega por stdin (solo el scout)")
			fs.Var(&multi{}, "option", "opción para NEEDS_DECISION (repetible)")
			fs.String("no-repro", "", "hotfix: cierra sin prueba de reproducción; abre un gate humano con el motivo")
		},
		Run: flowCommand(func(ctx context.Context, e *engine.Engine, id string, c *Ctx, _ []string) (engine.Outcome, error) {
			opts := *(c.Flags.Lookup("option").Value.(*multi))
			var noRepro *string
			c.Flags.Visit(func(f *flag.Flag) {
				if f.Name == "no-repro" {
					v := f.Value.String()
					noRepro = &v
				}
			})
			var content *string
			if c.Flags.Lookup("stdin").Value.String() == "true" {
				const limit = 64 << 10
				b, err := io.ReadAll(io.LimitReader(c.Stdin, limit+1))
				if err != nil {
					return engine.Outcome{}, err
				}
				if len(b) > limit {
					return engine.Outcome{}, &flow.Rejection{Code: "scout_too_long", Reason: "el reporte pasa de 64 KiB; recórtalo (40 líneas como máximo) y reporta otra vez"}
				}
				s := string(b)
				content = &s
			}
			return e.Report(ctx, id, engine.ReportOpts{Agent: str(c.Flags, "agent"), Verdict: flow.Verdict(str(c.Flags, "verdict")),
				Note: str(c.Flags, "note"), File: str(c.Flags, "file"), Options: opts, Content: content, NoRepro: noRepro})
		})})

	Register(&Command{Name: "block", Summary: "bloquea la tarea: block [ID] --reason \"motivo\"",
		Setup: func(fs *flag.FlagSet) { fs.String("reason", "", "motivo") },
		Run: flowCommand(func(ctx context.Context, e *engine.Engine, id string, c *Ctx, _ []string) (engine.Outcome, error) {
			return e.Block(ctx, id, str(c.Flags, "reason"))
		})})

	Register(&Command{Name: "pause", Summary: "pide parar antes de quality, al terminar implementing, para probar tú: pause [ID]",
		Run: flowCommand(func(ctx context.Context, e *engine.Engine, id string, c *Ctx, _ []string) (engine.Outcome, error) {
			return e.Pause(ctx, id)
		})})

	Register(&Command{Name: "drop", Summary: "retira una tarea sin terminarla: drop <ID> --note \"motivo\"",
		Setup: func(fs *flag.FlagSet) { fs.String("note", "", "motivo") },
		Run: func(c *Ctx) output.Envelope {
			if len(c.Args) == 0 {
				return output.Fail("usage", errors.New("drop necesita el ID de la tarea: bflow drop <ID> --note \"motivo\""))
			}
			e, err := engineFor(c)
			if err != nil {
				return output.Fail("config", err)
			}
			o, err := e.Drop(context.Background(), c.Args[0], str(c.Flags, "note"))
			if err != nil {
				return fail(err)
			}
			return outcomeEnvelope(o, c.Env.Loc)
		}})

	Register(&Command{Name: "unblock", Summary: "desbloquea la tarea y vuelve a su fase: unblock [ID] [--note t] [--no-changelog] (la nota llega al agente; --no-changelog exime del changelog)",
		Setup: func(fs *flag.FlagSet) {
			fs.String("note", "", "resultado para el agente que retoma")
			fs.Bool("no-changelog", false, "exime a documenting del changelog para consumidores (exige --note)")
		},
		Run: flowCommand(func(ctx context.Context, e *engine.Engine, id string, c *Ctx, _ []string) (engine.Outcome, error) {
			return e.Unblock(ctx, id, str(c.Flags, "note"), str(c.Flags, "no-changelog") == "true")
		})})

	Register(&Command{Name: "freeze", Summary: "vuelve a congelar las pruebas del contrato tal como están, o con --allow deja cambiar una una vez (solo una persona): freeze [ID] [--allow <archivo>]",
		Setup: func(fs *flag.FlagSet) {
			fs.String("allow", "", "prueba congelada que el implementer puede cambiar una vez")
		},
		Run: func(c *Ctx) output.Envelope {
			ctx := context.Background()
			e, err := engineFor(c)
			if err != nil {
				return output.Fail("config", err)
			}
			id, _, err := resolveID(ctx, e, c.Args)
			if err != nil {
				return fail(err)
			}
			if f := str(c.Flags, "allow"); f != "" {
				if err := e.AllowFrozen(id, f); err != nil {
					return fail(err)
				}
				env := output.OK("freeze_allowed", map[string]any{"id": id, "file": f}, nil)
				env.Text = fmt.Sprintf("%s: %s se puede cambiar una vez; se vuelve a congelar cuando el implementer reporte DONE", id, f)
				return env
			}
			n, err := e.Refreeze(id)
			if err != nil {
				return fail(err)
			}
			env := output.OK("refrozen", map[string]any{"id": id, "files": n}, nil)
			env.Text = fmt.Sprintf("%s: %d prueba(s) congelada(s) de nuevo con su contenido actual", id, n)
			return env
		}})

	Register(&Command{Name: "status", Summary: "estado y siguiente paso: status [ID] [--brief]",
		Setup: func(fs *flag.FlagSet) { fs.Bool("brief", false, "resumen de pocas líneas (hooks)") },
		Run:   runStatus})

	Register(&Command{Name: "show", Summary: "muestra un artefacto: show [ID] task|brief|spec|contract|review-map|questions|decisions|discovery|check|scout [--section s]",
		Setup: func(fs *flag.FlagSet) { fs.String("section", "", "sección del spec") },
		Run: func(c *Ctx) output.Envelope {
			ctx := context.Background()
			e, err := engineFor(c)
			if err != nil {
				return output.Fail("config", err)
			}
			id, rest, err := resolveID(ctx, e, c.Args)
			if err != nil {
				return fail(err)
			}
			if len(rest) == 0 {
				return output.Fail("usage", errors.New("uso: bflow show [ID] task|brief|spec|contract|review-map|questions|decisions|discovery|check|scout"))
			}
			text, err := e.Show(ctx, id, rest[0], str(c.Flags, "section"))
			if err != nil {
				return output.Fail("not_found", err)
			}
			env := output.OK("shown", map[string]any{"id": id, "what": rest[0], "content": text}, nil)
			env.Text = strings.TrimRight(text, "\n")
			if strings.TrimSpace(text) == "" {
				env.Text = fmt.Sprintf("(%s de %s está vacío todavía)", rest[0], id)
			}
			return env
		}})

	Register(&Command{Name: "task add", Summary: "crea una tarea (trackers que lo permiten, como local): task add \"título\" [--description t]",
		Setup: func(fs *flag.FlagSet) { fs.String("description", "", "descripción en Markdown") },
		Run: func(c *Ctx) output.Envelope {
			title := strings.TrimSpace(strings.Join(c.Args, " "))
			if title == "" {
				return output.Fail("usage", errors.New(`uso: bflow task add "título"`))
			}
			e, err := engineFor(c)
			if err != nil {
				return output.Fail("config", err)
			}
			ctx := context.Background()
			task, err := e.CreateTask(ctx, title, str(c.Flags, "description"))
			if err != nil {
				return fail(err)
			}
			view, err := e.Status(ctx, task.ID)
			if err != nil {
				return fail(err)
			}
			next := view.Next
			env := output.OK("created", map[string]any{"id": task.ID, "title": task.Title}, &next)
			env.Text = c.Env.Loc.T("render.task_created", task.ID, task.Title) + "\n" + strings.TrimRight(renderNext(next, c.Env.Loc), "\n")
			return env
		}})

	Register(&Command{Name: "sync", Summary: "reintenta los cambios pendientes con el tracker: sync [ID]",
		Run: func(c *Ctx) output.Envelope {
			ctx := context.Background()
			e, err := engineFor(c)
			if err != nil {
				return output.Fail("config", err)
			}
			ids := []string{}
			if len(c.Args) > 0 {
				ids = append(ids, strings.ToUpper(c.Args[0]))
			} else {
				views, err := e.Views(ctx)
				if err != nil {
					return fail(err)
				}
				for _, v := range views {
					if v.Pending > 0 {
						ids = append(ids, v.ID)
					}
				}
			}
			applied := map[string]int{}
			var remaining int
			for _, id := range ids {
				n, err := e.Sync(ctx, id)
				if err != nil {
					return fail(err)
				}
				applied[id] = n
				if v, err := e.Status(ctx, id); err == nil {
					remaining += v.Pending
				}
			}
			env := output.OK("synced", map[string]any{"applied": applied, "remaining": remaining}, nil)
			env.Text = fmt.Sprintf("sincronizados: %v · pendientes: %d", applied, remaining)
			if remaining > 0 {
				env = env.WithExit(output.ExitError)
				env.OK = false
			}
			return env
		}})
}

func runStatus(c *Ctx) output.Envelope {
	ctx := context.Background()
	e, err := engineFor(c)
	if err != nil {
		return output.Fail("config", err)
	}
	brief := c.Flags.Lookup("brief").Value.String() == "true"
	if len(c.Args) > 0 {
		v, err := e.Status(ctx, strings.ToUpper(c.Args[0]))
		if err != nil {
			return fail(err)
		}
		return viewEnvelope(v, brief, c.Env.Loc)
	}
	views, err := e.Views(ctx)
	if err != nil {
		return fail(err)
	}
	if id, err := e.Active(ctx); err == nil {
		for _, v := range views {
			if v.ID == id {
				return viewEnvelope(v, brief, c.Env.Loc)
			}
		}
	}
	var lines []string
	for _, v := range views {
		l := fmt.Sprintf("%s · %s", v.ID, v.Phase)
		if v.Gate != "" {
			l += " · gate " + v.Gate
		}
		if !brief {
			l += " · " + v.Title
		}
		lines = append(lines, l)
	}
	text := "bflow: sin tareas en curso"
	if len(lines) > 0 {
		text = "bflow: " + fmt.Sprint(len(lines)) + " tareas en curso (indica el ID)\n  " + strings.Join(lines, "\n  ")
	}
	if len(views) == 0 {
		next := flow.IntakeNext(e.Cfg.Flow.Core())
		env := output.OK("status_list", map[string]any{"tasks": views}, &next)
		env.Text = text
		if !brief {
			env.Text += "\n" + strings.TrimRight(renderNext(next, c.Env.Loc), "\n")
		}
		return env
	}
	env := output.OK("status_list", map[string]any{"tasks": views}, nil)
	env.Text = text
	return env
}

func viewEnvelope(v engine.View, brief bool, loc *i18n.Localizer) output.Envelope {
	next := v.Next
	env := output.OK("status", map[string]any{"task": v}, &next)
	if brief {
		l := fmt.Sprintf("bflow: %s · %s", v.ID, v.Phase)
		if v.Gate != "" {
			l += " · gate " + v.Gate
		}
		if v.Pending > 0 {
			l += fmt.Sprintf(" · %d sin sincronizar", v.Pending)
		}
		l += " · siguiente: " + next.Action
		if next.Action == output.ActionSpawn && len(next.Agents) > 0 {
			var as []string
			for _, a := range next.Agents {
				as = append(as, a.Agent)
			}
			l += " " + strings.Join(as, ", ")
		}
		env.Text = l + " (bflow status " + v.ID + " --json)"
		return env
	}
	env.Text = renderView(v, loc)
	return env
}

func abs(dir, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dir, p)
}

func init() {
	Register(&Command{Name: "import", Summary: "importa el harness anterior una vez: import --from harness [--dir ruta] [--state-only] [--dry-run]",
		Setup: func(fs *flag.FlagSet) {
			fs.String("from", "harness", "origen (solo harness)")
			fs.String("dir", "", "raíz del repo con harness/ (por defecto, el repo actual)")
			fs.Bool("state-only", false, "solo .state-times.json: las tareas ya existen en el tracker")
		},
		Run: func(c *Ctx) output.Envelope {
			if str(c.Flags, "from") != "harness" {
				return output.Fail("usage", errors.New("solo se importa --from harness"))
			}
			e, err := engineFor(c)
			if err != nil {
				return output.Fail("config", err)
			}
			dir := e.Cfg.Root
			if d := str(c.Flags, "dir"); d != "" {
				dir = abs(c.Dir, d)
			}
			ctx := context.Background()
			var plan engine.ImportPlan
			if str(c.Flags, "state-only") == "true" {
				plan, err = e.ImportStateTimes(ctx, dir, c.DryRun)
			} else {
				plan, err = e.Import(ctx, dir, c.DryRun)
			}
			if err != nil {
				return fail(err)
			}
			code := "imported"
			if c.DryRun {
				code = "import_plan"
			}
			env := output.OK(code, map[string]any{"tasks": plan.Tasks, "skipped": plan.Skipped}, nil)
			var b strings.Builder
			fmt.Fprintf(&b, "%d tarea(s)", len(plan.Tasks))
			if c.DryRun {
				b.WriteString(" se importarían")
			} else {
				b.WriteString(" importadas")
			}
			for _, t := range plan.Tasks {
				fmt.Fprintf(&b, "\n  %s → %s · %s · %s", t.From, t.ID, t.Phase, t.Title)
			}
			if len(plan.Skipped) > 0 {
				fmt.Fprintf(&b, "\n%d saltadas (cerradas o sin estado)", len(plan.Skipped))
			}
			env.Text = b.String()
			return env
		}})
}

func init() {
	Register(&Command{Name: "pr", Summary: "publica la rama y abre o actualiza el PR de una tarea en in_review: pr [ID]",
		Run: flowCommand(func(ctx context.Context, e *engine.Engine, id string, _ *Ctx, _ []string) (engine.Outcome, error) {
			return e.PR(ctx, id)
		})})

	Register(&Command{Name: "panel", Summary: "compromisos: cierra lo mergeado o terminado en el tracker, sincroniza y avisa SLA: panel [--sla]",
		Setup: func(fs *flag.FlagSet) {
			fs.Bool("sla", false, "comentar en el tracker los gates vencidos y las tareas vencidas")
		},
		Run: func(c *Ctx) output.Envelope {
			e, err := engineFor(c)
			if err != nil {
				return output.Fail("config", err)
			}
			rep, err := e.Panel(context.Background(), str(c.Flags, "sla") == "true")
			if err != nil {
				return fail(err)
			}
			env := output.OK("panel", map[string]any{"items": rep.Items, "more": rep.More, "closed": rep.Closed,
				"closed_outside": rep.ClosedOutside, "reminded": rep.Reminded, "warnings": rep.Warnings}, nil)
			env.Text = renderPanel(rep)
			return env
		}})
}

func renderPanel(rep engine.PanelReport) string {
	var b strings.Builder
	for _, id := range rep.Closed {
		fmt.Fprintf(&b, "✅ %s cerrada: PR mergeado\n", id)
	}
	for _, c := range rep.ClosedOutside {
		why := "(tracker)"
		if c.Reason == "pr" {
			why = fmt.Sprintf("(PR #%d mergeado)", c.PR)
		}
		fmt.Fprintf(&b, "%s cerrada: terminada fuera de esta copia %s\n", c.ID, why)
	}
	if len(rep.Items) == 0 {
		b.WriteString("📋 sin compromisos pendientes\n")
	} else {
		fmt.Fprintf(&b, "📋 compromisos (%d)\n", len(rep.Items)+rep.More)
	}
	for _, it := range rep.Items {
		mark := "🟡"
		if it.SLA || it.Overdue {
			mark = "🔴"
		}
		title := it.Title
		if len([]rune(title)) > 40 {
			title = string([]rune(title)[:39]) + "…"
		}
		l := fmt.Sprintf("%s %s %s · %s", mark, it.ID, title, it.Phase)
		if it.Gate != "" {
			l += fmt.Sprintf(" · espera %s %dh", it.Gate, it.Hours)
		}
		if it.Overdue {
			l += " · VENCIDA " + it.Due
		} else if it.Due != "" {
			l += " · due " + it.Due
		}
		b.WriteString(l + "\n")
	}
	if rep.More > 0 {
		fmt.Fprintf(&b, "… y %d más\n", rep.More)
	}
	for _, r := range rep.Reminded {
		b.WriteString("  ↳ comentado: " + r + "\n")
	}
	for _, w := range rep.Warnings {
		b.WriteString("  ⚠ " + w + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
