package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"innobytes.tech/bflow/internal/config"
	"innobytes.tech/bflow/internal/engine"
	"innobytes.tech/bflow/internal/i18n"
	"innobytes.tech/bflow/internal/store"
	"innobytes.tech/bflow/internal/testutil"
)

func TestInitLanguage(t *testing.T) {
	t.Run("default --yes uses en", func(t *testing.T) {
		env := &Env{
			Stdout: &bytes.Buffer{},
			Stderr: &bytes.Buffer{},
			Stdin:  strings.NewReader(""),
			Dir:    testutil.TempDir(t),
		}
		t.Setenv("BFLOW_CONFIG_HOME", testutil.TempDir(t))
		if code := Run([]string{"init", "--yes"}, env); code != 0 {
			t.Fatalf("exit %d: %s", code, env.Stdout.(*bytes.Buffer))
		}
		b, err := os.ReadFile(filepath.Join(env.Dir, "bflow.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		y := string(b)
		if !strings.Contains(y, "lang: en") {
			t.Errorf("expected 'lang: en' in bflow.yaml, got:\n%s", y)
		}
	})

	t.Run("--lang es writes es", func(t *testing.T) {
		env := &Env{
			Stdout: &bytes.Buffer{},
			Stderr: &bytes.Buffer{},
			Stdin:  strings.NewReader(""),
			Dir:    testutil.TempDir(t),
		}
		t.Setenv("BFLOW_CONFIG_HOME", testutil.TempDir(t))
		if code := Run([]string{"init", "--yes", "--lang", "es"}, env); code != 0 {
			t.Fatalf("exit %d: %s", code, env.Stdout.(*bytes.Buffer))
		}
		b, err := os.ReadFile(filepath.Join(env.Dir, "bflow.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		y := string(b)
		if !strings.Contains(y, "lang: es") {
			t.Errorf("expected 'lang: es' in bflow.yaml, got:\n%s", y)
		}
	})

	t.Run("--lang invalid fails", func(t *testing.T) {
		env := &Env{
			Stdout: &bytes.Buffer{},
			Stderr: &bytes.Buffer{},
			Stdin:  strings.NewReader(""),
			Dir:    testutil.TempDir(t),
		}
		t.Setenv("BFLOW_CONFIG_HOME", testutil.TempDir(t))
		if code := Run([]string{"init", "--yes", "--lang", "fr"}, env); code == 0 {
			t.Fatalf("expected error for unsupported language 'fr', got exit 0")
		}
	})

	t.Run("interactive chooses es", func(t *testing.T) {
		env := &Env{
			Stdout: &bytes.Buffer{},
			Stderr: &bytes.Buffer{},
			Stdin:  strings.NewReader("2\n"), // Option 2: es (Español)
			Dir:    testutil.TempDir(t),
		}
		t.Setenv("BFLOW_CONFIG_HOME", testutil.TempDir(t))
		if code := Run([]string{"init", "--host", "none", "--tracker", "local"}, env); code != 0 {
			t.Fatalf("exit %d: %s", code, env.Stdout.(*bytes.Buffer))
		}
		b, err := os.ReadFile(filepath.Join(env.Dir, "bflow.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		y := string(b)
		if !strings.Contains(y, "lang: es") {
			t.Errorf("expected 'lang: es' in bflow.yaml, got:\n%s", y)
		}
	})

	t.Run("interactive chooses en displays english tracker question", func(t *testing.T) {
		stderr := &bytes.Buffer{}
		env := &Env{
			Stdout: &bytes.Buffer{},
			Stderr: stderr,
			Stdin:  strings.NewReader("1\n1\n"), // Option 1: en (English), Option 1: local
			Dir:    testutil.TempDir(t),
		}
		t.Setenv("BFLOW_CONFIG_HOME", testutil.TempDir(t))
		if code := Run([]string{"init", "--host", "none"}, env); code != 0 {
			t.Fatalf("exit %d: %s", code, env.Stdout.(*bytes.Buffer))
		}
		out := stderr.String()
		if !strings.Contains(out, "Which tracker do you use?") {
			t.Errorf("expected English prompt 'Which tracker do you use?', got:\n%s", out)
		}
		if strings.Contains(out, "¿Qué tracker usa?") {
			t.Errorf("did not expect Spanish prompt '¿Qué tracker usa?', got:\n%s", out)
		}
	})

	t.Run("interactive chooses en emits english errors on failure", func(t *testing.T) {
		stderr := &bytes.Buffer{}
		stdout := &bytes.Buffer{}
		env := &Env{
			Stdout: stdout,
			Stderr: stderr,
			Stdin:  strings.NewReader("1\n"), // Option 1: en (English)
			Dir:    testutil.TempDir(t),
		}
		t.Setenv("BFLOW_CONFIG_HOME", testutil.TempDir(t))
		// tracker github without host github fails with incomplete
		if code := Run([]string{"init", "--host", "none", "--tracker", "github"}, env); code == 0 {
			t.Fatalf("expected error, got exit 0")
		}
		out := stdout.String()
		if !strings.Contains(out, "github tracker requires github host") {
			t.Errorf("expected English error 'github tracker requires github host', got:\n%s", out)
		}
		if strings.Contains(out, "el tracker github exige host github") {
			t.Errorf("did not expect Spanish error 'el tracker github exige host github', got:\n%s", out)
		}
	})

	t.Run("system locale detects spanish default", func(t *testing.T) {
		t.Setenv("LANG", "es_ES.UTF-8")
		stderr := &bytes.Buffer{}
		stdout := &bytes.Buffer{}
		env := &Env{
			Stdout: stdout,
			Stderr: stderr,
			Stdin:  strings.NewReader("\n"), // hit enter to pick default
			Dir:    testutil.TempDir(t),
		}
		t.Setenv("BFLOW_CONFIG_HOME", testutil.TempDir(t))
		_ = Run([]string{"init", "--host", "none", "--tracker", "github"}, env)
		out := stdout.String()
		if !strings.Contains(out, "el tracker github exige host github") {
			t.Errorf("expected Spanish error with LANG=es, got:\n%s", out)
		}
	})

	t.Run("already exists error message localized", func(t *testing.T) {
		dir := testutil.TempDir(t)
		if err := os.WriteFile(filepath.Join(dir, "bflow.yaml"), []byte("stack: go\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		// English (default bundle localizer)
		stdoutEn := &bytes.Buffer{}
		envEn := &Env{
			Stdout: stdoutEn,
			Stderr: &bytes.Buffer{},
			Stdin:  strings.NewReader(""),
			Dir:    dir,
		}
		codeEn := Run([]string{"init"}, envEn)
		if codeEn != 2 {
			t.Fatalf("expected exit 2, got %d", codeEn)
		}
		outEn := stdoutEn.String()
		if !strings.Contains(outEn, "already exists (use --force to regenerate, or edit the file)") {
			t.Errorf("expected English exists message, got: %s", outEn)
		}
		if !strings.HasPrefix(outEn, "REJECTED:") {
			t.Errorf("expected REJECTED: prefix in English, got: %s", outEn)
		}

		// Spanish via Env.Loc
		bundle := i18n.DefaultBundle()
		stdoutEs := &bytes.Buffer{}
		envEs := &Env{
			Stdout: stdoutEs,
			Stderr: &bytes.Buffer{},
			Stdin:  strings.NewReader(""),
			Dir:    dir,
			Loc:    bundle.New("es"),
		}
		codeEs := Run([]string{"init"}, envEs)
		if codeEs != 2 {
			t.Fatalf("expected exit 2, got %d", codeEs)
		}
		outEs := stdoutEs.String()
		if !strings.Contains(outEs, "ya existe (usa --force para regenerarlo, o edita el archivo)") {
			t.Errorf("expected Spanish exists message, got: %s", outEs)
		}
		if !strings.HasPrefix(outEs, "RECHAZADO:") {
			t.Errorf("expected RECHAZADO: prefix in Spanish, got: %s", outEs)
		}
	})

	t.Run("doctor output localized by language", func(t *testing.T) {
		bundle := i18n.DefaultBundle()

		// Test English doctor output
		dirEn := testutil.TempDir(t)
		if err := os.WriteFile(filepath.Join(dirEn, "bflow.yaml"), []byte("stack: go\nui:\n  lang: en\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		stdoutEn := &bytes.Buffer{}
		envEn := &Env{
			Stdout: stdoutEn,
			Stderr: &bytes.Buffer{},
			Stdin:  strings.NewReader(""),
			Dir:    dirEn,
			Loc:    bundle.New("en"),
			Build: func(d string) (*engine.Engine, error) {
				cfg, err := config.Load(d)
				if err != nil {
					return nil, err
				}
				e := &engine.Engine{Cfg: cfg, Store: store.Open(cfg.Root)}
				if cfg.UI.Lang != "" {
					e.Loc = bundle.New(cfg.UI.Lang)
				}
				return e, nil
			},
		}
		_ = Run([]string{"doctor"}, envEn)
		outEn := stdoutEn.String()
		if !strings.Contains(outEn, "not a git repo: no branches, PRs, or check") {
			t.Errorf("expected English git check in doctor, got:\n%s", outEn)
		}
		if !strings.Contains(outEn, "no check steps: DONE is accepted without verification") {
			t.Errorf("expected English tools check in doctor, got:\n%s", outEn)
		}

		// Test Spanish doctor output
		dirEs := testutil.TempDir(t)
		if err := os.WriteFile(filepath.Join(dirEs, "bflow.yaml"), []byte("stack: go\nui:\n  lang: es\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		stdoutEs := &bytes.Buffer{}
		envEs := &Env{
			Stdout: stdoutEs,
			Stderr: &bytes.Buffer{},
			Stdin:  strings.NewReader(""),
			Dir:    dirEs,
			Loc:    bundle.New("es"),
			Build: func(d string) (*engine.Engine, error) {
				cfg, err := config.Load(d)
				if err != nil {
					return nil, err
				}
				e := &engine.Engine{Cfg: cfg, Store: store.Open(cfg.Root)}
				if cfg.UI.Lang != "" {
					e.Loc = bundle.New(cfg.UI.Lang)
				}
				return e, nil
			},
		}
		_ = Run([]string{"doctor"}, envEs)
		outEs := stdoutEs.String()
		if !strings.Contains(outEs, "no es un repo git: sin ramas, PR ni check") {
			t.Errorf("expected Spanish git check in doctor, got:\n%s", outEs)
		}
		if !strings.Contains(outEs, "sin pasos de check: DONE se acepta sin verificar") {
			t.Errorf("expected Spanish tools check in doctor, got:\n%s", outEs)
		}
	})
}
