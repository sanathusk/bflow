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
}
