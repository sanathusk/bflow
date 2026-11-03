package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
}
