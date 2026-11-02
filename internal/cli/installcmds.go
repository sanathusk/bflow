package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"innobytes.tech/bflow/internal/config"
	"innobytes.tech/bflow/internal/output"
)

func init() {
	Register(&Command{Name: "install", Summary: "instala la skill y los hooks de bflow para tu herramienta: install claude|opencode [--skill-only]",
		Setup: func(fs *flag.FlagSet) {
			fs.Bool("skill-only", false, "solo la skill del usuario; no toca settings.json ni corre render")
		},
		Run: runInstall})
}

func runInstall(c *Ctx) output.Envelope {
	name := ""
	if len(c.Args) > 0 {
		name = c.Args[0]
	}
	if !slices.Contains(config.Known.Agents, name) {
		return output.Fail("usage", fmt.Errorf("uso: bflow install <herramienta>; disponibles: %s", strings.Join(config.Known.Agents, ", ")))
	}
	tool := c.tool(name)
	if tool == nil || (name == "claude" && c.Agent == nil) {
		return output.Fail("install", fmt.Errorf("no hay adaptador de %s", name))
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = ""
		if name == "claude" {
			return output.Fail("install", fmt.Errorf("no se pudo resolver tu carpeta de usuario: %v", err))
		}
	}
	skill, err := tool.InstallSkill(home)
	if err != nil {
		return output.Fail("install", err)
	}
	warns := []string{}
	data := map[string]any{"skill": skill, "settings": "", "changed": false, "render": false, "warnings": warns}
	var text strings.Builder
	label := "skill"
	if name != "claude" {
		label = "comando"
	}
	text.WriteString(label + ": " + skill)

	finish := func() output.Envelope {
		data["warnings"] = warns
		for _, w := range warns {
			text.WriteString("\naviso: " + w)
		}
		env := output.OK("installed", data, nil)
		env.Text = text.String()
		return env
	}
	if str(c.Flags, "skill-only") == "true" {
		return finish()
	}
	root := config.FindRoot(c.Dir)
	if _, err := os.Stat(filepath.Join(root, config.RepoFile)); err != nil {
		if name == "claude" {
			warns = append(warns, "no hay bflow.yaml aquí: los hooks se instalan por repo; corre bflow install claude dentro de él")
		} else {
			warns = append(warns, "no hay bflow.yaml aquí: los agentes se generan por repo; corre bflow install "+name+" dentro de él")
		}
		return finish()
	}
	cfg, err := config.Load(c.Dir)
	if err != nil {
		return output.Fail("config", err)
	}
	if name == "claude" {
		res, err := c.Agent.InstallSettings(cfg.Root)
		if err != nil {
			return output.Fail("install", err)
		}
		data["settings"], data["changed"] = res.Path, res.Changed
		warns = append(warns, res.Warnings...)
		if res.Changed {
			text.WriteString("\nhooks: " + res.Path + " (actualizado)")
		} else {
			text.WriteString("\nhooks: " + res.Path + " (sin cambios)")
		}
	}
	if !cfg.Agent.Has(name) {
		warns = append(warns, fmt.Sprintf("agent: no incluye %s en bflow.yaml, así que no se generaron sus agentes; agrégalo (agent: [claude, opencode]) y corre bflow render", name))
		return finish()
	}
	p, err := applyRender(c, cfg)
	switch {
	case err != nil:
		warns = append(warns, "render falló: "+err.Error()+"; corre bflow render")
	case len(p.Conflicts) > 0:
		warns = append(warns, fmt.Sprintf("%s ya existe y no lo generó bflow: renómbralo o bórralo y corre bflow render", strings.Join(p.Conflicts, ", ")))
	default:
		data["render"] = true
		text.WriteString("\nagentes: " + renderText(p, c.Env.Loc))
		pm := syncPersonalModels(c, cfg, p)
		data["personal_models"] = pm
		text.WriteString(personalText(cfg, pm))
	}
	return finish()
}
