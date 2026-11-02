// Package cli despacha los subcomandos de bflow. No conoce adaptadores: los
// recibe ya construidos en Env desde cmd/bflow, que es la raíz de composición.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"innobytes.tech/bflow/internal/agents"
	"innobytes.tech/bflow/internal/engine"
	"innobytes.tech/bflow/internal/eval"
	"innobytes.tech/bflow/internal/guard"
	"innobytes.tech/bflow/internal/i18n"
	"innobytes.tech/bflow/internal/metrics"
	"innobytes.tech/bflow/internal/output"
	"innobytes.tech/bflow/internal/secrets"
	"innobytes.tech/bflow/internal/tracker"
)

// Env reúne lo que los comandos necesitan del exterior.
type Env struct {
	Stdout  io.Writer
	Stderr  io.Writer
	Stdin   io.Reader
	Version string
	Dir     string // directorio de trabajo
	Loc     *i18n.Localizer

	// Build construye el engine del repo que contiene dir con los adaptadores
	// que indique su configuración. Lo provee cmd/bflow.
	Build func(dir string) (*engine.Engine, error)

	// Connect valida una credencial contra un servicio (lo provee cmd/bflow).
	Connect func(ctx context.Context, service string, o ConnectOpts) (string, error)
	// Secrets es el llavero donde connect guarda las credenciales.
	Secrets secrets.Store
	// Projects lista los proyectos de un tracker (para init, antes de tener config).
	Projects func(ctx context.Context, service, url, workspace string) ([]tracker.Project, error)
	// Agent es el adaptador de la herramienta de agente (Claude Code): traduce
	// sus hooks y lee sus transcripts. Lo provee cmd/bflow.
	Agent AgentAdapter
	// Tools son todas las herramientas de agente registradas (claude, opencode).
	// nil equivale a [Agent] si Agent no es nil. Lo provee cmd/bflow.
	Tools []ToolAdapter
	// EvalGit da el git del repo para el eval del reviewer (lo provee cmd/bflow).
	EvalGit func(dir string) eval.Git
}

// Ctx es lo que recibe cada comando.
type Ctx struct {
	*Env
	JSON   bool
	DryRun bool
	Flags  *flag.FlagSet
	Args   []string
}

// Command es un subcomando registrado.
type Command struct {
	Name    string
	Summary string
	// Setup declara banderas propias antes de parsear.
	Setup func(fs *flag.FlagSet)
	Run   func(c *Ctx) output.Envelope
}

var registry = map[string]*Command{}

// Register agrega un comando. Nombres compuestos ("env check") se registran con espacio.
func Register(c *Command) { registry[c.Name] = c }

func envLoc(env *Env) *i18n.Localizer {
	if env != nil && env.Loc != nil {
		return env.Loc
	}
	return i18n.DefaultLocalizer()
}

func cmdSummary(cmd *Command, loc *i18n.Localizer) string {
	if loc == nil {
		loc = i18n.DefaultLocalizer()
	}
	key := "cli.cmd." + strings.ReplaceAll(cmd.Name, " ", "_")
	if s := loc.T(key); s != key {
		return s
	}
	return cmd.Summary
}

func init() {
	Register(&Command{Name: "version", Summary: "versión de bflow", Run: func(c *Ctx) output.Envelope {
		e := output.OK("version", map[string]any{"version": c.Version}, nil)
		e.Text = "bflow " + c.Version
		if m := bannerFor(c); m != bannerOff {
			e.Text = strings.TrimRight(renderBanner(m, c.Version), "\n")
		}
		return e
	}})
	Register(&Command{Name: "help", Summary: "lista los comandos", Run: func(c *Ctx) output.Envelope {
		names := make([]string, 0, len(registry))
		for n := range registry {
			names = append(names, n)
		}
		sort.Strings(names)
		var b strings.Builder
		loc := envLoc(c.Env)
		b.WriteString(loc.T("cli.help_header") + "\n\n")
		for _, n := range names {
			fmt.Fprintf(&b, "  %-22s %s\n", n, cmdSummary(registry[n], loc))
		}
		e := output.OK("help", map[string]any{"commands": names}, nil)
		e.Text = strings.TrimRight(b.String(), "\n")
		return e
	}})
}

// Run ejecuta args (sin el nombre del binario) y devuelve el código de salida.
func Run(args []string, env *Env) int {
	asJSON := false
	var rest []string
	for _, a := range args {
		if a == "--json" || a == "-json" {
			asJSON = true
			continue
		}
		rest = append(rest, a)
	}
	cmd, params := resolve(rest)
	if cmd == nil {
		name := strings.Join(rest, " ")
		if name == "" {
			cmd, params = homeCommand, nil
		} else {
			l := envLoc(env)
			return emit(env, asJSON, output.FailLoc(l, "unknown_command", fmt.Errorf("%s", l.T("cli.unknown_command", name))))
		}
	}
	fs := flag.NewFlagSet(cmd.Name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dry := fs.Bool("dry-run", false, "no aplica cambios")
	if cmd.Setup != nil {
		cmd.Setup(fs)
	}
	if err := fs.Parse(interleave(fs, params)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return emit(env, asJSON, output.OK("help", map[string]any{"command": cmd.Name}, nil))
		}
		return emit(env, asJSON, output.Fail("bad_flags", err))
	}
	c := &Ctx{Env: env, JSON: asJSON, DryRun: *dry, Flags: fs, Args: fs.Args()}
	return emit(env, asJSON, cmd.Run(c))
}

// resolve busca el comando más largo que coincida ("env check" antes que "env").
func resolve(args []string) (*Command, []string) {
	for n := min(len(args), 3); n > 0; n-- {
		if c, ok := registry[strings.Join(args[:n], " ")]; ok {
			return c, args[n:]
		}
	}
	return nil, nil
}

