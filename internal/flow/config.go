package flow

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"innobytes.tech/bflow/internal/i18n"
)

// Config es lo que el núcleo necesita saber del proyecto.
type Config struct {
	// Lanes: fases que recorre cada carril, en el orden de Order.
	Lanes map[Lane][]Phase
	// Agents: agentes que trabajan en cada fase. En Quality corren en paralelo;
	// en las demás fases, uno tras otro en el orden dado.
	Agents map[Phase][]string
	// MaxQualityRounds: al llegar a este número de rechazos de calidad se
	// pregunta al humano antes de otra ronda.
	MaxQualityRounds int
	// UI: el proyecto tiene interfaz; la spec lleva UI blueprint y el humano
	// lo ve al aprobarla.
	UI bool
	// ContractGate: el repo no puede validar el contrato, así que CONTRACT_READY abre la gate contract.
	ContractGate bool
	// Changelog: plantilla de la ruta del changelog para consumidores, con
	// {id} y {slug}; vacía, va junto a la spec.
	Changelog string
	// PublicPaths: ver config.Flow.PublicPaths; vacío = el gate no actúa.
	PublicPaths []string
	// NarrativeComments: false apaga el rechazo narrative_comment.
	NarrativeComments bool
	// CommentPatterns: ver config.Flow.CommentPatterns.
	CommentPatterns []string
	// Scout: nombre del agente scout; vacío = apagado.
	Scout string
	// Budget: tokens nuevos presupuestados por fase (GH-16); fase ausente =
	// sin presupuesto.
	Budget map[Phase]int64
	// Loc: localizador de mensajes para preguntas, opciones y descripciones.
	Loc *i18n.Localizer
}

func (c Config) t(key string, args ...any) string {
	if c.Loc != nil {
		return c.Loc.T(key, args...)
	}
	return i18n.T(key, args...)
}

// BudgetPhases son las fases que aceptan flow.budget en bflow.yaml.
var BudgetPhases = []Phase{Discovery, Spec, Contract, Implementing, Paused, Quality, Documenting, Walkthrough}

// BudgetMultiple es el múltiplo del presupuesto ya gastado: floor(used/budget),
// 0 si budget no es positivo.
func BudgetMultiple(budget, used int64) int {
	if budget <= 0 {
		return 0
	}
	return int(used / budget)
}

// budgetTerminal son las fases en las que no se pregunta por presupuesto.
var budgetTerminal = []Phase{Done, Dropped, InReview, Blocked, Backlog}

// BudgetDue dice si, en el estado s, toca preguntar por presupuesto: la fase
// está en cfg.Budget, no es una fase terminal, k ≥ 1 y k es mayor que el
// último múltiplo respondido en esa fase (s.BudgetAck).
func BudgetDue(cfg Config, s State, used int64) (k int, ok bool) {
	budget, has := cfg.Budget[s.Phase]
	if !has || slices.Contains(budgetTerminal, s.Phase) {
		return 0, false
	}
	k = BudgetMultiple(budget, used)
	if k < 1 || k <= s.BudgetAck[s.Phase] {
		return 0, false
	}
	return k, true
}

// ChangelogPath es la ruta, versionada, del único changelog para consumidores
// de la tarea.
func (c Config) ChangelogPath(id, slug string) string {
	if c.Changelog == "" {
		return path.Dir(SpecPath(id, slug)) + "/consumer-changelog.md"
	}
	return strings.NewReplacer("{id}", id, "{slug}", slug).Replace(c.Changelog)
}

// PhasesOf devuelve, en el orden del flujo, las fases en las que trabaja el agente.
func (c Config) PhasesOf(agent string) []Phase {
	var ps []Phase
	for _, p := range Order {
		if slices.Contains(c.Agents[p], agent) {
			ps = append(ps, p)
		}
	}
	return ps
}

