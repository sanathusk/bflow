package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"innobytes.tech/bflow/internal/testutil"
	"innobytes.tech/bflow/internal/tracker"
)

func initEnv(t *testing.T, stdin string, projects []tracker.Project, projErr error) (*Env, *[]string) {
	t.Helper()
	t.Setenv("BFLOW_CONFIG_HOME", testutil.TempDir(t))
	var asked []string
	return &Env{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Stdin: strings.NewReader(stdin), Dir: testutil.TempDir(t),
		Projects: func(_ context.Context, service, url, workspace string) ([]tracker.Project, error) {
			asked = append(asked, service+" "+workspace)
			return projects, projErr
		}}, &asked
}

func initYAML(t *testing.T, env *Env) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(env.Dir, "bflow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInitGithubTracker(t *testing.T) {
	t.Run("--project escribe adapter y project sin preguntar", func(t *testing.T) {
		env, asked := initEnv(t, "", nil, nil)
		if code := Run([]string{"init", "--yes", "--host", "github", "--tracker", "github", "--project", "acme/7"}, env); code != 0 {
			t.Fatalf("exit %d: %s", code, env.Stdout.(*bytes.Buffer))
		}
		y := initYAML(t, env)
		for _, w := range []string{"adapter: github", "project: acme/7", "host: github"} {
			if !strings.Contains(y, w) {
				t.Errorf("falta %q:\n%s", w, y)
			}
		}
		if len(*asked) != 0 {
			t.Errorf("con --project no se listan los Projects: %v", *asked)
		}
		out := env.Stdout.(*bytes.Buffer).String()
		for _, w := range []string{"bflow connect github", "bflow tracker setup --dry-run"} {
			if !strings.Contains(out, w) {
				t.Errorf("los siguientes pasos no dicen %q:\n%s", w, out)
			}
		}
	})
	t.Run("elige un Project de la lista", func(t *testing.T) {
		env, asked := initEnv(t, "1\n3\n", []tracker.Project{{ID: "acme/7", Name: "Uno"}, {ID: "acme/9", Name: "Dos"}}, nil)
		if code := Run([]string{"init", "--host", "github", "--tracker", "github"}, env); code != 0 {
			t.Fatalf("exit %d: %s", code, env.Stdout.(*bytes.Buffer))
		}
		if y := initYAML(t, env); !strings.Contains(y, "project: acme/9") {
			t.Errorf("eligió el segundo Project:\n%s", y)
		}
		if len(*asked) != 1 || !strings.HasPrefix((*asked)[0], "github ") {
			t.Errorf("lista los Projects de github: %v", *asked)
		}
	})
	t.Run("sin project es válido", func(t *testing.T) {
		env, _ := initEnv(t, "1\n1\n", []tracker.Project{{ID: "acme/7", Name: "Uno"}}, nil)
		if code := Run([]string{"init", "--host", "github", "--tracker", "github"}, env); code != 0 {
			t.Fatalf("exit %d: %s", code, env.Stdout.(*bytes.Buffer))
		}
		if y := initYAML(t, env); !strings.Contains(y, "adapter: github") || strings.Contains(y, "project:") {
			t.Errorf("github sin project:\n%s", y)
		}
	})
	t.Run("no se puede listar: sigue sin project", func(t *testing.T) {
		env, _ := initEnv(t, "", nil, errors.New("sin token"))
		if code := Run([]string{"init", "--yes", "--host", "github", "--tracker", "github"}, env); code != 0 {
			t.Fatalf("exit %d", code)
		}
		if y := initYAML(t, env); !strings.Contains(y, "adapter: github") {
			t.Errorf("adapter:\n%s", y)
		}
	})
	t.Run("exige host github", func(t *testing.T) {
		env, _ := initEnv(t, "", nil, nil)
		if code := Run([]string{"init", "--yes", "--host", "none", "--tracker", "github"}, env); code == 0 {
			t.Error("tracker github sin host github debe fallar")
		}
		if _, err := os.Stat(filepath.Join(env.Dir, "bflow.yaml")); err == nil {
			t.Error("no debe escribir bflow.yaml")
		}
	})
}