// interleave permite banderas después de argumentos posicionales
// (`bflow approve LOCAL-1 --gate spec`), que flag de stdlib no admite.
func interleave(fs *flag.FlagSet, args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			if strings.Contains(name, "=") {
				continue
			}
			if f := fs.Lookup(name); f != nil {
				if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
					continue
				}
				if i+1 < len(args) {
					flags = append(flags, args[i+1])
					i++
				}
			}
			continue
		}
		pos = append(pos, a)
	}
	return append(flags, pos...)
}

func emit(env *Env, asJSON bool, e output.Envelope) int {
	if err := output.Write(env.Stdout, e, asJSON); err != nil {
		fmt.Fprintln(env.Stderr, err)
		return output.ExitError
	}
	return e.ExitCode()
}

// ToolAdapter es lo que render, install, update, init y doctor necesitan de
// cualquier herramienta de agente.
type ToolAdapter interface {
	Name() string
	// RenderAgents da formato a los agentes: ruta relativa → contenido.
	RenderAgents(specs []agents.Spec) (map[string][]byte, error)
	// GeneratedAgents lista los agentes que ya generó bflow render.
	GeneratedAgents(root string) []string
	// InstallSkill escribe la skill (Claude) o el comando /bflow (OpenCode)
	// embebido en home y devuelve su ruta.
	InstallSkill(home string) (path string, err error)
	// SkillState compara lo instalado con lo embebido: "ok", "missing" o "stale".
	SkillState(home string) (path, state string)
	// Version compara la versión instalada con la mínima ("" = sin mínimo).
	Version() (have, min string, ok bool, err error)
	// ResolvesModels dice que el modelo de un agente debe ser proveedor/modelo
	// y se traduce con models.<Name()>; false: el alias va directo.
	ResolvesModels() bool
}

// tools devuelve las herramientas registradas.
func (c *Ctx) tools() []ToolAdapter {
	if c.Tools == nil && c.Agent != nil {
		return []ToolAdapter{c.Agent}
	}
	return c.Tools
}

// tool devuelve la herramienta registrada con ese nombre, o nil.
func (c *Ctx) tool(name string) ToolAdapter {
	for _, t := range c.tools() {
		if t.Name() == name {
			return t
		}
	}
	return nil
}

// LogFinding es un hallazgo del log de una herramienta (ver agents.LogFinding).
type LogFinding = agents.LogFinding

// LogDoctor la implementan los adaptadores que saben leer el log de su herramienta.
type LogDoctor interface {
	DoctorLog(root string, now time.Time) []LogFinding
}

// ModelOverrider lo implementan los adaptadores cuyo mapeo personal de modelos
// vive en la config del usuario y no en los archivos versionados (OpenCode).
type ModelOverrider interface {
	// SyncModels lleva want (subagente → proveedor/modelo) a la config del usuario.
	SyncModels(home string, want map[string]string) (agents.ModelSync, error)
	// ModelsState dice si la config del usuario coincide con want: ok|stale|manual.
	ModelsState(home string, want map[string]string) (agents.ModelSync, string)
}

// TokenSource vive en metrics para que los adaptadores no importen cli.
type TokenSource = metrics.TokenSource

// HookAdapter es lo que guard y hook tokens necesitan de una herramienta que
// no es la de c.Agent (OpenCode). Se elige con --tool.
type HookAdapter interface {
	Name() string
	// ParseActions traduce la entrada nativa en acciones; ok=false si no tiene la forma.
	ParseActions(raw []byte) (acts []guard.Action, cwd string, ok bool)
	// TokenSources dice qué archivos leer en este evento y si es la sesión principal.
	TokenSources(raw []byte, cacheDir string) (srcs []TokenSource, main bool)
	ReadUsage(path string, cur *metrics.Cursor) ([]metrics.Sample, error)
	// Prune borra los archivos leídos por completo y viejos, y sus offsets.
	Prune(cacheDir string, cur *metrics.Cursor, olderThan time.Duration)
}

// hooks devuelve la herramienta de --tool si implementa HookAdapter.
func (c *Ctx) hooks(name string) HookAdapter {
	if t := c.tool(name); t != nil {
		if h, ok := t.(HookAdapter); ok {
			return h
		}
	}
	return nil
}

// AgentAdapter es la herramienta de agente con la que bflow se integra por
// completo (hooks, guard, tokens): hoy Claude Code.
type AgentAdapter interface {
	ToolAdapter
	ParsePreToolUse(raw []byte) (a guard.Action, cwd string, ok bool)
	// TokenSources dice qué transcripts leer en un hook de fin de turno o de
	// subagente y de quién son (agent vacío = sesión principal): el del
	// subagente primero y el de la principal después.
	TokenSources(raw []byte) []TokenSource
	// ReadUsage devuelve las respuestas nuevas del transcript con su hora.
	ReadUsage(path string, cur *metrics.Cursor) ([]metrics.Sample, error)
	// Skills lista las skills instaladas (proyecto y usuario).
	Skills(root string) []agents.Skill
	// SubagentStopped lee la entrada del hook de fin de subagente.
	SubagentStopped(raw []byte) (subagent, cwd string, ok bool)
	// KeepWorking es la salida de ese hook que hace seguir al subagente.
	KeepWorking(reason string) string
	// CoauthorOff dice si la herramienta no agrega Co-Authored-By a sus commits.
	CoauthorOff(root string) bool
	// StartupContext lista lo que la herramienta carga al iniciar cada sesión.
	StartupContext(root string) []agents.ContextSource
	// InstallSettings fusiona los ajustes de bflow en la configuración del repo.
	InstallSettings(root string) (agents.SettingsResult, error)
}

// Commands devuelve los nombres de los comandos registrados.
func Commands() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
