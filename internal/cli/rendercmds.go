package cli

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"innobytes.tech/bflow/internal/agents"
	"innobytes.tech/bflow/internal/config"
	"innobytes.tech/bflow/internal/eval"
	"innobytes.tech/bflow/internal/i18n"
	"innobytes.tech/bflow/internal/output"
)

func init() {
	Register(&Command{Name: "render", Summary: "genera los agentes de bflow para la herramienta del repo: render [--check]",
		Setup: func(fs *flag.FlagSet) {
			fs.Bool("check", false, "no escribe: falla si los agentes generados no coinciden con la configuración (CI)")
		},
		Run: runRender})
}

// renderPlan compara lo que render generaría con lo que hay en el repo.
type renderPlan struct {
	Files     map[string][]byte // todo lo que debe existir
	Changed   []string          // nuevo o distinto
	Stale     []string          // generado antes, ya no pertenece al flujo
	Conflicts []string          // existe con ese nombre pero no lo generó bflow
	// Unresolved son los alias de modelo sin equivalente, por herramienta.
	Unresolved map[string][]agents.Unresolved
	// Overrides son los modelos personales que cubren alias sin valor en el
	// repo: herramienta → subagente → proveedor/modelo.
	Overrides map[string]map[string]string
}

// agentsMD es el archivo que leen otras herramientas (Codex, OpenCode). Si el
// repo lo tiene, render mantiene en él un bloque que les dice cómo retomar
// una tarea; si no lo tiene, no lo crea.
const agentsMD = "AGENTS.md"

const (
	agentsMDStart = "<!-- bflow:inicio (generado por bflow render) -->"
	agentsMDEnd   = "<!-- bflow:fin -->"
	agentsMDBody  = "Este repo usa bflow para llevar cada tarea de la idea al PR. Si hay una tarea en curso o te piden avanzar una, corre `bflow status --json` y sigue `next`."
)

// upsertBlock pone el bloque de bflow en doc: lo reemplaza si ya está o lo
// agrega al final.
func upsertBlock(doc string, loc *i18n.Localizer) string {
	body := agentsMDBody
	if loc != nil {
		body = loc.T("render.agents_md_body")
	}
	block := agentsMDStart + "\n" + body + "\n" + agentsMDEnd
	if i := strings.Index(doc, agentsMDStart); i >= 0 {
		if j := strings.Index(doc[i:], agentsMDEnd); j >= 0 {
			return doc[:i] + block + doc[i+j+len(agentsMDEnd):]
		}
	}
	return strings.TrimRight(doc, "\n") + "\n\n" + block + "\n"
}

