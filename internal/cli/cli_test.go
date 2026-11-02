package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"innobytes.tech/bflow/internal/i18n"
	"innobytes.tech/bflow/internal/output"
)

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(args, &Env{Stdout: &out, Stderr: &errb, Version: "test"})
	return code, out.String(), errb.String()
}

func TestVersionJSON(t *testing.T) {
	code, out, _ := run(t, "version", "--json")
	if code != output.ExitOK {
		t.Fatalf("exit %d", code)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("no es JSON: %v\n%s", err, out)
	}
	if env["data"].(map[string]any)["version"] != "test" {
		t.Errorf("version: %v", env)
	}
}

func TestJSONFlagAnywhere(t *testing.T) {
	code, out, _ := run(t, "--json", "version")
	if code != 0 || !strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Errorf("--json antes del subcomando: exit=%d out=%q", code, out)
	}
}

func TestUnknownCommandIsError(t *testing.T) {
	code, out, _ := run(t, "nope", "--json")
	if code != output.ExitError {
		t.Errorf("exit=%d, want 1", code)
	}
	if !strings.Contains(out, `"unknown_command"`) {
		t.Errorf("salida: %s", out)
	}
}

func TestHelpListsCommands(t *testing.T) {
	code, out, _ := run(t, "help")
	if code != 0 || !strings.Contains(out, "version") {
		t.Errorf("help: exit=%d out=%q", code, out)
	}
	if !strings.Contains(out, "usage: bflow <command> [--json]") {
		t.Errorf("help default should be English: %q", out)
	}
	if !strings.Contains(out, "bflow version") || !strings.Contains(out, "list commands") {
		t.Errorf("help summaries should be English by default: %q", out)
	}
}

func TestHelpLocalizationSpanish(t *testing.T) {
	var out, errb bytes.Buffer
	b, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	loc := b.New("es")
	code := Run([]string{"help"}, &Env{Stdout: &out, Stderr: &errb, Version: "test", Loc: loc})
	if code != 0 {
		t.Fatalf("help exit %d", code)
	}
	got := out.String()
	if !strings.Contains(got, "uso: bflow <comando> [--json]") {
		t.Errorf("help Spanish header missing: %q", got)
	}
	if !strings.Contains(got, "versión de bflow") || !strings.Contains(got, "lista los comandos") {
		t.Errorf("help Spanish summaries missing: %q", got)
	}
}

func TestUnknownCommandLocalization(t *testing.T) {
	// English default
	var outEn, errbEn bytes.Buffer
	code := Run([]string{"nonexistent"}, &Env{Stdout: &outEn, Stderr: &errbEn, Version: "test"})
	if code != output.ExitError {
		t.Errorf("exit=%d, want 1", code)
	}
	if !strings.Contains(outEn.String(), `unknown command: "nonexistent"`) {
		t.Errorf("English unknown command failed: %s", outEn.String())
	}

	// Spanish
	b, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	var outEs, errbEs bytes.Buffer
	code = Run([]string{"nonexistent"}, &Env{Stdout: &outEs, Stderr: &errbEs, Version: "test", Loc: b.New("es")})
	if code != output.ExitError {
		t.Errorf("exit=%d, want 1", code)
	}
	if !strings.Contains(outEs.String(), `comando desconocido: "nonexistent"`) {
		t.Errorf("Spanish unknown command failed: %s", outEs.String())
	}
}
