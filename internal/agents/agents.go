// Package agents define los agentes que lanza bflow. Cada uno tiene dos capas:
// el contrato con bflow, que sale del flujo (fases, veredictos, archivos,
// report) y no se configura, y el oficio: uno por defecto más lo que cada repo
// agrega en bflow.yaml. Es neutral: el adaptador de cada herramienta le da
// formato (bflow render).
package agents

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"innobytes.tech/bflow/internal/config"
	"innobytes.tech/bflow/internal/flow"
	"innobytes.tech/bflow/internal/i18n"
)

// Capacidades neutrales; el adaptador las traduce a herramientas concretas.
const (
	Read  = "read"
	Write = "write"
	Bash  = "bash"
)

// Spec es un agente listo para que un adaptador lo escriba.
type Spec struct {
	Name         string // nombre en el flujo (bflow report --agent)
	Subagent     string // nombre de la definición generada
	Description  string
	Tools        []string
	Model        string
	Effort       string
	OmitClaudeMd bool
	Body         string // Markdown: contrato y oficio
}

// GeneratedMark distingue los archivos que escribe bflow render de los que
// escribió una persona. Lo llevan los agentes de todas las herramientas.
const GeneratedMark = "<!-- generado por bflow render: no lo edites; cambia bflow.yaml (agents) o el archivo extra y vuelve a correrlo -->"

// ModelSync es el resultado de llevar los overrides de modelo a la config del usuario.
type ModelSync struct {
	Path    string            // opencode.json (o .jsonc si es el que existe)
	Changed bool              // se escribió el archivo
	Paste   string            // bloque {"agent":{...}} si no se pudo editar; "" si se editó
	Want    map[string]string // subagente → proveedor/modelo
}

// Unresolved es un alias de modelo sin equivalente en la tabla de una
// herramienta, con los agentes (Spec.Subagent, ordenados) que lo usan.
type Unresolved struct {
	Alias  string
	Agents []string
}

// ResolveModels traduce el modelo de cada agente con la tabla alias →
// proveedor/modelo: un valor con "/" se usa tal cual, un alias de la tabla se
// cambia por su valor, vacío se queda vacío y un alias ausente queda vacío y
// se devuelve en Unresolved (ordenado por alias). No muta specs.
func ResolveModels(specs []Spec, table map[string]string) ([]Spec, []Unresolved) {
	out := make([]Spec, len(specs))
	missing := map[string][]string{}
	for i, s := range specs {
		out[i] = s
		if s.Model == "" || strings.Contains(s.Model, "/") {
			continue
		}
		if v := table[s.Model]; v != "" {
			out[i].Model = v
			continue
		}
		out[i].Model = ""
		missing[s.Model] = append(missing[s.Model], s.Subagent)
	}
	var un []Unresolved
	for alias, as := range missing {
		sort.Strings(as)
		un = append(un, Unresolved{Alias: alias, Agents: as})
	}
	sort.Slice(un, func(i, j int) bool { return un[i].Alias < un[j].Alias })
	return out, un
}

type base struct {
	description string
	model       string
	effort      string
	writes      []string // artefactos en .bflow/tasks/<id>/
	writesSpec  bool
	changelog   bool // escribe el único changelog para consumidores (ruta en `changelog`)
}

// catalog son los agentes que bflow trae con un oficio por defecto.
var catalog = map[string]base{
	"spec-author":      {"Escribe la spec de la tarea (brief, requirements, design, tasks) a partir de la descripción y el discovery.", "opus", "medium", nil, true, false},
	"ui-designer":      {"Diseña el UI blueprint de la spec anclado a las pantallas hermanas del repo.", "", "medium", nil, true, false},
	"implementer":      {"Escribe el contrato (pruebas y firmas) y después implementa las tareas de la spec.", "sonnet", "medium", []string{"contract.md", "reports/impl.md"}, false, false},
	"reviewer":         {"Revisa trazabilidad, pruebas, arquitectura y seguridad del diff y escribe el review-map.", "opus", "medium", []string{"reports/review-map.md"}, false, false},
	"security-auditor": {"Audita seguridad, resiliencia y rendimiento del código nuevo de la rama.", "sonnet", "medium", []string{"reports/security.md"}, false, false},
	"ux-auditor":       {"Audita la interfaz nueva contra el UI blueprint y las guías del repo.", "sonnet", "medium", []string{"reports/ux.md"}, false, false},
	"scout":            {"Lee el repo y resume lo que toca la tarea antes del discovery o la spec.", "haiku", "low", nil, false, false},
	"documenter":       {"Documenta el cambio y escribe el walkthrough del PR.", "haiku", "low", []string{"walkthrough.md", "reports/docs.md"}, false, true},
}

