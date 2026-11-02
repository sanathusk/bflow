package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"innobytes.tech/bflow/internal/testutil"
)

// La página del panel muestra todos los repos donde se usó bflow, agrupados
// por perfil, y el detalle del que se pida.
func TestUINetwork(t *testing.T) {
	a := newRepo(t)
	b := &repo{t: t, dir: testutil.TempDir(t)}
	if err := os.WriteFile(filepath.Join(os.Getenv("BFLOW_CONFIG_HOME"), "config.yaml"), []byte("profiles:\n  acme:\n    agent: claude\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.dir, "bflow.yaml"), []byte("profile: acme\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.dir, "bflow.yaml"), []byte("stack: go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	idA := a.ok("task", "add", "Alta de clientes").Data["id"].(string)
	a.ok("start", idA, "--lane", "light")
	idB := b.ok("task", "add", "Servidor no arranca").Data["id"].(string)
	b.ok("start", idB, "--lane", "hotfix")

	cmd := exec.Command(binary, "ui", "--no-open", "--port", "0")
	cmd.Dir = a.dir
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill() }()
	line, _ := bufio.NewReader(out).ReadString('\n')
	line = strings.TrimPrefix(line, "bflow ui at ")
	line = strings.TrimPrefix(line, "bflow ui en ")
	url := strings.Fields(line)[0]

	type row struct {
		Key  string `json:"key"`
		Name string `json:"name"`
		Task *struct {
			ID    string `json:"id"`
			Phase string `json:"phase"`
		} `json:"task"`
	}
	var st struct {
		Repo    string `json:"repo"`
		Profile string `json:"profile"`
		Task    *struct {
			ID string `json:"id"`
		} `json:"task"`
		Network []struct {
			Profile string `json:"profile"`
			Repos   []row  `json:"repos"`
		} `json:"network"`
	}
	get := func(path string) {
		t.Helper()
		resp, err := http.Get(url + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		st.Task = nil
		if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
			t.Fatal(err)
		}
	}
	get("api/state")
	if st.Repo != filepath.Base(a.dir) || st.Profile != "acme" || st.Task == nil || st.Task.ID != idA {
		t.Fatalf("por defecto, el repo desde donde se abrió: %+v", st)
	}
	n := st.Network
	if len(n) != 2 || n[0].Profile != "acme" || n[1].Profile != "" || n[1].Repos[0].Task == nil ||
		n[1].Repos[0].Task.ID != idB || n[1].Repos[0].Task.Phase != "implementing" {
		t.Fatalf("red por perfil, sin perfil al final: %+v", n)
	}
	get("api/state?repo=" + n[1].Repos[0].Key)
	if st.Repo != filepath.Base(b.dir) || st.Task == nil || st.Task.ID != idB {
		t.Errorf("el repo pedido: %+v", st)
	}
}
