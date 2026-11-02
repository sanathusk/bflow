package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os/exec"
	"strings"

	"innobytes.tech/bflow/internal/config"
	"innobytes.tech/bflow/internal/output"
	"innobytes.tech/bflow/internal/secrets"
	"innobytes.tech/bflow/internal/tracker"
)

// ConnectOpts son los datos para validar una credencial.
type ConnectOpts struct {
	URL       string // plane: URL de la instancia; github: API (vacío = pública)
	Workspace string
	Token     string
}

func init() {
	Register(&Command{Name: "connect", Summary: "guarda una credencial en el llavero tras validarla: connect plane|github [--token t] [--url u] [--workspace w] [--from-gh]",
		Setup: func(fs *flag.FlagSet) {
			fs.String("token", "", "token (si falta, se pide sin mostrarlo o se lee de la entrada)")
			fs.String("url", "", "plane: URL de la instancia; github: URL de la API (Enterprise)")
			fs.String("workspace", "", "plane: slug del workspace")
			fs.Bool("from-gh", false, "github: usar el token de `gh auth token`")
		},
		Run: runConnect})

	Register(&Command{Name: "tracker setup", Summary: "crea en el tracker los estados que las fases necesitan: tracker setup [--dry-run]",
		Run: func(c *Ctx) output.Envelope {
			e, err := engineFor(c)
			if err != nil {
				return output.Fail("config", err)
			}
			p, ok := e.Tracker.(tracker.StateProvisioner)
			if !ok {
				return output.OK("nothing_to_do", map[string]any{"tracker": e.Tracker.Name()}, nil)
			}
			created, err := p.EnsureStates(context.Background(), c.DryRun)
			var manual *tracker.ManualSetupError
			if errors.As(err, &manual) {
				env := output.OK("manual", map[string]any{"where": manual.Where, "missing": manual.Missing, "reason": manual.Reason}, nil)
				env.Code = "manual"
				env.Text = "crea a mano en " + manual.Where + ":\n  " + strings.Join(manual.Missing, "\n  ") + "\n(" + manual.Reason + ")"
				return env
			}
			if err != nil {
				return fail(err)
			}
			env := output.OK("states", map[string]any{"created": created, "dry_run": c.DryRun}, nil)
			switch {
			case len(created) == 0:
				env.Text = "el tracker ya tiene un estado para cada fase"
			case c.DryRun:
				env.Text = "se crearían:\n  " + strings.Join(created, "\n  ")
			default:
				env.Text = "creados:\n  " + strings.Join(created, "\n  ")
			}
			return env
		}})
}

func runConnect(c *Ctx) output.Envelope {
	if len(c.Args) != 1 {
		return output.Fail("usage", errors.New("uso: bflow connect plane|github"))
	}
	service := strings.ToLower(c.Args[0])
	if c.Connect == nil || c.Secrets == nil {
		return output.Fail("unavailable", errors.New("este binario no tiene llavero ni validadores"))
	}
	o := ConnectOpts{URL: str(c.Flags, "url"), Workspace: str(c.Flags, "workspace"), Token: str(c.Flags, "token")}
	cfg, _ := config.Load(c.Dir) // la config es opcional: las banderas mandan

	var key string
	switch service {
	case "plane":
		if cfg != nil && cfg.Tracker.Adapter == "plane" {
			if o.URL == "" {
				o.URL = cfg.Tracker.URL
			}
			if o.Workspace == "" {
				o.Workspace = cfg.Tracker.Workspace
			}
		}
		if o.URL == "" || o.Workspace == "" {
			return output.Fail("usage", errors.New("falta la instancia: bflow connect plane --url https://plane.ejemplo.com --workspace <slug> (o configúralos en tracker)"))
		}
		key = secrets.Key("plane", o.URL)
	case "github":
		if o.URL == "" && cfg != nil {
			o.URL = cfg.VCS.APIURL
		}
		host := "github.com"
		if o.URL != "" {
			if u, err := url.Parse(o.URL); err == nil && u.Host != "" {
				host = u.Host
			}
		}
		key = secrets.Key("github", host)
		if o.Token == "" && str(c.Flags, "from-gh") == "true" {
			out, err := exec.Command("gh", "auth", "token").Output()
			if err != nil {
				return output.Fail("gh", fmt.Errorf("gh auth token falló (¿gh auth login?): %w", err))
			}
			o.Token = strings.TrimSpace(string(out))
		}
	default:
		return output.Fail("usage", fmt.Errorf("servicio %q no soportado (plane, github)", service))
	}

	if o.Token == "" {
		loc := envLoc(c.Env)
		tok, err := readSecret(c.Stdin, c.Stderr, loc.T("connect.token_prompt", service), loc)
		if err != nil {
			return output.Fail("no_token", err)
		}
		o.Token = tok
	}
	if o.Token == "" {
		return output.Fail("no_token", errors.New("token vacío"))
	}
	detail, err := c.Connect(context.Background(), service, o)
	if err != nil {
		return output.Fail("invalid_credentials", fmt.Errorf("no se guardó: %w", err))
	}
	data := map[string]any{"service": service, "key": key, "detail": detail, "stored": !c.DryRun}
	if !c.DryRun {
		if err := c.Secrets.Set(key, o.Token); err != nil {
			return output.Fail("keyring", fmt.Errorf("credencial válida pero no se pudo guardar en el llavero: %w (usa la variable de entorno)", err))
		}
	}
	env := output.OK("connected", data, nil)
	env.Text = fmt.Sprintf("%s conectado · %s\nguardado en el llavero como %s", service, detail, key)
	if c.DryRun {
		env.Text = fmt.Sprintf("%s válido · %s (no se guardó: --dry-run)", service, detail)
	}
	return env
}

func init() {
	Register(&Command{Name: "tracker states", Summary: "estados del tracker y la fase con la que bflow los lee y escribe (solo lectura)",
		Run: func(c *Ctx) output.Envelope {
			e, err := engineFor(c)
			if err != nil {
				return output.Fail("config", err)
			}
			l, ok := e.Tracker.(tracker.StateLister)
			if !ok {
				return output.OK("nothing_to_do", map[string]any{"tracker": e.Tracker.Name()}, nil)
			}
			sts, err := l.StateMap(context.Background())
			if err != nil {
				return fail(err)
			}
			var b strings.Builder
			for _, s := range sts {
				ph := string(s.Phase)
				if ph == "" {
					ph = "—"
				}
				fmt.Fprintf(&b, "%-22s [%s] → lee: %s", s.Name, s.Group, ph)
				if len(s.Writes) > 0 {
					fmt.Fprintf(&b, " · escribe: %s", strings.Join(s.Writes, ", "))
				}
				b.WriteString("\n")
			}
			env := output.OK("states", map[string]any{"states": sts}, nil)
			env.Text = strings.TrimRight(b.String(), "\n")
			return env
		}})
}