//go:embed craft/*/*.md
var craftFS embed.FS

func loadCraft(name, lang string) (string, error) {
	path := fmt.Sprintf("craft/%s/%s.md", lang, name)
	md, err := craftFS.ReadFile(path)
	if err == nil {
		return strings.TrimSpace(string(md)), nil
	}
	if lang != "en" {
		md, err = craftFS.ReadFile(fmt.Sprintf("craft/en/%s.md", name))
		if err == nil {
			return strings.TrimSpace(string(md)), nil
		}
	}
	if lang != "es" {
		md, err = craftFS.ReadFile(fmt.Sprintf("craft/es/%s.md", name))
		if err == nil {
			return strings.TrimSpace(string(md)), nil
		}
	}
	return "", err
}

// Build arma los agentes del flujo con los ajustes del repo. Un agente que no
// está en el catálogo necesita su oficio en agents.<nombre>.extra.
func Build(fc flow.Config, conf map[string]config.AgentConf, root string) ([]Spec, error) {
	loc := fc.Loc
	if loc == nil {
		loc = i18n.DefaultLocalizer()
	}
	lang := loc.Lang()
	var specs []Spec
	var problems []string
	for _, name := range fc.AgentNames() {
		b, known := catalog[name]
		c := conf[name]
		phases := fc.PhasesOf(name)
		desc := loc.T("agent.desc." + name)
		if desc == "agent.desc."+name {
			desc = b.description
		}
		if !known {
			desc = loc.T("agent.desc.custom", joinPhases(phases))
			b = base{description: desc}
			if slices.Contains(phases, flow.Quality) {
				b.writes = []string{"reports/" + name + ".md"}
			}
			if c.Extra == "" {
				problems = append(problems, fmt.Sprintf("%s no es un agente de bflow: define su oficio en agents.%s.extra", name, name))
				continue
			}
		}
		craft := ""
		if known {
			var err error
			craft, err = loadCraft(name, lang)
			if err != nil {
				return nil, err
			}
		}
		extra := ""
		if c.Extra != "" {
			md, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(c.Extra)))
			if err != nil {
				problems = append(problems, fmt.Sprintf("agents.%s.extra: %v", name, err))
				continue
			}
			extra = strings.TrimSpace(string(md))
		}
		s := Spec{
			Name:         name,
			Subagent:     flow.SubagentPrefix + name,
			Description:  desc + loc.T("agent.desc_suffix"),
			Tools:        []string{Read, Write, Bash},
			Model:        pick(c.Model, b.model),
			Effort:       pick(c.Effort, b.effort),
			OmitClaudeMd: len(c.Read) > 0,
			Body:         body(name, phases, b, craft, c.Read, extra, loc),
		}
		if c.OmitClaudeMd != nil {
			s.OmitClaudeMd = *c.OmitClaudeMd
		}
		specs = append(specs, s)
	}
	if fc.Scout != "" {
		sp, err := scoutSpec(fc.Scout, conf[fc.Scout], root, loc)
		if err != nil {
			problems = append(problems, err.Error())
		} else {
			specs = append(specs, sp)
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("agentes: %s", strings.Join(problems, "; "))
	}
	return specs, nil
}

