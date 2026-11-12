package engine

import (
	"context"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"os"
	"slices"
	"strings"

	"innobytes.tech/bflow/internal/flow"
	"innobytes.tech/bflow/internal/i18n"
	"innobytes.tech/bflow/internal/output"
	"innobytes.tech/bflow/internal/review"
	"innobytes.tech/bflow/internal/store"
)

// maxDisplayLines acota lo que se muestra en una gate: más que eso no se lee
// para decidir, y el resto sigue a un comando de distancia.
const maxDisplayLines = 150

// withDisplay agrega a una pregunta lo que el humano tiene que ver para
// decidir (el brief, el contrato, el review-map…), ya leído. En la piloto la
// sesión corría `bflow show` pero no copiaba la salida al chat, y el humano
// aprobaba sin ver nada: con el texto en `next`, solo tiene que repetirlo.
func (e *Engine) withDisplay(ctx context.Context, rec store.Record, n output.Next) output.Next {
	if n.Action == output.ActionSpawn {
		if files := e.pendingAllow(rec); len(files) > 0 {
			return e.holdForAllow(rec.Flow.ID, files)
		}
	}
	if n.Action != output.ActionAsk || len(n.Show) == 0 {
		return n
	}
	var parts []string
	if g := rec.Flow.Gate; g != nil && g.Name == flow.GateWalkthrough {
		if cov, ok := e.lastCoverage(rec.Flow.ID); ok {
			parts = append(parts, e.loc().T("engine.display.reviewer_coverage_title")+"\n"+strings.Join(cov.Lines(), "\n"))
		}
	}
	if g := rec.Flow.Gate; g != nil && g.Name == flow.GateWalkthrough && rec.Flow.Note != "" {
		parts = append(parts, e.loc().T("engine.display.product_answers_title")+"\n"+rec.Flow.Note)
	}
	for _, c := range n.Show {
		f := strings.Fields(c) // bflow show <ID> <qué> [--section <s>]
		if len(f) < 4 || f[1] != "show" {
			continue
		}
		section := ""
		if len(f) >= 6 && f[4] == "--section" {
			section = f[5]
		}
		text, err := e.Show(ctx, f[2], f[3], section)
		if err != nil {
			text = "(" + err.Error() + ")"
		}
		parts = append(parts, e.clip(strings.TrimSpace(text), c))
	}
	n.Display = strings.Join(parts, "\n\n")
	return n
}

func (e *Engine) clip(text, cmd string) string {
	return clip(text, cmd, e.loc())
}

func clip(text, cmd string, locs ...*i18n.Localizer) string {
	lines := strings.Split(text, "\n")
	if len(lines) <= maxDisplayLines {
		return text
	}
	return strings.Join(lines[:maxDisplayLines], "\n") + "\n" + locOf(locs...).T("engine.display.clip_suffix", len(lines)-maxDisplayLines, cmd)
}

// productQuestions saca del review-map la sección de preguntas de producto sin
// las líneas "el código responde": el humano contesta sin ver la respuesta.
func (e *Engine) productQuestions(id string) (string, error) {
	b, err := e.Store.ReadFile(id, artifacts["review-map"])
	if err != nil {
		if os.IsNotExist(err) {
			return e.loc().T("engine.display.review_map_not_exist"), nil
		}
		return "", err
	}
	var out, block []string
	var scan review.OptionScanner
	in, level, question := false, 0, ""
	// flush mezcla el bloque de opciones pendiente con la semilla de su pregunta.
	flush := func() {
		if len(block) == 0 {
			return
		}
		h := fnv.New64a()
		h.Write([]byte(id + "\n" + question))
		out = append(out, shuffleOptions(h.Sum64(), block)...)
		block = nil
	}
	for _, l := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(l)
		if h := strings.IndexFunc(t, func(r rune) bool { return r != '#' }); strings.HasPrefix(t, "#") && h > 0 {
			low := strings.ToLower(t)
			switch {
			case strings.Contains(low, "preguntas") || strings.Contains(low, "questions"):
				flush()
				in, level, scan = true, h, review.OptionScanner{}
				continue
			case in && h <= level:
				flush()
				in = false
			}
		}
		if !in {
			continue
		}
		low := strings.ToLower(t)
		if strings.Contains(low, "el código responde") || strings.Contains(low, "the code answers") {
			scan.Next(l)
			continue
		}
		if _, ok := scan.Next(l); ok {
			block = append(block, l)
			continue
		}
		flush()
		out = append(out, l)
		if t != "" {
			question = t
		}
	}
	flush()
	q := strings.TrimSpace(strings.Join(out, "\n"))
	if q == "" {
		return e.loc().T("engine.display.review_map_no_questions"), nil
	}
	return q, nil
}

// shuffleOptions devuelve opts en un orden mezclado que solo depende de seed.
func shuffleOptions(seed uint64, opts []string) []string {
	out := slices.Clone(opts)
	r := rand.New(rand.NewPCG(seed, seed))
	r.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

// pendingAllow son los archivos congelados que la decisión elegida pidió dejar
// cambiar con `freeze --allow` y que una persona todavía no liberó.
func (e *Engine) pendingAllow(rec store.Record) []string {
	s := rec.Flow
	if s.Decision == "" || s.Gate != nil || s.Phase != flow.Implementing {
		return nil
	}
	m := e.readFrozen(s.ID)
	var out []string
	for _, f := range flow.AllowFiles(s.Decision) {
		if h, ok := m[f]; ok && h != allowed {
			out = append(out, f)
		}
	}
	return out
}

// holdForAllow retiene el spawn: el guard no deja que un agente corra
// `freeze --allow`, así que lo corre una persona antes de relanzarlo.
func (e *Engine) holdForAllow(id string, files []string) output.Next {
	list := strings.Join(files, " ")
	return output.Next{
		Action:   output.ActionAsk,
		Gate:     "freeze_allow",
		Question: e.loc().T("engine.display.hold_freeze_allow_question", list),
		Options: []output.Option{
			{ID: "done", Label: e.loc().T("engine.display.option_already_ran"), Command: "bflow status " + id},
			{ID: "block", Label: e.loc().T("engine.display.option_leave_for_later"), Command: fmt.Sprintf("bflow block %s --reason %q", id, e.loc().T("engine.display.block_missing_freeze_allow", files[0]))},
		},
	}
}