// AgentNames devuelve todos los agentes del flujo, en orden de aparición.
func (c Config) AgentNames() []string {
	var names []string
	for _, p := range Order {
		for _, a := range c.Agents[p] {
			if !slices.Contains(names, a) {
				names = append(names, a)
			}
		}
	}
	return names
}

// SubagentPrefix distingue los agentes que genera bflow de los del usuario.
const SubagentPrefix = "bflow-"

// afterPause es la fase que sigue a la pausa: la que sigue a paused si el
// carril la tiene, o la que sigue a implementing.
func (c Config) afterPause(l Lane) Phase {
	if c.has(l, Paused) {
		return c.after(l, Paused)
	}
	return c.after(l, Implementing)
}

// DefaultLanes son los carriles por defecto. `paused` es opcional: un carril
// la puede declarar, o una persona la pide con `bflow pause`.
func DefaultLanes() map[Lane][]Phase {
	return map[Lane][]Phase{
		Full:   {Discovery, Spec, Contract, Implementing, Quality, Documenting, Walkthrough, InReview, Done},
		Light:  {Spec, Implementing, Quality, Documenting, Walkthrough, InReview, Done},
		Hotfix: {Implementing, Quality, Documenting, Walkthrough, InReview, Done},
	}
}

// DefaultConfig es la configuración sin bflow.yaml.
func DefaultConfig() Config {
	return Config{
		Lanes: DefaultLanes(),
		Agents: map[Phase][]string{
			Spec:         {"spec-author"},
			Contract:     {"implementer"},
			Implementing: {"implementer"},
			Quality:      {"reviewer"}, // con security_audit: true se suma security-auditor
			Documenting:  {"documenter"},
		},
		MaxQualityRounds: 2,
	}
}

// Validate comprueba que la configuración respete las reglas del flujo.
func (c Config) Validate() error {
	if c.MaxQualityRounds < 1 {
		return fmt.Errorf("max_quality_rounds debe ser ≥ 1 (es %d)", c.MaxQualityRounds)
	}
	if len(c.Lanes) == 0 {
		return fmt.Errorf("no hay carriles definidos")
	}
	for lane, phases := range c.Lanes {
		if err := validLane(lane, phases); err != nil {
			return err
		}
	}
	for _, p := range []Phase{Implementing, Quality, Documenting} {
		if len(c.Agents[p]) == 0 {
			return fmt.Errorf("la fase %s necesita al menos un agente", p)
		}
	}
	for _, lane := range c.Lanes {
		for _, p := range []Phase{Spec, Contract} {
			if slices.Contains(lane, p) && len(c.Agents[p]) == 0 {
				return fmt.Errorf("la fase %s necesita al menos un agente", p)
			}
		}
	}
	return nil
}

func validLane(lane Lane, phases []Phase) error {
	if len(phases) == 0 {
		return fmt.Errorf("carril %s: sin fases", lane)
	}
	last := -1
	for _, p := range phases {
		i := slices.Index(Order, p)
		if i < 0 {
			return fmt.Errorf("carril %s: fase desconocida %q", lane, p)
		}
		if i <= last {
			return fmt.Errorf("carril %s: fases fuera de orden o repetidas en %q", lane, p)
		}
		last = i
	}
	// Ningún carril se salta la compuerta de calidad ni el cierre por merge.
	for _, must := range []Phase{Implementing, Quality, Walkthrough, InReview, Done} {
		if !slices.Contains(phases, must) {
			return fmt.Errorf("carril %s: la fase %s es obligatoria", lane, must)
		}
	}
	if slices.Contains(phases, Contract) && !slices.Contains(phases, Spec) {
		return fmt.Errorf("carril %s: contract requiere spec", lane)
	}
	return nil
}

func (c Config) has(l Lane, p Phase) bool { return slices.Contains(c.Lanes[l], p) }

// after devuelve la fase que sigue a p en el carril.
func (c Config) after(l Lane, p Phase) Phase {
	phases := c.Lanes[l]
	i := slices.Index(phases, p)
	if i < 0 || i+1 >= len(phases) {
		return Done
	}
	return phases[i+1]
}