func planRender(c *Ctx, cfg *config.Config) (*renderPlan, error) {
	if len(c.tools()) == 0 {
		return nil, errors.New("no hay adaptador de agente")
	}
	fc := cfg.Flow.Core()
	fc.Loc = c.Env.Loc
	specs, err := agents.Build(fc, cfg.Agents, cfg.Root)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(filepath.Join(cfg.Root, filepath.FromSlash(eval.CasesDir))); err == nil && st.IsDir() {
		specs = append(specs, agents.EvalReviewerSpec(cfg.Agents["reviewer"], c.Env.Loc))
	}
	files := map[string][]byte{}
	p := &renderPlan{Files: files, Unresolved: map[string][]agents.Unresolved{}}
	for _, name := range cfg.Agent.Rendered() {
		t := c.tool(name)
		if t == nil {
			return nil, fmt.Errorf("no hay adaptador de %s", name)
		}
		toolSpecs := specs
		if t.ResolvesModels() {
			var un []agents.Unresolved
			toolSpecs, un = agents.ResolveModels(specs, cfg.Models[t.Name()])
			var left []agents.Unresolved
			for _, u := range un {
				if v := cfg.PersonalModels[t.Name()][u.Alias]; v != "" {
					if p.Overrides == nil {
						p.Overrides = map[string]map[string]string{}
					}
					if p.Overrides[t.Name()] == nil {
						p.Overrides[t.Name()] = map[string]string{}
					}
					for _, a := range u.Agents {
						p.Overrides[t.Name()][a] = v
					}
					continue
				}
				left = append(left, u)
			}
			if len(left) > 0 {
				p.Unresolved[t.Name()] = left
			}
		}
		out, err := t.RenderAgents(toolSpecs)
		if err != nil {
			return nil, err
		}
		for rel, content := range out {
			files[rel] = content
		}
	}
	// Lo generado por cualquier herramienta registrada, también la que ya no está en agent:.
	var generated []string
	for _, t := range c.tools() {
		generated = append(generated, t.GeneratedAgents(cfg.Root)...)
	}
	for rel, want := range files {
		have, err := os.ReadFile(filepath.Join(cfg.Root, filepath.FromSlash(rel)))
		switch {
		case err == nil && !slices.Contains(generated, rel):
			p.Conflicts = append(p.Conflicts, rel)
		case err != nil || !bytes.Equal(bytes.ReplaceAll(have, []byte("\r\n"), []byte("\n")), want):
			p.Changed = append(p.Changed, rel)
		}
	}
	for _, rel := range generated {
		if _, ok := files[rel]; !ok {
			p.Stale = append(p.Stale, rel)
		}
	}
	if have, err := os.ReadFile(filepath.Join(cfg.Root, agentsMD)); err == nil {
		doc := strings.ReplaceAll(string(have), "\r\n", "\n")
		if want := upsertBlock(doc, c.Env.Loc); want != doc {
			p.Files[agentsMD] = []byte(want)
			p.Changed = append(p.Changed, agentsMD)
		}
	}
	slices.Sort(p.Changed)
	slices.Sort(p.Stale)
	slices.Sort(p.Conflicts)
	return p, nil
}

func runRender(c *Ctx) output.Envelope {
	cfg, err := config.Load(c.Dir)
	if err != nil {
		return output.Fail("config", err)
	}
	p, err := planRender(c, cfg)
	if err != nil {
		return output.Fail("render", err)
	}
	data := map[string]any{"changed": p.Changed, "stale": p.Stale, "total": len(p.Files), "unresolved": p.unresolvedData(c)}
	if len(p.Conflicts) > 0 {
		env := output.Fail("render_conflict", fmt.Errorf("%s ya existe y no lo generó bflow: renómbralo o bórralo y vuelve a correr bflow render", strings.Join(p.Conflicts, ", ")))
		env.Data["conflicts"] = p.Conflicts
		return env
	}
	if str(c.Flags, "check") == "true" {
		if len(p.Changed)+len(p.Stale) == 0 {
			env := output.OK("render_ok", data, nil)
			env.Text = envLoc(c.Env).T("render.up_to_date", len(p.Files))
			return env
		}
		env := output.FailLoc(envLoc(c.Env), "render_outdated", fmt.Errorf("%s", envLoc(c.Env).T("render.outdated_agents", strings.Join(append(p.Changed, p.Stale...), ", "))))
		env.Data = data
		return env
	}
	if err := writeRender(cfg, p); err != nil {
		return fail(err)
	}
	pm := syncPersonalModels(c, cfg, p)
	data["personal_models"] = pm
	env := output.OK("rendered", data, nil)
	env.Text = renderText(p, c.Env.Loc) + personalText(cfg, pm)
	return env
}

// syncPersonalModels lleva el mapeo personal al opencode.json del usuario (R6).
// Un fallo de E/S no rompe render: queda en "error". nil si ninguna herramienta lo usa.
func syncPersonalModels(c *Ctx, cfg *config.Config, p *renderPlan) map[string]any {
	home, _ := os.UserHomeDir()
	for _, name := range cfg.Agent.Rendered() {
		mo, ok := c.tool(name).(ModelOverrider)
		if !ok {
			continue
		}
		want := p.Overrides[name]
		res, err := mo.SyncModels(home, want)
		out := map[string]any{"path": res.Path, "changed": res.Changed, "agents": slices.Sorted(maps.Keys(want)), "paste": res.Paste, "error": nil}
		if err != nil {
			out["error"] = err.Error()
		}
		return out
	}
	return nil
}

