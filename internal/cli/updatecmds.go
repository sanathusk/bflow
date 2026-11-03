package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"time"

	"innobytes.tech/bflow/internal/output"
	"innobytes.tech/bflow/internal/release"
)

func init() {
	Register(&Command{Name: "update", Summary: "updates bflow to latest published version: update [--check] [--force]",
		Setup: func(fs *flag.FlagSet) {
			fs.Bool("check", false, "only check if a newer version is available")
			fs.Bool("force", false, "replace even if this binary does not come from a published release")
		},
		Run: runUpdate})
}

// executable es la ruta del binario que update reemplaza (las pruebas la cambian).
var executable = os.Executable

func runUpdate(c *Ctx) output.Envelope {
	ctx := context.Background()
	cl := release.NewClient()
	loc := envLoc(c.Env)
	// El canal sale de la versión instalada: con una prerelease se busca entre
	// todas; con una estable (o sin versión publicada) solo entre las estables.
	_, suffix, perr := release.Parts(c.Version)
	pre := perr == nil && release.Published(c.Version) && suffix != ""
	rel, err := cl.Latest(ctx, pre)
	if err != nil {
		return output.Fail("update", fmt.Errorf("%s", loc.T("update.failed_latest", err)))
	}
	data := map[string]any{"current": c.Version, "latest": rel.Tag}
	env := func(code, text string) output.Envelope {
		e := output.OK(code, data, nil)
		e.Text = text
		return e
	}
	cmp, err := release.Compare(c.Version, rel.Tag)
	published := err == nil && release.Published(c.Version)
	force := str(c.Flags, "force") == "true"
	if published && !pre && rel.Prerelease {
		// Un estable nunca recibe una prerelease (llegó por la lista).
		return env("up_to_date", loc.T("update.up_to_date", c.Version))
	}
	switch {
	case !published && !force:
		return env("update_unknown", loc.T("update.unknown_version", c.Version, rel.Tag, rel.Tag))
	case published && cmp >= 0 && !force:
		return env("up_to_date", loc.T("update.up_to_date", c.Version))
	case str(c.Flags, "check") == "true":
		return env("update_available", loc.T("update.available", rel.Tag, c.Version))
	}
	bin, err := cl.Binary(ctx, rel)
	if err != nil {
		return output.Fail("update", err)
	}
	exe, err := executable()
	if err != nil {
		return output.Fail("update", err)
	}
	home, herr := os.UserHomeDir()
	had := map[string]bool{}
	if herr == nil {
		for _, t := range c.tools() {
			_, st := t.SkillState(home)
			had[t.Name()] = st != "missing"
		}
	}
	if err := release.Replace(exe, bin); err != nil {
		return output.Fail("update", errors.New(loc.T("update.replace_failed", err.Error())))
	}
	data["path"] = exe
	text := fmt.Sprintf("bflow %s → %s (%s)", c.Version, rel.Tag, exe)
	integrations := map[string]string{}
	for _, t := range c.tools() {
		name := t.Name()
		what := loc.T("update.tool_skill")
		if name != "claude" {
			what = loc.T("update.tool_command")
		}
		if !had[name] {
			integrations[name] = "skipped"
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := exec.CommandContext(cctx, exe, "install", name, "--skill-only", "--json").Run()
		cancel()
		if err != nil {
			integrations[name] = "failed"
			text += "\n" + loc.T("update.refresh_failed", what, name, name)
		} else {
			integrations[name] = "updated"
			text += "\n" + loc.T("update.integration_updated", what, name)
		}
	}
	text += "\n" + loc.T("update.restart_panels")
	data["integrations"] = integrations
	data["skill"] = "skipped"
	if v, ok := integrations["claude"]; ok {
		data["skill"] = v
	}
	return env("updated", text)
}
