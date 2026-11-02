package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"innobytes.tech/bflow/internal/release"
	"innobytes.tech/bflow/internal/testutil"
)

// rel es una release que sirve releaseServerOf.
type rel struct {
	Tag               string
	Prerelease, Draft bool
}

// releaseServer publica v9.9.9 con un "binario" para esta plataforma. Con
// badSum, checksums.txt no coincide con la descarga.
func releaseServer(t *testing.T, badSum bool) *httptest.Server {
	return releaseServerOf(t, badSum, rel{Tag: "v9.9.9"})
}

// releaseServerOf publica las releases en el orden dado (como la lista de
// GitHub). /releases/latest responde la primera que no es prerelease ni
// borrador, o 404. El binario de cada una contiene "bflow <tag>" (v9.9.9:
// "bflow nuevo").
func releaseServerOf(t *testing.T, badSum bool, rels ...rel) *httptest.Server {
	dir := testutil.TempDir(t)
	type built struct {
		name    string
		archive []byte
		sum     string
	}
	files := map[string]built{}
	for _, r := range rels {
		src := filepath.Join(dir, "src-"+r.Tag)
		body := "bflow " + r.Tag
		if r.Tag == "v9.9.9" {
			body = "bflow nuevo"
		}
		if err := os.WriteFile(src, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		name := release.AssetName(r.Tag, runtime.GOOS, runtime.GOARCH)
		if err := release.Archive(filepath.Join(dir, name), src, runtime.GOOS); err != nil {
			t.Fatal(err)
		}
		archive, _ := os.ReadFile(filepath.Join(dir, name))
		sum := release.SHA256(archive)
		if badSum {
			sum = strings.Repeat("0", 64)
		}
		files[r.Tag] = built{name, archive, sum}
	}
	var srv *httptest.Server
	obj := func(r rel) map[string]any {
		f := files[r.Tag]
		return map[string]any{"tag_name": r.Tag, "prerelease": r.Prerelease, "draft": r.Draft, "assets": []map[string]string{
			{"name": f.name, "browser_download_url": srv.URL + "/dl/" + r.Tag + "/" + f.name},
			{"name": release.Checksums, "browser_download_url": srv.URL + "/dl/" + r.Tag + "/" + release.Checksums},
		}}
	}
	srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		p := req.URL.Path
		switch {
		case p == "/releases/latest":
			for _, r := range rels {
				if !r.Prerelease && !r.Draft {
					_ = json.NewEncoder(w).Encode(obj(r))
					return
				}
			}
			http.NotFound(w, req)
		case p == "/releases":
			list := []map[string]any{}
			for _, r := range rels {
				list = append(list, obj(r))
			}
			_ = json.NewEncoder(w).Encode(list)
		case strings.HasPrefix(p, "/releases/tags/"):
			for _, r := range rels {
				if !r.Draft && r.Tag == strings.TrimPrefix(p, "/releases/tags/") {
					_ = json.NewEncoder(w).Encode(obj(r))
					return
				}
			}
			http.NotFound(w, req)
		case strings.HasPrefix(p, "/dl/"):
			tag, file, _ := strings.Cut(strings.TrimPrefix(p, "/dl/"), "/")
			f, ok := files[tag]
			switch {
			case ok && file == f.name:
				_, _ = w.Write(f.archive)
			case ok && file == release.Checksums:
				fmt.Fprintf(w, "%s  %s\n", f.sum, f.name)
			default:
				http.NotFound(w, req)
			}
		default:
			http.NotFound(w, req)
		}
	}))
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// buildAs compila bflow con la versión fijada. El binario de las demás
// pruebas toma la versión de git (en un checkout con tag es ese tag), así que
// no sirve para probar la comparación de versiones.
func buildAs(t *testing.T, args ...string) string {
	t.Helper()
	exe := filepath.Join(testutil.TempDir(t), filepath.Base(binary))
	cmd := exec.Command("go", append(append([]string{"build"}, args...), "-o", exe, ".")...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return exe
}

// runCopy corre una copia de src (update la reemplaza).
func runCopy(t *testing.T, src, api string, args ...string) (exe string, out string, code int) {
	exe = filepath.Join(testutil.TempDir(t), filepath.Base(src))
	b, _ := os.ReadFile(src)
	if err := os.WriteFile(exe, b, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), "BFLOW_RELEASES_URL="+api)
	o, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	}
	return exe, string(o), code
}

