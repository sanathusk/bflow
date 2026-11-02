package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"innobytes.tech/bflow/internal/check"
	"innobytes.tech/bflow/internal/engine"
	"innobytes.tech/bflow/internal/envcheck"
	"innobytes.tech/bflow/internal/output"
)

// Runner arma el check del repo con la config y el git del engine.
func Runner(e *engine.Engine) *check.Runner {
	cfg := e.Cfg
	base := cfg.VCS.BaseBranch
	if base != "" && cfg.VCS.Remote != "" {
		base = cfg.VCS.Remote + "/" + base
	}
	r := &check.Runner{Root: cfg.Root, Base: base, Cfg: cfg.Check, Git: e.Git, Exec: check.OSExec,
		LookPath: exec.LookPath, CGO: check.CGOAvailable, TestPatterns: cfg.Guard.TestPatterns}
	if cfg.Stack == "go" || cfg.Check.DBTestPattern != "" {
		r.Packages = func(ctx context.Context) ([]check.GoPackage, error) {
			return check.GoPackages(ctx, cfg.Root, cfg.Check.DBTestPattern, cfg.Check.DBTestImports)
		}
	}
	return r
}

func init() {
	Register(&Command{Name: "check", Summary: "compuerta determinista: check [ID] [--quick pkg] [--verify]",
		Setup: func(fs *flag.FlagSet) {
			fs.String("quick", "", "solo los pasos rápidos sobre un paquete (no registra resultado)")
			fs.Bool("verify", false, "¿el último check pasó y corresponde al código actual?")
		},
		Run: runCheck})

	Register(&Command{Name: "env check", Summary: "verifica API, puertos y variables del entorno local: env check [--quiet]",
		Setup: func(fs *flag.FlagSet) { fs.Bool("quiet", false, "una sola línea si todo está bien") },
		Run: func(c *Ctx) output.Envelope {
			e, err := engineFor(c)
			if err != nil {
				return output.Fail("config", err)
			}
			res := envcheck.Run(context.Background(), e.Cfg.Env, os.Getenv)
			env := output.OK("env", map[string]any{"checks": res, "failed": envcheck.Failed(res)}, nil)
			env.Text = envcheck.Text(res, str(c.Flags, "quiet") == "true")
			if envcheck.Failed(res) > 0 {
				env.OK = false
				env = env.WithExit(output.ExitError)
			}
			return env
		}})
}

func runCheck(c *Ctx) output.Envelope {
	ctx := context.Background()
	e, err := engineFor(c)
	if err != nil {
		return output.Fail("config", err)
	}
	if e.Git == nil {
		return output.Fail("no_git", errors.New("check necesita un repo git (liga el resultado a un commit)"))
	}
	id := ""
	args := c.Args
	if len(args) > 0 && looksLikeID.MatchString(args[0]) {
		id, args = strings.ToUpper(args[0]), args[1:]
	} else if a, err := e.Active(ctx); err == nil {
		id = a
	}
	r := Runner(e)

	if str(c.Flags, "verify") == "true" {
		ok, detail, err := check.Verifier{Store: e.Store, Git: e.Git, CodePaths: e.Cfg.Check.CodePaths}.Verify(ctx, id)
		if err != nil {
			return fail(err)
		}
		if changed := e.FrozenChanged(id); ok && id != "" && len(changed) > 0 {
			ok, detail = false, "pruebas congeladas modificadas: "+strings.Join(changed, ", ")
		}
		if found := e.NarrativeComments(ctx, id); ok && id != "" && len(found) > 0 {
			places := make([]string, len(found))
			for i, f := range found {
				places[i], _, _ = strings.Cut(f, ": ")
			}
			ok, detail = false, "comentarios narrativos: "+strings.Join(places, ", ")
		}
		env := output.OK("verified", map[string]any{"id": id, "ok": ok, "detail": detail}, nil)
		env.Text = envLoc(c.Env).T("check.verify_ok", detail)
		if !ok {
			env.OK, env.Code, env.Text = false, "not_verified", envLoc(c.Env).T("check.verify_fail", detail)
			env = env.WithExit(output.ExitError)
		}
		return env
	}

	if e.Cfg.Check.EnvFirst {
		// Solo lo que usan las pruebas (puertos y variables). La salud del API
		// avisa al iniciar sesión, pero el check no necesita el servidor corriendo.
		need := e.Cfg.Env
		need.APIURL, need.HealthPaths = "", nil
		if res := envcheck.Run(ctx, need, os.Getenv); envcheck.Failed(res) > 0 {
			env := output.Fail("env_not_ready", errors.New("entorno no listo; no se corrió el check"))
			env.Data["checks"] = res
			env.Text = envcheck.Text(res, true) + "\nCHECK ABORTADO: entorno no listo"
			return env
		}
	}

	var res check.Result
	quick := str(c.Flags, "quick")
	if quick != "" {
		res, err = r.Quick(ctx, quick)
	} else {
		if len(e.Cfg.Check.Steps) == 0 {
			return output.Fail("no_steps", errors.New("no hay pasos en check.steps (bflow init los propone)"))
		}
		res, err = r.Run(ctx)
	}
	if err != nil {
		return fail(err)
	}
	if quick == "" {
		if err := check.CompareTimes(e.Store, &res); err != nil {
			return fail(err)
		}
		if err := check.Save(e.Store, id, res); err != nil {
			return fail(err)
		}
	}
	var failed []map[string]any
	var b strings.Builder
	b.WriteString(res.SummaryLoc(c.Env.Loc))
	for _, s := range res.Steps {
		if s.Status != "fail" {
			continue
		}
		// Al agente le basta el inicio del extracto; el completo queda en check.md.
		lines := s.Fails
		if len(lines) == 0 {
			all := strings.Split(s.Tail, "\n")
			lines = all[max(0, len(all)-10):]
		}
		short := strings.Join(lines[:min(len(lines), 10)], "\n")
		f := map[string]any{"step": s.Name, "tail": short}
		fmt.Fprintf(&b, "\n❌ %s\n", s.Name)
		if s.Preexisting {
			f["preexisting"] = true
			b.WriteString(s.PreexistingHint() + "\n")
		}
		b.WriteString(short)
		failed = append(failed, f)
	}
	for _, w := range res.Warnings {
		b.WriteString("\n⚠ " + w)
	}
	if res.SHA == "DIRTY" && quick == "" {
		b.WriteString("\n⚠ " + envLoc(c.Env).T("check.dirty_warning"))
	}
	if quick == "" && id != "" {
		b.WriteString("\ndetalle: bflow show " + id + " check")
	}
	data := map[string]any{"result": res.Result, "sha": res.SHA}
	if id != "" {
		data["id"] = id
	}
	if len(failed) > 0 {
		data["failed"] = failed
	}
	if len(res.Degraded) > 0 {
		data["degraded"] = res.Degraded
	}
	if len(res.Warnings) > 0 {
		data["warnings"] = res.Warnings
	}
	env := output.OK("check", data, nil)
	env.Text = b.String()
	if res.Result != "PASS" {
		env.OK = false
		env = env.WithExit(output.ExitError)
	}
	return env
}
