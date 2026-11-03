package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"innobytes.tech/bflow/internal/config"
)

// GH-96: el mapeo personal de modelos (config global) no entra a los agentes
// versionados; va a agent.bflow-*.model del opencode.json del usuario.

const repoOC = "agent: [claude, opencode]\nmodels:\n  opencode: { sonnet: r/sonnet }\n"

// personalEnv aísla HOME, XDG_CONFIG_HOME y la config global; devuelve el
// repo y <xdg>/opencode/opencode.json.
func personalEnv(t *testing.T, repoYAML, global string) (*Env, string) {
	t.Helper()
	setHome(t)
	xdg, _ := setXDG(t)
	env := toolsEnv(t, repoYAML)
	if global != "" {
		if err := os.MkdirAll(config.GlobalDir(), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(config.GlobalPath(), []byte(global), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return env, filepath.Join(xdg, "opencode", "opencode.json")
}

func setGlobal(t *testing.T, global string) {
	t.Helper()
	if err := os.WriteFile(config.GlobalPath(), []byte(global), 0o644); err != nil {
		t.Fatal(err)
	}
}

// userModels lee agent.<n>.model de opencode.json: subagente → modelo.
func userModels(t *testing.T, path string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("falta %s: %v", path, err)
	}
	var m struct {
		Agent map[string]struct {
			Model string `json:"model"`
		} `json:"agent"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v\n%s", path, err, b)
	}
	out := map[string]string{}
	for n, a := range m.Agent {
		out[n] = a.Model
	}
	return out
}

func notExist(path string) bool {
	_, err := os.Stat(path)
	return os.IsNotExist(err)
}

const globalHaiku = "models:\n  opencode: { haiku: g/haiku, sonnet: g/sonnet, opus: g/opus }\n"

// R4, R5: los agentes versionados solo usan los models de bflow.yaml.
func TestRenderIgnoresGlobalModels(t *testing.T) {
	env, _ := personalEnv(t, repoOC, globalHaiku)
	if code, _, raw := runJSON(t, env, "render"); code != 0 {
		t.Fatalf("render: %s", raw)
	}
	if imp := rendered(t, env, ".opencode/agents/bflow-implementer.md"); !strings.Contains(imp, "model: r/sonnet") {
		t.Errorf("sonnet sale de bflow.yaml, no de la global:\n%s", imp)
	}
	if doc := rendered(t, env, ".opencode/agents/bflow-documenter.md"); strings.Contains(doc, "model:") {
		t.Errorf("haiku solo está en la global: el agente versionado sale sin model (R4):\n%s", doc)
	}
	for _, f := range []string{"implementer", "reviewer", "documenter", "scout", "spec-author"} {
		if strings.Contains(rendered(t, env, ".opencode/agents/bflow-"+f+".md"), "g/") {
			t.Errorf("bflow-%s: la config global no debe llegar a lo versionado", f)
		}
	}
	// render --check no depende de la global.
	for _, g := range []string{globalHaiku, "", "models:\n  opencode: { haiku: otro/h, sonnet: otro/s }\n"} {
		if g == "" {
			_ = os.Remove(config.GlobalPath())
		} else {
			setGlobal(t, g)
		}
		if code, _, raw := runJSON(t, env, "render", "--check"); code != 0 {
			t.Errorf("render --check no depende de la global (%q): %s", g, raw)
		}
	}
}

// R6, R8, R10: overrides personales para los alias que el repo no define.
func TestRenderSyncsPersonalModels(t *testing.T) {
	env, oc := personalEnv(t, repoOC, globalHaiku)
	env.Stdout = &strings.Builder{}
	code, out, raw := runJSON(t, env, "render")
	if code != 0 {
		t.Fatalf("render: %s", raw)
	}
	got := userModels(t, oc)
	if len(got) != 4 || got["bflow-documenter"] != "g/haiku" || got["bflow-scout"] != "g/haiku" || got["bflow-reviewer"] != "g/opus" || got["bflow-spec-author"] != "g/opus" {
		t.Errorf("solo los agentes de haiku (el repo define sonnet) y de opus (reviewer y spec-author): %v", got)
	}
	pm, _ := dataOf(out)["personal_models"].(map[string]any)
	if pm == nil || pm["path"] != oc || pm["changed"] != true || pm["error"] != nil && pm["error"] != "" {
		t.Errorf("data.personal_models: %v", dataOf(out)["personal_models"])
	}
	if un := dataOf(out)["unresolved"].([]any); len(un) != 0 {
		t.Errorf("haiku lo cubre el mapeo personal y sonnet el repo: %v", un)
	}

	// Segunda vez: nada que cambiar, no se escribe.
	before, _ := os.ReadFile(oc)
	st1, _ := os.Stat(oc)
	_, out, raw = runJSON(t, env, "render")
	pm, _ = dataOf(out)["personal_models"].(map[string]any)
	if pm == nil || pm["changed"] != false {
		t.Errorf("sin cambios: %s", raw)
	}
	after, _ := os.ReadFile(oc)
	st2, _ := os.Stat(oc)
	if string(before) != string(after) || !st1.ModTime().Equal(st2.ModTime()) {
		t.Error("si nada cambia, opencode.json no se reescribe")
	}

	// Un alias sin valor ni en el repo ni en la global sigue sin resolver.
	setGlobal(t, "models:\n  opencode: { haiku: g/haiku }\n")
	_ = os.WriteFile(filepath.Join(env.Dir, "bflow.yaml"), []byte("agent: [claude, opencode]\n"), 0o644)
	_, out, raw = runJSON(t, env, "render")
	b, _ := json.Marshal(dataOf(out)["unresolved"])
	if !strings.Contains(string(b), "sonnet") || strings.Contains(string(b), "haiku") {
		t.Errorf("solo sonnet queda sin resolver (R10): %s", raw)
	}

	// Cambiar el valor personal actualiza el override; quitarlo lo borra.
	setGlobal(t, "models:\n  opencode: { haiku: g/nuevo }\n")
	runJSON(t, env, "render")
	if got := userModels(t, oc); got["bflow-documenter"] != "g/nuevo" {
		t.Errorf("se actualiza: %v", got)
	}
	_ = os.Remove(config.GlobalPath())
	runJSON(t, env, "render")
	if got := userModels(t, oc); len(got) != 0 {
		t.Errorf("sin mapeo personal se quitan los overrides de bflow: %v", got)
	}

	// Texto: avisa dónde quedó.
	setGlobal(t, globalHaiku)
	_ = os.WriteFile(filepath.Join(env.Dir, "bflow.yaml"), []byte(repoOC), 0o644)
	_ = os.Remove(oc)
	sb := &strings.Builder{}
	env.Stdout = sb
	Run([]string{"render"}, env)
	if !strings.Contains(sb.String(), "modelos personales") || !strings.Contains(sb.String(), oc) {
		t.Errorf("el texto dice dónde quedaron los modelos personales: %q", sb.String())
	}
}

// R9: .jsonc no se edita: se devuelve el bloque a pegar.
func TestRenderPersonalModelsJSONC(t *testing.T) {
	env, oc := personalEnv(t, repoOC, globalHaiku)
	jsonc := filepath.Join(filepath.Dir(oc), "opencode.jsonc")
	_ = os.MkdirAll(filepath.Dir(oc), 0o755)
	_ = os.WriteFile(jsonc, []byte("{ // mío\n}\n"), 0o644)
	code, out, raw := runJSON(t, env, "render")
	if code != 0 {
		t.Fatalf("render: %s", raw)
	}
	pm, _ := dataOf(out)["personal_models"].(map[string]any)
	paste, _ := pm["paste"].(string)
	if pm == nil || !strings.Contains(paste, `"bflow-documenter"`) || !strings.Contains(paste, "g/haiku") || pm["changed"] == true {
		t.Errorf("data.personal_models.paste: %v", pm)
	}
	if !notExist(oc) {
		t.Error("con .jsonc no se crea opencode.json")
	}
	if b, _ := os.ReadFile(jsonc); string(b) != "{ // mío\n}\n" {
		t.Error("el .jsonc no se toca")
	}
}

// R13: un error de E/S al sincronizar no rompe render.
func TestRenderPersonalModelsIOError(t *testing.T) {
	env, oc := personalEnv(t, repoOC, globalHaiku)
	if err := os.WriteFile(filepath.Dir(oc), []byte("soy un archivo"), 0o644); err != nil { // <xdg>/opencode no es carpeta
		t.Fatal(err)
	}
	sb := &strings.Builder{}
	code, out, raw := runJSON(t, env, "render")
	if code != 0 || out["ok"] != true {
		t.Fatalf("un fallo al escribir el mapeo personal no es un fallo de render: %s", raw)
	}
	pm, _ := dataOf(out)["personal_models"].(map[string]any)
	if e, _ := pm["error"].(string); e == "" {
		t.Errorf("data.personal_models.error: %v", pm)
	}
	if !exists(env, ".opencode/agents/bflow-implementer.md") {
		t.Error("los agentes se escriben igual")
	}
	env.Stdout = sb
	if Run([]string{"render"}, env); !strings.Contains(sb.String(), "aviso") {
		t.Errorf("el texto avisa del error: %q", sb.String())
	}
}

// R5: render --check no lee ni escribe el opencode.json del usuario.
func TestRenderCheckNoUserConfig(t *testing.T) {
	env, oc := personalEnv(t, repoOC, globalHaiku)
	if code, _, raw := runJSON(t, env, "render"); code != 0 {
		t.Fatalf("render: %s", raw)
	}
	if err := os.Remove(oc); err != nil {
		t.Fatal(err)
	}
	code, out, raw := runJSON(t, env, "render", "--check")
	if code != 0 {
		t.Fatalf("render --check: %s", raw)
	}
	if !notExist(oc) {
		t.Error("render --check no crea opencode.json")
	}
	if v, present := dataOf(out)["personal_models"]; present && v != nil {
		t.Errorf("con --check data.personal_models es null: %v", v)
	}
	// Un opencode.json ilegible ni se lee ni importa.
	_ = os.MkdirAll(filepath.Dir(oc), 0o755)
	_ = os.WriteFile(oc, []byte("{ roto"), 0o644)
	if code, _, raw := runJSON(t, env, "render", "--check"); code != 0 {
		t.Errorf("el check no depende del opencode.json: %s", raw)
	}
	if b, _ := os.ReadFile(oc); string(b) != "{ roto" {
		t.Error("render --check no toca el archivo")
	}
}

// R12: con conflictos, o sin opencode en agent:, no se toca opencode.json.
func TestRenderConflictNoSync(t *testing.T) {
	env, oc := personalEnv(t, repoOC, globalHaiku)
	handmade := filepath.Join(env.Dir, ".opencode", "agents", "bflow-implementer.md")
	_ = os.MkdirAll(filepath.Dir(handmade), 0o755)
	_ = os.WriteFile(handmade, []byte("mío\n"), 0o644)
	code, out, raw := runJSON(t, env, "render")
	if code != 1 || out["code"] != "render_conflict" {
		t.Fatalf("conflicto: %s", raw)
	}
	if !notExist(oc) {
		t.Error("con conflictos no se escribe opencode.json")
	}
	_ = os.Remove(handmade)
	if code, _, raw := runJSON(t, env, "render"); code != 0 || notExist(oc) {
		t.Errorf("resuelto el conflicto, render sí sincroniza: %s", raw)
	}

	env2, oc2 := personalEnv(t, "agent: claude\nmodels:\n  opencode: { sonnet: r/s }\n", globalHaiku)
	if code, _, raw := runJSON(t, env2, "render"); code != 0 {
		t.Fatalf("render: %s", raw)
	}
	if !notExist(oc2) {
		t.Error("sin opencode en agent: no se toca opencode.json")
	}
	_ = os.WriteFile(filepath.Join(env2.Dir, "bflow.yaml"), []byte("agent: [claude, opencode]\nmodels:\n  opencode: { sonnet: r/s }\n"), 0o644)
	if code, _, raw := runJSON(t, env2, "render"); code != 0 || notExist(oc2) {
		t.Errorf("con opencode en agent: sí sincroniza: %s", raw)
	}
}

// R6: install opencode también deja los overrides.
func TestInstallOpenCodeSyncsModels(t *testing.T) {
	env, oc := personalEnv(t, repoOC, globalHaiku)
	code, out, raw := runJSON(t, env, "install", "opencode")
	if code != 0 || out["code"] != "installed" {
		t.Fatalf("install opencode: %d %s", code, raw)
	}
	got := userModels(t, oc)
	if len(got) != 4 || got["bflow-documenter"] != "g/haiku" || got["bflow-scout"] != "g/haiku" || got["bflow-reviewer"] != "g/opus" || got["bflow-spec-author"] != "g/opus" {
		t.Errorf("install deja los overrides personales: %v", got)
	}
	if pm, _ := dataOf(out)["personal_models"].(map[string]any); pm == nil || pm["path"] != oc {
		t.Errorf("data.personal_models: %v", dataOf(out)["personal_models"])
	}
	if strings.Contains(rendered(t, env, ".opencode/agents/bflow-documenter.md"), "g/haiku") {
		t.Error("y lo versionado sigue sin el mapeo personal")
	}
}

func modelosItems(t *testing.T, env *Env) []map[string]any {
	t.Helper()
	return doctorChecks(t, env)["modelos"]
}

func anyDetail(items []map[string]any, status string, wants ...string) bool {
next:
	for _, it := range items {
		d, _ := it["detail"].(string)
		if it["status"] != status {
			continue
		}
		for _, w := range wants {
			if !strings.Contains(d, w) {
				continue next
			}
		}
		return true
	}
	return false
}

// R10, R11, R2: doctor.
func TestDoctorOpenCodeModels(t *testing.T) {
	env, oc := personalEnv(t, "agent: [claude, opencode]\n", "models:\n  opencode: { haiku: g/haiku }\n")

	// R10: solo cuenta lo que no cubren ni el repo ni el mapeo personal.
	items := modelosItems(t, env)
	if !anyDetail(items, "warn", "sonnet", "bflow-implementer", config.GlobalPath(), "bflow.yaml") {
		t.Fatalf("sonnet sigue sin resolver: %v", items)
	}
	for _, it := range items {
		if d := it["detail"].(string); strings.Contains(d, "bflow-documenter") {
			t.Errorf("haiku lo cubre el mapeo personal: %q", d)
		}
	}

	// R11: opencode.json no tiene los overrides todavía.
	if !anyDetail(items, "warn", "bflow render") {
		t.Errorf("sin sincronizar, doctor sugiere bflow render: %v", items)
	}
	if code, _, raw := runJSON(t, env, "render"); code != 0 {
		t.Fatalf("render: %s", raw)
	}
	if anyDetail(modelosItems(t, env), "warn", "bflow render") {
		t.Error("con opencode.json al día no hay aviso de sincronización")
	}

	// R11: lo que no se puede editar.
	_ = os.WriteFile(oc, []byte("{ roto"), 0o644)
	if !anyDetail(modelosItems(t, env), "warn", "opencode.json", "bflow render") {
		t.Errorf("opencode.json ilegible: %v", modelosItems(t, env))
	}
	_ = os.Remove(oc)
	jsonc := filepath.Join(filepath.Dir(oc), "opencode.jsonc")
	_ = os.WriteFile(jsonc, []byte("{}"), 0o644)
	if !anyDetail(modelosItems(t, env), "warn", "opencode.jsonc") {
		t.Errorf("solo .jsonc: %v", modelosItems(t, env))
	}
	_ = os.Remove(jsonc)

	// R2: los models de un perfil se ignoran y doctor lo dice, sin fallar.
	prof := "profiles:\n  p:\n    models:\n      opencode: { sonnet: p/sonnet }\n"
	setGlobal(t, prof)
	_ = os.WriteFile(filepath.Join(env.Dir, "bflow.yaml"), []byte("agent: [claude, opencode]\nprofile: p\n"), 0o644)
	items = modelosItems(t, env)
	if !anyDetail(items, "warn", "p") || (!anyDetail(items, "warn", "perfil") && !anyDetail(items, "warn", "profile")) {
		t.Errorf("avisa que los models del perfil no se aplican: %v", items)
	}
	if !anyDetail(items, "warn", "sonnet") {
		t.Errorf("y sonnet sigue sin resolver: %v", items)
	}
}

// C3: con un rechazo de región vigente, doctor no da modelos ok.
func TestDoctorOpenCodeModelosRegion(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	env, oc := personalEnv(t, "agent: [claude, opencode]\n", "models:\n  opencode: { haiku: opencode-go/deepseek-v4.1-flash, sonnet: opencode-go/deepseek-v4-pro, opus: opencode-go/deepseek-v4-pro }\n")
	if code, _, raw := runJSON(t, env, "render"); code != 0 {
		t.Fatalf("render: %s", raw)
	}
	old := time.Now().Add(-3 * time.Hour)
	_ = os.Chtimes(oc, old, old)
	if !anyDetail(modelosItems(t, env), "ok", "resueltos") && !anyDetail(modelosItems(t, env), "ok", "resolved") {
		t.Fatalf("sin rechazo, modelos ok: %v", modelosItems(t, env))
	}
	dir := filepath.Join(data, "opencode", "log")
	_ = os.MkdirAll(dir, 0o755)
	ts := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	line := "timestamp=" + ts + ` level=ERROR message="x" modelID=deepseek-v4-pro error="This Go model requires Global regions."` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "opencode.log"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	items := modelosItems(t, env)
	if anyDetail(items, "ok", "resueltos") || anyDetail(items, "ok", "resolved") {
		t.Errorf("con rechazo vigente no hay modelos ok: %v", items)
	}
	if !anyDetail(items, "warn", "agentes con un modelo rechazado", "proveedor") {
		t.Errorf("falta modelos warn: %v", items)
	}
}