// scoutSpec arma el agente scout: solo lee y reporta por stdin, no escribe.
func scoutSpec(name string, c config.AgentConf, root string, loc *i18n.Localizer) (Spec, error) {
	if loc == nil {
		loc = i18n.DefaultLocalizer()
	}
	b := catalog[name]
	desc := loc.T("agent.desc." + name)
	if desc == "agent.desc."+name {
		desc = b.description
	}
	craft, err := loadCraft(name, loc.Lang())
	if err != nil {
		return Spec{}, err
	}
	extra := ""
	if c.Extra != "" {
		x, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(c.Extra)))
		if err != nil {
			return Spec{}, fmt.Errorf("agents.%s.extra: %v", name, err)
		}
		extra = strings.TrimSpace(string(x))
	}
	s := Spec{
		Name:         name,
		Subagent:     flow.SubagentPrefix + name,
		Description:  desc + loc.T("agent.desc_suffix"),
		Tools:        []string{Read, Bash},
		Model:        pick(c.Model, b.model),
		Effort:       pick(c.Effort, b.effort),
		OmitClaudeMd: len(c.Read) > 0,
		Body:         bodyWith(scoutContract(name, loc), craft, c.Read, extra, loc),
	}
	if c.OmitClaudeMd != nil {
		s.OmitClaudeMd = *c.OmitClaudeMd
	}
	return s, nil
}

// scoutContract es fijo: el scout no escribe archivos, bflow guarda su reporte.
func scoutContract(name string, loc *i18n.Localizer) string {
	if loc == nil {
		loc = i18n.DefaultLocalizer()
	}
	var w strings.Builder
	w.WriteString(loc.T("agent.contract_header"))
	w.WriteString(loc.T("agent.scout_intro"))
	w.WriteString(loc.T("agent.scout_receive"))
	w.WriteString(loc.T("agent.scout_read_task"))
	w.WriteString(loc.T("agent.scout_no_write"))
	w.WriteString(loc.T("agent.scout_report", name))
	w.WriteString(loc.T("agent.scout_exit2"))
	w.WriteString(loc.T("agent.contract_final_response"))
	return w.String()
}

