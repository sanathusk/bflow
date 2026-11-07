package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"innobytes.tech/bflow/internal/output"
)

func TestCompactNext(t *testing.T) {
	n := output.Next{Action: output.ActionAsk, Gate: "spec", Display: "mucho texto", Question: "¿a < b & c?",
		Options: []output.Option{{ID: "approve", Label: "Aprobar", Command: "bflow approve X"}}, Clear: true}
	got := compactNext(n)
	if got == "" || strings.Contains(got, "\n") {
		t.Fatalf("una línea no vacía: %q", got)
	}
	if strings.Contains(got, "display") || strings.Contains(got, "mucho texto") {
		t.Errorf("sin display: %s", got)
	}
	if !strings.Contains(got, `"action":"ask"`) || !strings.Contains(got, `"gate":"spec"`) || !strings.Contains(got, `"options"`) {
		t.Errorf("conserva action, gate y options: %s", got)
	}
	if !strings.Contains(got, "a < b & c") {
		t.Errorf("no escapa HTML: %s", got)
	}
	if n.Display == "" {
		t.Error("no muta el original")
	}
}

// claudeLoop es la entrada de un hook PreToolUse de Claude Code de un subagente,
// con sesión fija para que el guard pueda contar sus repeticiones.
func claudeLoop(env *Env, cmd, agent, session string) string {
	m := map[string]any{"hook_event_name": "PreToolUse", "cwd": env.Dir, "tool_name": "Bash",
		"tool_input": map[string]any{"command": cmd}, "agent_type": agent, "session_id": session}
	b, _ := json.Marshal(m)
	return string(b)
}

// R17, R18: la N-ésima repetición idéntica de un subagente en la misma corrida se rechaza.
func TestGuardLoopDeniesSubagent(t *testing.T) {
	setHome(t)
	env := toolsEnv(t, "agent: [claude, opencode]\nflow: { loop_repeats: 3 }\n")
	in := claudeLoop(env, "go test ./...", "implementer", "ses-loop-1")
	for i := 0; i < 2; i++ {
		if code, _, errOut := hookRun(t, env, in, "guard"); code != 0 {
			t.Fatalf("repetición %d: debía permitirse: %d %q", i+1, code, errOut)
		}
	}
	code, _, errOut := hookRun(t, env, in, "guard")
	if code != 2 || (!strings.Contains(errOut, "ciclo") && !strings.Contains(errOut, "loop")) {
		t.Fatalf("3ra repetición idéntica debía rechazarse: %d %q", code, errOut)
	}
}

// R18: la sesión principal (sin agent_type) no cuenta para el ciclo.
func TestGuardLoopIgnoresMain(t *testing.T) {
	setHome(t)
	env := toolsEnv(t, "agent: [claude, opencode]\nflow: { loop_repeats: 2 }\n")
	in := claudeLoop(env, "go test ./...", "", "ses-main-1")
	for i := 0; i < 4; i++ {
		if code, _, errOut := hookRun(t, env, in, "guard"); code != 0 {
			t.Fatalf("repetición %d de la sesión principal no debía rechazarse: %d %q", i+1, code, errOut)
		}
	}
}

// R16, R21: sin flow.loop_repeats (o 0) el guard no detecta ciclos.
func TestGuardLoopOff(t *testing.T) {
	setHome(t)
	env := toolsEnv(t, "agent: [claude, opencode]\n")
	in := claudeLoop(env, "go test ./...", "implementer", "ses-off-1")
	for i := 0; i < 5; i++ {
		if code, _, errOut := hookRun(t, env, in, "guard"); code != 0 {
			t.Fatalf("repetición %d: sin loop_repeats no debía rechazarse: %d %q", i+1, code, errOut)
		}
	}
}