// personalText son las líneas de texto de la sincronización y del aviso del perfil.
func personalText(cfg *config.Config, pm map[string]any) string {
	var b strings.Builder
	if pm != nil {
		path, _ := pm["path"].(string)
		agentsN, _ := pm["agents"].([]string)
		switch {
		case pm["error"] != nil:
			fmt.Fprintf(&b, "\naviso: no se pudieron escribir los modelos personales en %v: %v", path, pm["error"])
		case pm["paste"] != "":
			fmt.Fprintf(&b, "\naviso: %s no se puede editar; pega en %s: %s", path, path, pm["paste"])
		case pm["changed"] == true:
			fmt.Fprintf(&b, "\nmodelos personales: %s (actualizado)", path)
		case len(agentsN) > 0:
			fmt.Fprintf(&b, "\nmodelos personales: %s (sin cambios)", path)
		}
	}
	b.WriteString(profileModelsText(cfg))
	return b.String()
}

func profileModelsText(cfg *config.Config) string {
	if !cfg.ProfileModelsIgnored {
		return ""
	}
	return fmt.Sprintf("\naviso: los models del perfil %s no se aplican; ponlos en bflow.yaml o en %s", cfg.Profile, config.GlobalPath())
}

// applyRender planifica y escribe los agentes; con conflictos no escribe nada
// y devuelve el plan para que quien llama decida qué decir.
func applyRender(c *Ctx, cfg *config.Config) (*renderPlan, error) {
	p, err := planRender(c, cfg)
	if err != nil || len(p.Conflicts) > 0 {
		return p, err
	}
	return p, writeRender(cfg, p)
}

func writeRender(cfg *config.Config, p *renderPlan) error {
	for _, rel := range p.Changed {
		abs := filepath.Join(cfg.Root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(abs, p.Files[rel], 0o644); err != nil {
			return err
		}
	}
	for _, rel := range p.Stale {
		if err := os.Remove(filepath.Join(cfg.Root, filepath.FromSlash(rel))); err != nil {
			return err
		}
		if dir := filepath.ToSlash(filepath.Dir(filepath.FromSlash(rel))); dir == ".opencode/plugins/bflow2" {
			_ = os.Remove(filepath.Join(cfg.Root, filepath.FromSlash(dir))) // falla si no está vacía: se deja
		}
	}
	return nil
}

// unresolvedData aplana los alias sin resolver: [{tool, alias, agents}], por
// herramienta en el orden de registro y luego por alias.
func (p *renderPlan) unresolvedData(c *Ctx) []map[string]any {
	out := []map[string]any{}
	for _, t := range c.tools() {
		for _, u := range p.Unresolved[t.Name()] {
			out = append(out, map[string]any{"tool": t.Name(), "alias": u.Alias, "agents": u.Agents})
		}
	}
	return out
}

func renderText(p *renderPlan, loc *i18n.Localizer) string {
	if loc == nil {
		loc = i18n.DefaultLocalizer()
	}
	var b strings.Builder
	b.WriteString(loc.T("render.files_summary", len(p.Files), len(p.Changed), len(p.Files)-len(p.Changed)))
	for _, rel := range p.Changed {
		b.WriteString("\n  + " + rel)
	}
	for _, rel := range p.Stale {
		b.WriteString("\n  - " + rel + loc.T("render.stale_suffix"))
	}
	if len(p.Changed)+len(p.Stale) > 0 {
		b.WriteString("\n" + loc.T("render.commit_hint"))
	}
	for _, tool := range slices.Sorted(maps.Keys(p.Unresolved)) {
		for _, u := range p.Unresolved[tool] {
			fmt.Fprintf(&b, "\naviso: %s: el alias %q no tiene equivalente (%s) y esos agentes salen sin model; agrega models.%s.%s: proveedor/modelo en bflow.yaml (equipo) o en %s (solo tú)",
				tool, u.Alias, strings.Join(u.Agents, ", "), tool, u.Alias, config.GlobalPath())
		}
	}
	return b.String()
}