func pick(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

func joinPhases(ps []flow.Phase) string {
	s := make([]string, len(ps))
	for i, p := range ps {
		s[i] = string(p)
	}
	return strings.Join(s, ", ")
}

func body(name string, phases []flow.Phase, b base, craft string, read []string, extra string, loc *i18n.Localizer) string {
	return bodyWith(contract(name, phases, b, loc), craft, read, extra, loc)
}

func bodyWith(head, craft string, read []string, extra string, loc *i18n.Localizer) string {
	if loc == nil {
		loc = i18n.DefaultLocalizer()
	}
	var w strings.Builder
	w.WriteString(head)
	if craft != "" {
		w.WriteString("\n" + loc.T("agent.craft_header") + craft + "\n")
	}
	if len(read) > 0 || extra != "" {
		w.WriteString("\n" + loc.T("agent.repo_craft_header"))
		if len(read) > 0 {
			w.WriteString(loc.T("agent.repo_craft_read", "`"+strings.Join(read, "`, `")+"`"))
		}
		if extra != "" {
			if len(read) > 0 {
				w.WriteString("\n")
			}
			w.WriteString(extra + "\n")
		}
	}
	return w.String()
}

// contract sale del flujo: si cambian los veredictos o los archivos, cambia
// el contrato en el próximo bflow render.
func contract(name string, phases []flow.Phase, b base, loc *i18n.Localizer) string {
	if loc == nil {
		loc = i18n.DefaultLocalizer()
	}
	var w strings.Builder
	w.WriteString(loc.T("agent.contract_header"))
	w.WriteString(loc.T("agent.contract_intro"))
	w.WriteString(loc.T("agent.contract_receive"))
	w.WriteString(loc.T("agent.contract_read"))
	w.WriteString(loc.T("agent.contract_pasted"))

	var writes []string
	if b.writesSpec {
		writes = append(writes, loc.T("agent.contract_spec_part"))
	}
	for _, f := range b.writes {
		writes = append(writes, "`.bflow/tasks/<id>/"+f+"`")
	}
	if len(writes) > 0 {
		w.WriteString(loc.T("agent.contract_writes", strings.Join(writes, loc.T("agent.contract_and"))))
	} else {
		w.WriteString(loc.T("agent.contract_no_writes"))
	}
	if b.changelog {
		w.WriteString(loc.T("agent.contract_changelog_writer"))
	} else if slices.Contains(phases, flow.Implementing) {
		w.WriteString(loc.T("agent.contract_changelog_prohibited"))
	}
	if slices.Contains(phases, flow.Contract) {
		w.WriteString(loc.T("agent.contract_contract_ready"))
	}
	if slices.Contains(phases, flow.Implementing) {
		w.WriteString(loc.T("agent.contract_done_requirements"))
	}

	w.WriteString(loc.T("agent.contract_report_header"))
	var all []flow.Verdict
	for _, p := range phases {
		vs := flow.Verdicts(p)
		names := make([]string, len(vs))
		for i, v := range vs {
			names[i] = string(v)
			if !slices.Contains(all, v) {
				all = append(all, v)
			}
		}
		w.WriteString(loc.T("agent.contract_report_phase", p, name, strings.Join(names, "|")))
	}
	if slices.Contains(all, flow.NeedsDecision) {
		w.WriteString(loc.T("agent.contract_needs_decision"))
	}
	var withNote []string
	for _, v := range []flow.Verdict{flow.BlockedV, flow.RejectedV, flow.Split} {
		if slices.Contains(all, v) {
			withNote = append(withNote, string(v))
		}
	}
	if len(withNote) > 0 {
		w.WriteString(loc.T("agent.contract_with_note", strings.Join(withNote, ", ")))
	}
	w.WriteString(loc.T("agent.contract_exit2"))
	w.WriteString(loc.T("agent.contract_final_response"))
	return w.String()
}

// SettingsResult es lo que devuelve instalar los ajustes de bflow en la
// configuración de la herramienta del repo.
type SettingsResult struct {
	Path     string
	Changed  bool
	Warnings []string
}

// LogFinding es un hallazgo al leer el log de una herramienta de agente.
// Area: agente|log|proveedor; Level: ok|warn. Vive aquí para que los
// adaptadores no importen cli.
type LogFinding struct{ Area, Level, Msg string }

// Models da el modelo configurado de cada agente del flujo (con el scout): el de
// agents.<nombre>.model o el del catálogo; "" si ninguno lo fija.
func Models(fc flow.Config, conf map[string]config.AgentConf) map[string]string {
	out := map[string]string{}
	names := fc.AgentNames()
	if fc.Scout != "" {
		names = append(names, fc.Scout)
	}
	for _, name := range names {
		out[name] = pick(conf[name].Model, catalog[name].model)
	}
	return out
}

// EvalReviewerSpec arma el agente del eval del reviewer (bflow-eval-reviewer):
// mismo modelo y esfuerzo que agents.reviewer, contrato propio y sin oficio
// embebido, porque lo lee del archivo craft que recibe en cada corrida.
func EvalReviewerSpec(conf config.AgentConf, locs ...*i18n.Localizer) Spec {
	var loc *i18n.Localizer
	if len(locs) > 0 {
		loc = locs[0]
	}
	if loc == nil {
		loc = i18n.DefaultLocalizer()
	}
	b := catalog["reviewer"]
	desc := loc.T("agent.desc.eval-reviewer")
	s := Spec{
		Name:         "eval-reviewer",
		Subagent:     flow.SubagentPrefix + "eval-reviewer",
		Description:  desc,
		Tools:        []string{Read, Write, Bash},
		Model:        pick(conf.Model, b.model),
		Effort:       pick(conf.Effort, b.effort),
		OmitClaudeMd: len(conf.Read) > 0,
		Body:         evalContract(loc),
	}
	if conf.OmitClaudeMd != nil {
		s.OmitClaudeMd = *conf.OmitClaudeMd
	}
	return s
}

func evalContract(loc *i18n.Localizer) string {
	if loc == nil {
		loc = i18n.DefaultLocalizer()
	}
	var w strings.Builder
	w.WriteString(loc.T("agent.contract_header"))
	w.WriteString(loc.T("agent.eval_intro"))
	w.WriteString(loc.T("agent.eval_receive"))
	w.WriteString(loc.T("agent.eval_read_craft"))
	w.WriteString(loc.T("agent.eval_diff"))
	w.WriteString(loc.T("agent.eval_no_task"))
	w.WriteString(loc.T("agent.eval_reports"))
	w.WriteString(loc.T("agent.eval_report_done"))
	return w.String()
}
