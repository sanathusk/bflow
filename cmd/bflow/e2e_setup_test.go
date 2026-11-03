package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"innobytes.tech/bflow/internal/testutil"
)

// runIn corre bflow con stdin dado y devuelve la salida de texto.
func (r *repo) runIn(stdin string, args ...string) (string, int) {
	r.t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = r.dir
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	}
	return out.String(), code
}

func readFile(t *testing.T, p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInitGoRepoWithYes(t *testing.T) {
	r, _ := gitRepo(t, "")
	os.Remove(filepath.Join(r.dir, "bflow.yaml"))
	if err := os.WriteFile(filepath.Join(r.dir, "go.mod"), []byte("module demo\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := r.ok("init", "--yes")
	y := readFile(t, filepath.Join(r.dir, "bflow.yaml"))
	for _, want := range []string{"stack: go", "go vet ./...", "base_branch: dev", "quick:"} {
		if !strings.Contains(y, want) {
			t.Errorf("falta %q:\n%s", want, y)
		}
	}
	if strings.Contains(y, "adapter:") {
		t.Errorf("tracker local es el default: no se escribe\n%s", y)
	}
	if env.Data["stack"] != "go" {
		t.Errorf("data: %v", env.Data)
	}
	if again := r.run("init", "--yes"); again.exit != 2 || again.Code != "exists" {
		t.Errorf("init no sobrescribe sin --force: %d %s", again.exit, again.Code)
	}
	out, code := r.runIn("", "doctor")
	if code != 0 || !strings.Contains(out, "config") || !strings.Contains(out, "herramientas") {
		t.Errorf("doctor (exit %d):\n%s", code, out)
	}
}

func TestInitAngularInteractiveWithProfile(t *testing.T) {
	r := newRepo(t)
	t.Setenv("BFLOW_NO_KEYRING", "1")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	// Copia de los scripts reales de acme-web (sin depender del repo).
	scripts := []byte(`{"name":"acme-web","scripts":{"start":"ng serve","build":"ng build","test":"ng test","test:e2e":"playwright test","check:architecture":"node scripts/check-architecture.js","check:ui":"node scripts/check-ui-controls.js --scope=select","check:ui:full":"node scripts/check-ui-controls.js --scope=all","check:touch":"node scripts/check-touch.js","check:touch:list":"node scripts/check-touch.js --list","check:all":"npm run check:architecture && npm run check:ui","check:theme":"node scripts/check-theme.js","check:theme:hex":"node scripts/check-theme.js --hex"}}`)
	if err := os.WriteFile(filepath.Join(r.dir, "package.json"), scripts, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.dir, "angular.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, r.dir, "init", "-b", "dev")
	git(t, r.dir, "remote", "add", "origin", "https://github.com/acme/acme-web.git")

	// Perfil compartido con lo que no cambia entre repos de acme.
	r.ok("profile", "add", "acme", "--host", "github", "--base", "dev", "--agent", "claude")
	if list := r.ok("profile", "list"); list.Data["profiles"].([]any)[0] != "acme" {
		t.Errorf("profile list: %v", list.Data)
	}

	// Respuestas por pipe: idioma en (opción 1), perfil acme (opción 2), tracker local (1), usar pasos (s).
	out, code := r.runIn("1\n2\n1\ns\n", "init")
	if code != 0 {
		t.Fatalf("init (exit %d):\n%s", code, out)
	}
	y := readFile(t, filepath.Join(r.dir, "bflow.yaml"))
	for _, want := range []string{"profile: acme", "stack: angular", "npm run check:architecture", "npm run check:ui:full", "npm run check:theme", "npm test -- --watch=false", "npm run build"} {
		if !strings.Contains(y, want) {
			t.Errorf("falta %q:\n%s", want, y)
		}
	}
	for _, not := range []string{"check:ui\n", "check:all", "check:touch:list", "check:theme:hex", "test:e2e", "host: github", "base_branch", "agent:"} {
		if strings.Contains(y, not) {
			t.Errorf("no debería estar %q:\n%s", not, y)
		}
	}
	// La config efectiva combina perfil y repo.
	st := r.ok("status")
	_ = st
	out, code = r.runIn("", "doctor")
	if code != 0 || (!strings.Contains(out, "perfil acme") && !strings.Contains(out, "profile acme")) ||
		(!strings.Contains(out, "github sin token") && !strings.Contains(out, "github without token")) ||
		!strings.Contains(out, "claude") {
		t.Errorf("doctor (exit %d):\n%s", code, out)
	}
	// profile use sobre un repo existente conserva el resto del archivo.
	r.ok("profile", "add", "otro", "--base", "main")
	r.ok("profile", "use", "otro")
	y2 := readFile(t, filepath.Join(r.dir, "bflow.yaml"))
	if !strings.Contains(y2, "profile: otro") || !strings.Contains(y2, "npm run build") || !strings.Contains(y2, "# bflow.yaml") {
		t.Errorf("profile use:\n%s", y2)
	}
}

// Qué abrir al empezar una tarea se pregunta una vez por máquina y va a la
// config global; en CI o sin escritorio no se pregunta.
func TestInitAsksPanelOnce(t *testing.T) {
	r := newRepo(t)
	t.Setenv("CI", "")
	t.Setenv("DISPLAY", ":0") // Linux sin escritorio no pregunta
	t.Setenv("SSH_CONNECTION", "")
	if err := os.WriteFile(filepath.Join(r.dir, "Makefile"), []byte("test:\n\tgo test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// idioma es (2), tracker local (1), rama base por defecto, usar pasos (s), solo el panel (2).
	out, code := r.runIn("2\n1\n\ns\n2\n", "init")
	if code != 0 || !strings.Contains(out, "¿qué abro?") || !strings.Contains(out, "al empezar una tarea: solo el panel") {
		t.Fatalf("init (exit %d):\n%s", code, out)
	}
	g := readFile(t, filepath.Join(os.Getenv("BFLOW_CONFIG_HOME"), "config.yaml"))
	if !strings.Contains(g, "watch: true") || !strings.Contains(g, "web: false") {
		t.Errorf("config global:\n%s", g)
	}
	// Otro repo en la misma máquina: ya no pregunta.
	other := &repo{t: t, dir: testutil.TempDir(t)}
	if err := os.WriteFile(filepath.Join(other.dir, "Makefile"), []byte("test:\n\tgo test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := other.runIn("2\n1\n\ns\n", "init"); code != 0 || strings.Contains(out, "¿qué abro?") {
		t.Errorf("segundo init (exit %d):\n%s", code, out)
	}
}

func TestInitDryRunWritesNothing(t *testing.T) {
	r := newRepo(t)
	if err := os.WriteFile(filepath.Join(r.dir, "Makefile"), []byte("test:\n\tgo test\nbuild:\n\tgo build\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := r.ok("init", "--yes", "--dry-run")
	if !strings.Contains(env.Data["yaml"].(string), "make test") {
		t.Errorf("plan: %v", env.Data["yaml"])
	}
	if _, err := os.Stat(filepath.Join(r.dir, "bflow.yaml")); !os.IsNotExist(err) {
		t.Error("--dry-run no escribe")
	}
	_ = testutil.TempDir
}
