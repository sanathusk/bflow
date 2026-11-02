package claude

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"innobytes.tech/bflow/internal/agents"
	"innobytes.tech/bflow/internal/flow"
)

func TestOlder(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"2.1.270", "2.1.271", true},
		{"2.1.271", "2.1.271", false},
		{"2.1.283", "2.1.271", false},
		{"2.0.999", "2.1.0", true},
		{"3.0.0", "2.1.271", false},
		{"2.1.9", "2.1.10", true}, // numérico, no lexicográfico
	}
	for _, c := range cases {
		if got := older(c.a, c.b); got != c.want {
			t.Errorf("older(%s, %s) = %v", c.a, c.b, got)
		}
	}
}

// allSpecs construye todos los agentes del catálogo y el scout.
func allSpecs(t *testing.T, root string) []agents.Spec {
	t.Helper()
	fc := flow.DefaultConfig()
	fc.Agents = map[flow.Phase][]string{
		flow.Spec:         {"spec-author", "ui-designer"},
		flow.Contract:     {"implementer"},
		flow.Implementing: {"implementer"},
		flow.Quality:      {"reviewer", "security-auditor", "ux-auditor"},
		flow.Documenting:  {"documenter"},
	}
	fc.Scout = "scout"
	specs, err := agents.Build(fc, nil, root)
	if err != nil {
		t.Fatal(err)
	}
	return specs
}

// variable: lo que cambia entre tareas o máquinas y rompe el prefijo de caché (GH-19).
func variable(root string) []*regexp.Regexp {
	return []*regexp.Regexp{
		regexp.MustCompile(`[A-Z][A-Z0-9]*-[0-9]+`),
		regexp.MustCompile(`[0-9]{4}-[0-9]{2}-[0-9]{2}`),
		regexp.MustCompile(`\.bflow/tasks/[^<\s]`),
		regexp.MustCompile(regexp.QuoteMeta(root)),
	}
}

func TestRenderedAgentsStablePrefix(t *testing.T) {
	root := t.TempDir()
	specs := allSpecs(t, root)
	first, err := Agent{}.RenderAgents(specs)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Agent{}.RenderAgents(specs)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(specs) {
		t.Fatalf("un archivo por agente: %d", len(first))
	}
	for path, doc := range first {
		if !bytes.Equal(doc, second[path]) {
			t.Errorf("%s: dos renders seguidos difieren", path)
		}
		for _, re := range variable(root) {
			if loc := re.FindIndex(doc); loc != nil {
				t.Errorf("%s: %q aparece en el archivo: %q", path, re, doc[loc[0]:loc[1]])
			}
		}
		_, body, ok := strings.Cut(string(doc), "\n---\n")
		if !ok || (!strings.HasPrefix(body, agents.GeneratedMark+"\n\n## Contract with bflow") && !strings.HasPrefix(body, agents.GeneratedMark+"\n\n## Contrato con bflow")) {
			t.Errorf("%s: el cuerpo empieza con la marca y el contrato: %q", path, body[:min(len(body), 80)])
		}
	}
}
