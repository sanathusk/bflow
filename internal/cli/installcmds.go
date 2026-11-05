package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"innobytes.tech/bflow/internal/config"
	"innobytes.tech/bflow/internal/i18n"
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
	loc := envLoc(c.Env)
	name := ""
	if len(c.Args) > 0 {
		name = c.Args[0]
	}
	if !slices.Contains(config.Known.Agents, name) {
		return output.FailLoc(loc, "usage", fmt.Errorf("%s", loc.T("install.usage", strings.Join(config.Known.Agents, ", "))))
	}
	tool := c.tool(name)
	if tool == nil || (name == "claude" && c.Agent == nil) {
		return output.FailLoc(loc, "install", fmt.Errorf("%s", loc.T("install.no_adapter", name)))
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = ""
		if name == "claude" {
			return output.FailLoc(loc, "install", fmt.Errorf("%s", loc.T("install.user_dir_error", err)))
		}
	}
	skill, err := tool.InstallSkill(home)
	if err != nil {
		return output.FailLoc(loc, "install", err)
	}
	warns := []string{}
	data := map[string]any{"skill": skill, "settings": "", "changed": false, "render": false, "warnings": warns}
	var text strings.Builder
	label := loc.T("install.label_skill")
	if name != "claude" {
		label = loc.T("install.label_command")
	}
	text.WriteString(label)
	text.WriteString(": ")
	text.WriteString(skill)

	finish := func() output.Envelope {
		data["warnings"] = warns
		for _, w := range warns {
			text.WriteString(loc.T("install.warning_prefix"))
			text.WriteString(w)
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
			warns = append(warns, loc.T("install.no_repo_claude"))
		} else {
			warns = append(warns, fmt.Sprintf(loc.T("install.no_repo_other"), name))
		}
		return finish()
	}
	cfg, err := config.Load(c.Dir)
	if err != nil {
		return output.FailLoc(loc, "config", err)
	}
	if cfg.UI.Lang != "" {
		loc = i18n.DefaultBundle().New(i18n.DetectLang(cfg.UI.Lang))
		if c.Env != nil {
			c.Env.Loc = loc
		}
	}
	if name == "claude" {
		res, err := c.Agent.InstallSettings(cfg.Root)
		if err != nil {
			return output.FailLoc(loc, "install", err)
		}
		data["settings"], data["changed"] = res.Path, res.Changed
		warns = append(warns, res.Warnings...)
		if res.Changed {
			text.WriteString(fmt.Sprintf(loc.T("install.hooks_updated"), res.Path))
		} else {
			text.WriteString(fmt.Sprintf(loc.T("install.hooks_unchanged"), res.Path))
		}
	}
	if !cfg.Agent.Has(name) {
		warns = append(warns, fmt.Sprintf(loc.T("install.agent_not_in_config"), name))
		return finish()
	}
	p, err := applyRender(c, cfg)
	switch {
	case err != nil:
		warns = append(warns, fmt.Sprintf(loc.T("install.render_failed"), err.Error()))
	case len(p.Conflicts) > 0:
		warns = append(warns, fmt.Sprintf(loc.T("install.render_conflict"), strings.Join(p.Conflicts, ", ")))
	default:
		data["render"] = true
		text.WriteString(loc.T("install.agents_label"))
		text.WriteString(renderText(p, loc))
		pm := syncPersonalModels(c, cfg, p)
		data["personal_models"] = pm
		text.WriteString(personalText(cfg, pm))
	}
	return finish()
}
