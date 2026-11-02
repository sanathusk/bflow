package agents

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"innobytes.tech/bflow/internal/config"
	"innobytes.tech/bflow/internal/flow"
	"innobytes.tech/bflow/internal/testutil"
)

func scoutFlow() flow.Config {
	fc := flow.DefaultConfig()
	fc.Scout = flow.ScoutAgent
	return fc
}

// R21: el scout solo lee (read y bash), con haiku y esfuerzo bajo por defecto.
func TestBuildScoutReadOnly(t *testing.T) {
	specs, err := Build(scoutFlow(), nil, testutil.TempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	sc, ok := byName(t, specs)["scout"]
	if !ok {
		t.Fatal("con el scout activo debe generarse bflow-scout")
	}
	if sc.Subagent != "bflow-scout" || sc.Model != "haiku" || sc.Effort != "low" || sc.OmitClaudeMd {
		t.Errorf("scout: %+v", sc)
	}
	if !slices.Equal(sc.Tools, []string{Read, Bash}) {
		t.Errorf("herramientas: %v (sin write)", sc.Tools)
	}
	if !strings.Contains(sc.Body, "## Craft\n") && !strings.Contains(sc.Body, "## Oficio\n") {
		t.Errorf("sin oficio por defecto:\n%s", sc.Body)
	}
	// Apagado, no se genera; y los demás agentes siguen con Read, Write y Bash.
	off, err := Build(flow.DefaultConfig(), nil, testutil.TempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, has := byName(t, off)["scout"]; has {
		t.Error("sin scout en la config no se genera")
	}
	if imp := byName(t, specs)["implementer"]; !slices.Contains(imp.Tools, Write) {
		t.Errorf("el implementer escribe: %v", imp.Tools)
	}

	// Ajustes del repo: model, effort, read, extra y omit_claude_md.
	root := testutil.TempDir(t)
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "scout.md"), []byte("Mira primero cmd/.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	no := false
	specs, err = Build(scoutFlow(), map[string]config.AgentConf{
		"scout": {Model: "sonnet", Effort: "medium", Read: []string{"docs/architecture/"}, Extra: "docs/scout.md", OmitClaudeMd: &no},
	}, root)
	if err != nil {
		t.Fatal(err)
	}
	sc = byName(t, specs)["scout"]
	if sc.Model != "sonnet" || sc.Effort != "medium" || sc.OmitClaudeMd || !slices.Equal(sc.Tools, []string{Read, Bash}) {
		t.Errorf("ajustes: %+v", sc)
	}
	if !strings.Contains(sc.Body, "`docs/architecture/`") || !strings.Contains(sc.Body, "Mira primero cmd/.") {
		t.Errorf("oficio del repo:\n%s", sc.Body)
	}
}

// R22, R24: el contrato del scout reporta por stdin; el de los demás agentes
// lista scout entre lo que muestra bflow show.
func TestScoutContractStdin(t *testing.T) {
	specs, err := Build(scoutFlow(), nil, testutil.TempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	m := byName(t, specs)
	body := m["scout"].Body
	for _, want := range []string{
		"bflow report <id> --agent scout --verdict DONE --stdin",
		"≤40",
		"`bflow show <id> task`",
		"<pasted_content>",
		"`bflow report`",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("el contrato del scout no dice %q:\n%s", want, body)
		}
	}
	if !strings.Contains(body, "You do not write files") && !strings.Contains(body, "No escribes archivos") {
		t.Errorf("el contrato del scout no indica que no escribe archivos:\n%s", body)
	}
	for _, bad := range []string{"NEEDS_DECISION", "DONE exige", "congeladas", "Escribes `"} {
		if strings.Contains(body, bad) {
			t.Errorf("el contrato del scout no debe decir %q", bad)
		}
	}
	for _, name := range []string{"implementer", "spec-author", "reviewer"} {
		if !strings.Contains(m[name].Body, "`bflow show <id> discovery|contract|review-map|check|scout`") {
			t.Errorf("%s: bflow show no lista scout:\n%s", name, m[name].Body)
		}
	}
}
