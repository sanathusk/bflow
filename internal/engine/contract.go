package engine

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"innobytes.tech/bflow/internal/config"
	"innobytes.tech/bflow/internal/flow"
)

const contractFailLines = 10

var goTestFunc = regexp.MustCompile(`(?m)^func (Test\w+)\(`)

// contractCheck valida el contrato antes de aceptar CONTRACT_READY. verified
// es true si el último check confirma que las pruebas del contrato fallan contra los stubs; false
// sin rechazo significa que no se pudo validar y decide la gate contract.
func (e *Engine) contractCheck(ctx context.Context, id string) (verified bool, rej *flow.Rejection) {
	files := e.taskTests(ctx)
	if hollow := hollowTests(e.Cfg.Root, files); len(hollow) > 0 {
		return false, &flow.Rejection{Code: "contract_hollow", Reason: e.loc().T("engine.contract.hollow", strings.Join(hollow, "\n"))}
	}
	testStep := e.Cfg.Check.TestStep
	if testStep == "" {
		testStep = "test"
	}
	if !e.contractValidable() {
		return false, nil
	}
	res, fresh, detail, err := e.Contract.Last(ctx, id)
	if err != nil {
		detail = err.Error()
		fresh = false
	}
	if !fresh {
		return false, &flow.Rejection{Code: "contract_stale", Reason: e.loc().T("engine.contract.stale", detail, id)}
	}
	for _, s := range res.Steps {
		if s.Name == testStep {
			break
		}
		if s.Status == "fail" {
			return false, &flow.Rejection{Code: "contract_broken", Reason: e.loc().T("engine.contract.broken", s.Name, strings.Join(failLines(s.Fails, s.Tail, contractFailLines), "\n"), id)}
		}
	}
	t1 := contractTestNames(e.Cfg.Root, files)
	names := t1.names()
	for _, s := range res.Steps {
		if s.Name != testStep || s.Status != "fail" {
			continue
		}
		lines := failLines(s.Fails, s.Tail, -1)
		if buildFailed(lines) {
			return false, nil
		}
		if slices.ContainsFunc(lines, t1.failed) {
			return true, nil
		}
	}
	return false, &flow.Rejection{Code: "contract_green", Reason: e.loc().T("engine.contract.green", testStep, strings.Join(firstN(names, 5), ", "), id)}
}

// failLines son las líneas de fallo del paso: Fails, o las del Tail si no hay.
func failLines(fails []string, tail string, max int) []string {
	out := fails
	if len(out) == 0 {
		for _, l := range strings.Split(tail, "\n") {
			if strings.TrimSpace(l) != "" {
				out = append(out, l)
			}
		}
	}
	if max > 0 && len(out) > max {
		out = out[:max]
	}
	return out
}

func firstN(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// contractT1 son los nombres con que una línea de fallo nombra una prueba del contrato.
type contractT1 struct {
	Funcs []string // funciones Test* de los archivos .go del contrato
	Paths []string // rutas (con /) de los archivos del contrato que no son .go
}

// contractTestNames separa los archivos del contrato en funciones Test* (Go) y rutas.
func contractTestNames(root string, files []string) contractT1 {
	var out contractT1
	for _, f := range files {
		f = strings.ReplaceAll(f, `\`, "/")
		if !strings.HasSuffix(f, ".go") {
			if !slices.Contains(out.Paths, f) {
				out.Paths = append(out.Paths, f)
			}
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			continue
		}
		for _, m := range goTestFunc.FindAllSubmatch(b, -1) {
			if n := string(m[1]); !slices.Contains(out.Funcs, n) {
				out.Funcs = append(out.Funcs, n)
			}
		}
	}
	return out
}

// names son Funcs y luego Paths, para el mensaje contract_green.
func (c contractT1) names() []string {
	return append(slices.Clone(c.Funcs), c.Paths...)
}

// failed dice si la línea de fallo nombra con exactitud una prueba del contrato.
func (c contractT1) failed(line string) bool {
	t := strings.TrimSpace(line)
	for _, f := range c.Funcs {
		if rest, ok := strings.CutPrefix(t, "--- FAIL: "+f); ok && (rest == "" || rest[0] == ' ' || rest[0] == '/') {
			return true
		}
	}
	n := strings.ReplaceAll(line, `\`, "/")
	for _, p := range c.Paths {
		for from := 0; from <= len(n); {
			i := strings.Index(n[from:], p)
			if i < 0 {
				break
			}
			i += from
			end := i + len(p)
			if (i == 0 || !pathByte(n[i-1])) && (end >= len(n) || !pathByte(n[end])) {
				return true
			}
			from = i + 1
		}
	}
	return false
}

func pathByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '.' || b == '-'
}

// buildFailed dice si alguna línea indica que la prueba no llegó a correr.
func buildFailed(lines []string) bool {
	for _, l := range lines {
		if strings.Contains(l, "[build failed]") || strings.Contains(l, "[setup failed]") {
			return true
		}
	}
	return false
}

// contractValidable dice si el repo puede validar el contrato con el último check.
func (e *Engine) contractValidable() bool {
	if e.Contract == nil {
		return false
	}
	testStep := e.Cfg.Check.TestStep
	if testStep == "" {
		testStep = "test"
	}
	return slices.ContainsFunc(e.Cfg.Check.Steps, func(s config.Step) bool { return s.Name == testStep })
}

// Pause marca la tarea para parar antes de quality.
func (e *Engine) Pause(ctx context.Context, id string) (Outcome, error) {
	return e.apply(ctx, id, flow.Event{Kind: flow.EvPause}, "")
}