func TestUpdate(t *testing.T) {
	srv := releaseServer(t, false)
	old := buildAs(t, "-ldflags", "-X main.version=v0.0.1")
	if _, out, code := runCopy(t, old, srv.URL, "update", "--check"); code != 0 || (!strings.Contains(out, "hay una versión nueva: v9.9.9 (tienes v0.0.1)") && !strings.Contains(out, "there is a new version: v9.9.9 (you have v0.0.1)")) {
		t.Errorf("--check: %d %s", code, out)
	}
	exe, out, code := runCopy(t, old, srv.URL, "update")
	if code != 0 || !strings.Contains(out, "v0.0.1 → v9.9.9") {
		t.Fatalf("update: %d %s", code, out)
	}
	if b, _ := os.ReadFile(exe); string(b) != "bflow nuevo" {
		t.Errorf("el binario no se reemplazó: %q", b)
	}

	// Sin versión publicada (compilado sin datos de git) no se compara: pide --force.
	dev := buildAs(t, "-buildvcs=false")
	if _, out, code := runCopy(t, dev, srv.URL, "update"); code != 0 || (!strings.Contains(out, "no viene de una versión publicada") && !strings.Contains(out, "does not come from a published release")) {
		t.Errorf("dev: %d %s", code, out)
	}
	if exe, out, code := runCopy(t, dev, srv.URL, "update", "--force"); code != 0 || !strings.Contains(out, "→ v9.9.9") {
		t.Errorf("dev --force: %d %s", code, out)
	} else if b, _ := os.ReadFile(exe); string(b) != "bflow nuevo" {
		t.Errorf("--force no reemplazó: %q", b)
	}

	// Si la descarga no coincide con checksums.txt, no se toca nada.
	bad := releaseServer(t, true)
	exe, out, code = runCopy(t, old, bad.URL, "update")
	if code == 0 || !strings.Contains(out, "no coincide con checksums.txt") {
		t.Errorf("checksum malo: %d %s", code, out)
	}
	if b, _ := os.ReadFile(exe); string(b) == "bflow nuevo" {
		t.Error("con checksum malo no se instala")
	}
}

func TestUpdatePrerelease(t *testing.T) {
	pre := func(tag string) rel { return rel{Tag: tag, Prerelease: true} }
	cases := []struct {
		name, version, wantEs, wantEn string
		rels                          []rel
	}{
		{"solo prereleases, binario rc.8", "v0.1.0-rc.8", "hay una versión nueva: v0.1.0-rc.9 (tienes v0.1.0-rc.8)", "there is a new version: v0.1.0-rc.9 (you have v0.1.0-rc.8)", []rel{pre("v0.1.0-rc.9")}},
		{"rc.10 supera a rc.9", "v0.1.0-rc.9", "hay una versión nueva: v0.1.0-rc.10 (tienes v0.1.0-rc.9)", "there is a new version: v0.1.0-rc.10 (you have v0.1.0-rc.9)", []rel{pre("v0.1.0-rc.9"), pre("v0.1.0-rc.10")}},
		{"rc.9 es la última", "v0.1.0-rc.9", "ya tienes la última versión", "you already have the latest version", []rel{pre("v0.1.0-rc.9"), {Tag: "v0.1.0-rc.10", Prerelease: true, Draft: true}}},
		{"estable sin estable publicada no recibe prereleases", "v0.1.0", "ya tienes la última versión (v0.1.0)", "you already have the latest version (v0.1.0)", []rel{pre("v0.2.0-rc.1")}},
		{"estable no recibe prereleases", "v0.1.0", "ya tienes la última versión (v0.1.0)", "you already have the latest version (v0.1.0)", []rel{pre("v0.2.0-rc.1"), {Tag: "v0.1.0"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := releaseServerOf(t, false, c.rels...)
			exe := buildAs(t, "-ldflags", "-X main.version="+c.version)
			if _, out, code := runCopy(t, exe, srv.URL, "update", "--check"); code != 0 || (!strings.Contains(out, c.wantEs) && !strings.Contains(out, c.wantEn)) {
				t.Errorf("%d %s", code, out)
			}
		})
	}
	t.Run("binario sin versión publicada con solo prereleases", func(t *testing.T) {
		srv := releaseServerOf(t, false, pre("v0.1.0-rc.9"))
		dev := buildAs(t, "-buildvcs=false")
		if _, out, code := runCopy(t, dev, srv.URL, "update"); code != 0 || (!strings.Contains(out, "no viene de una versión publicada") && !strings.Contains(out, "does not come from a published release")) || !strings.Contains(out, "v0.1.0-rc.9") {
			t.Errorf("%d %s", code, out)
		}
	})
}
