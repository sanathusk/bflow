package guard

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"innobytes.tech/bflow/internal/flow"
	"innobytes.tech/bflow/internal/i18n"
	"innobytes.tech/bflow/internal/testutil"
)

func ctx() Context {
	return Context{
		Protected:      []string{"main", "master", "dev"},
		ProtectedPaths: []string{"cmd", "internal", "pkg"},
		TestPatterns:   []string{"*_test.go"},
		ForbidCoauthor: true,
	}
}

func TestBashRules(t *testing.T) {
	cases := []struct {
		cmd   string
		allow bool
		rule  string
	}{
		{"git status", true, ""},
		{"go test ./...", true, ""},
		{"git reset --hard HEAD~1", false, "git_destructive"},
		{"git reset --soft HEAD~1", true, ""},
		{"git clean -fd", false, "git_destructive"},
		{"git checkout -- internal/a.go", false, "git_destructive"},
		{"git checkout HEAD -- internal/a.go", false, "git_destructive"},
		{"git checkout -b feature/x", true, ""},
		{"git restore internal/a.go", false, "git_destructive"},
		{"git restore --staged internal/a.go", true, ""},
		{"git stash drop", false, "git_destructive"},
		{"git stash", true, ""},
		{"git push --force origin feature/x", false, "force_push"},
		{"git push -f", false, "force_push"},
		{"git push origin feature/x", true, ""},
		{"git push origin dev", false, "protected_branch"},
		{"git push origin HEAD:main", false, "protected_branch"},
		{"go vet ./... && git push origin master", false, "protected_branch"},
		{`git commit -m "feat: x" -m "Co-Authored-By: Claude <noreply@anthropic.com>"`, false, "coauthor"},
		{`git commit -m "feat: x"`, true, ""},
		{"git add -f .bflow/tasks/X/walkthrough.md", false, "bflow_tracked"},
		{"git add --force a.go", false, "bflow_tracked"},
		{"git add -fA", false, "bflow_tracked"},
		{"git -C . add .bflow/x", false, "bflow_tracked"},
		{`cd x && git add .bflow\x`, false, "bflow_tracked"},
		{"git stage .bflow", false, "bflow_tracked"},
		{`grep -nE "x|git add -f|\.bflow/" CHANGELOG.md`, true, ""},
		{`echo "no uses git add -f .bflow/x"`, true, ""},
		{`rg 'git add -f|y' docs`, true, ""},
		{"cat > n.md <<'EOF'\ngit add -f .bflow/x\nEOF", true, ""},
		{"gh issue create --body \"$(cat <<EOF\ngit add --force a\nEOF\n)\"", true, ""},
		{"echo hi\ngit add -f a.go", false, "bflow_tracked"},
		{`bash -c "ls && git add -f a.go"`, false, "bflow_tracked"},
		{"git add internal/a.go", true, ""},
		{"git add -A", true, ""},
		{"git add -p", true, ""},
		{"echo x > .env", false, "env_file"},
		{"echo x>.env", false, "env_file"},
		{"echo x >> config/.env.local", false, "env_file"},
		{"echo x | tee .env", false, "env_file"},
		{"cp /dev/null .env", false, "env_file"},
		{"cp .env /tmp/x", true, ""},
		{"cp a .env.example", true, ""},
		{"rm .env*", false, "env_file"},
		{"mv .env old", false, "env_file"},
		{"rm -rf .bflow", false, "bflow_state"},
		{"rm -rf .bflow/", false, "bflow_state"},
		{"mv .bflow/state.json x", false, "bflow_state"},
		{"echo x > .bflow/tasks/X/state.json", false, "bflow_state"},
		{"cat > .bflow/tasks/X/contract.md", true, ""},
		{"echo hi 2>&1", true, ""},
		{"ls .env", true, ""},
		{"git push origin +main", false, "force_push"},
		{"git push origin +HEAD:feature/x", false, "force_push"},
		{"git -c x=y reset --hard", false, "git_destructive"},
		{"git -C . reset --hard", false, "git_destructive"},
		{"git -C sub -c a=b clean -fd", false, "git_destructive"},
		{"git --git-dir=.git reset --hard", false, "git_destructive"},
		{"git --git-dir .git --work-tree . push -f", false, "force_push"},
		{"git --no-pager -c x=y push origin dev", false, "protected_branch"},
		{"git -c x=y status", true, ""},
		{"git -c x=y push origin feature/x", true, ""},
		{"git -C sub rm .env", false, "env_file"},
	}
	for _, c := range cases {
		d := Evaluate(Action{Tool: Bash, Command: c.cmd}, ctx())
		if d.Allow != c.allow || d.Rule != c.rule {
			t.Errorf("%q → allow=%v rule=%q, want allow=%v rule=%q", c.cmd, d.Allow, d.Rule, c.allow, c.rule)
		}
		if !d.Allow && d.Reason == "" {
			t.Errorf("%q: un bloqueo sin motivo", c.cmd)
		}
	}
}

func TestEditRules(t *testing.T) {
	c := ctx()
	cases := []struct {
		name  string
		a     Action
		mod   func(*Context)
		allow bool
		rule  string
	}{
		{"código normal", Action{Tool: Edit, Path: "internal/a.go"}, nil, true, ""},
		{".env", Action{Tool: Write, Path: ".env"}, nil, false, "env_file"},
		{".env.local", Action{Tool: Edit, Path: "config/.env.local"}, nil, false, "env_file"},
		{".env.example se permite", Action{Tool: Edit, Path: ".env.example"}, nil, true, ""},
		{"leader estricto en código", Action{Tool: Edit, Path: "internal/a.go"}, func(c *Context) { c.StrictLeader = true }, false, "leader_code"},
		{"subagente con leader estricto", Action{Tool: Edit, Path: "internal/a.go", Subagent: true}, func(c *Context) { c.StrictLeader = true }, true, ""},
		{"leader estricto fuera de código", Action{Tool: Edit, Path: "docs/x.md"}, func(c *Context) { c.StrictLeader = true }, true, ""},
		{"prueba congelada en implementing", Action{Tool: Edit, Path: "internal/a_test.go", Subagent: true},
			func(c *Context) { c.Phase = flow.Implementing; c.Frozen = []string{"internal/a_test.go"} }, false, "frozen_test"},
		{"prueba congelada en quality", Action{Tool: Write, Path: "internal/a_test.go"},
			func(c *Context) { c.Phase = flow.Quality; c.Frozen = []string{"internal/a_test.go"} }, false, "frozen_test"},
		{"prueba nueva se permite", Action{Tool: Write, Path: "internal/b_test.go"},
			func(c *Context) { c.Phase = flow.Implementing; c.Frozen = []string{"internal/a_test.go"} }, true, ""},
		{"en contract todavía se edita", Action{Tool: Edit, Path: "internal/a_test.go"},
			func(c *Context) { c.Phase = flow.Contract; c.Frozen = []string{"internal/a_test.go"} }, true, ""},
		{"borrar una prueba congelada por bash", Action{Tool: Bash, Command: "rm internal/a_test.go"},
			func(c *Context) { c.Phase = flow.Implementing; c.Frozen = []string{"internal/a_test.go"} }, false, "frozen_test"},
		{"vulnerabilidades aceptadas: solo una persona", Action{Tool: Edit, Path: "security/vulns-accepted.json", Subagent: true},
			func(c *Context) { c.HumanFiles = []string{"security/vulns-accepted.json"} }, false, "human_file"},
	}
	for _, tc := range cases {
		cc := c
		if tc.mod != nil {
			tc.mod(&cc)
		}
		d := Evaluate(tc.a, cc)
		if d.Allow != tc.allow || d.Rule != tc.rule {
			t.Errorf("%s → allow=%v rule=%q (%s), want allow=%v rule=%q", tc.name, d.Allow, d.Rule, d.Reason, tc.allow, tc.rule)
		}
	}
}

func TestBflowOwnedActions(t *testing.T) {
	active := ctx()
	active.Phase = flow.Implementing
	cases := []struct {
		a     Action
		c     Context
		allow bool
		rule  string
	}{
		// Con una tarea en curso, rama y PR los maneja bflow.
		{Action{Tool: Bash, Command: "gh pr create --fill"}, active, false, "bflow_pr"},
		{Action{Tool: Bash, Command: "go vet ./... && gh pr create -t x"}, active, false, "bflow_pr"},
		{Action{Tool: Bash, Command: "gh pr view 12"}, active, true, ""},
		{Action{Tool: Bash, Command: "git checkout -b feature/x"}, active, false, "bflow_branch"},
		{Action{Tool: Bash, Command: "git checkout -B feature/x origin/dev"}, active, false, "bflow_branch"},
		{Action{Tool: Bash, Command: "git switch -c feature/x"}, active, false, "bflow_branch"},
		{Action{Tool: Bash, Command: "git branch feature/x"}, active, false, "bflow_branch"},
		{Action{Tool: Bash, Command: "git branch --show-current"}, active, true, ""},
		{Action{Tool: Bash, Command: "git checkout feature/x"}, active, true, ""},
		{Action{Tool: Bash, Command: "git switch dev"}, active, true, ""},
		// Sin tarea en curso, el agente puede trabajar fuera del flujo.
		{Action{Tool: Bash, Command: "gh pr create --fill"}, ctx(), true, ""},
		{Action{Tool: Bash, Command: "git switch -c chore/x"}, ctx(), true, ""},
		// Siempre: estado de bflow y re-congelar pruebas.
		{Action{Tool: Bash, Command: "bflow freeze API-1"}, ctx(), false, "human_only"},
		{Action{Tool: Bash, Command: "bflow status --json"}, ctx(), true, ""},
		{Action{Tool: Edit, Path: ".bflow/tasks/API-1/state.json"}, ctx(), false, "bflow_state"},
		{Action{Tool: Write, Path: ".bflow/tasks/API-1/frozen-tests.json"}, ctx(), false, "bflow_state"},
		{Action{Tool: Write, Path: ".bflow/log.jsonl"}, ctx(), false, "bflow_state"},
		{Action{Tool: Write, Path: ".bflow/tasks/API-1/contract.md"}, ctx(), true, ""},
		{Action{Tool: Write, Path: ".bflow/tasks/API-1/reports/review-map.md"}, ctx(), true, ""},
		{Action{Tool: Write, Path: ".bflow/tasks/API-1/walkthrough.md"}, ctx(), true, ""},
		{Action{Tool: Write, Path: ".bflow/tasks/API-1/reports"}, ctx(), false, "bflow_state"},
		{Action{Tool: Write, Path: ".bflowrc"}, ctx(), true, ""},
	}
	for _, tc := range cases {
		d := Evaluate(tc.a, tc.c)
		if d.Allow != tc.allow || d.Rule != tc.rule {
			t.Errorf("%+v (fase %q) → allow=%v rule=%q, want allow=%v rule=%q", tc.a, tc.c.Phase, d.Allow, d.Rule, tc.allow, tc.rule)
		}
	}
	for _, cmd := range []string{"gh pr create", "cd x && git switch -c y", "git branch z"} {
		if !TaskScoped(cmd) {
			t.Errorf("TaskScoped(%q) = false", cmd)
		}
	}
	if TaskScoped("git branch -a") || TaskScoped("go test ./...") {
		t.Error("TaskScoped no debe pedir la tarea para comandos ajenos")
	}
}

func TestDiffSize(t *testing.T) {
	c := ctx()
	c.MaxDiffLines = 400
	c.DiffLines = func() (int, error) { return 650, nil }
	d := Evaluate(Action{Tool: Bash, Command: `git commit -m "feat: grande"`}, c)
	if d.Allow || d.Rule != "diff_size" || !strings.Contains(d.Reason, "650") || !strings.Contains(d.Reason, "400") {
		t.Errorf("diff grande: %+v", d)
	}
	c.DiffLines = func() (int, error) { return 120, nil }
	if d := Evaluate(Action{Tool: Bash, Command: `git commit -m "x"`}, c); !d.Allow {
		t.Errorf("diff chico: %+v", d)
	}
	called := false
	c.DiffLines = func() (int, error) { called = true; return 0, nil }
	Evaluate(Action{Tool: Bash, Command: "go test ./..."}, c)
	if called {
		t.Error("el tamaño del diff solo se calcula al commitear (el guard corre en cada herramienta)")
	}
}

func TestPathNormalization(t *testing.T) {
	c := ctx()
	c.Root = `C:\repo`
	c.Phase = flow.Implementing
	c.Frozen = []string{"internal/a_test.go"}
	d := Evaluate(Action{Tool: Edit, Path: `C:\repo\internal\a_test.go`}, c)
	if d.Allow {
		t.Error("rutas absolutas de Windows deben normalizarse a relativas")
	}
}

func TestHumanOnlySubagent(t *testing.T) {
	active := ctx()
	active.Phase = flow.Implementing
	sub := func(cmd string) Action { return Action{Tool: Bash, Command: cmd, Subagent: true, Agent: "implementer"} }
	main := func(cmd string) Action { return Action{Tool: Bash, Command: cmd} }
	for _, name := range []string{"approve", "reject", "unblock", "start", "new"} {
		cmd := "bflow " + name + " GH-1"
		d := Evaluate(sub(cmd), active)
		if d.Allow || d.Rule != "human_only" {
			t.Errorf("subagente %q → allow=%v rule=%q", cmd, d.Allow, d.Rule)
		}
		if !strings.Contains(d.Reason, name) || !strings.Contains(d.Reason, "bflow report") || (!strings.Contains(d.Reason, "persona") && !strings.Contains(d.Reason, "human decision")) {
			t.Errorf("motivo de %s: %q", name, d.Reason)
		}
		if d := Evaluate(main(cmd), active); !d.Allow {
			t.Errorf("la sesión principal corre %q: %+v", cmd, d)
		}
	}
	cases := []struct {
		cmd   string
		ctx   Context
		allow bool
	}{
		{"bflow report GH-1 --agent implementer --verdict DONE", active, true},
		{"bflow block GH-1 --note x", active, true},
		{"bflow status", active, true},
		{"bflow show GH-1 spec", active, true},
		{"bflow check GH-1", active, true},
		{"bflow pr GH-1", active, true},
		{"bflow task add titulo", active, true},
		{"bflow status && bflow approve GH-1", active, false},
		{"go test ./... ; ./bin/bflow.exe --json reject GH-1", active, false},
		{"bflow approve --help", active, false},
		{"bflow approve GH-1", ctx(), false}, // sin tarea activa ni fase
		{"echo \"<<EOF\"\nbflow approve GH-1\nEOF", active, false},
		{"cat <<'EOF'\nbflow approve GH-1\nEOF", active, true},
		{`env -i X=1 "C:\tools\BFLOW.EXE" unblock GH-1`, active, false},
	}
	for _, tc := range cases {
		d := Evaluate(sub(tc.cmd), tc.ctx)
		if d.Allow != tc.allow || (!tc.allow && d.Rule != "human_only") {
			t.Errorf("subagente %q → allow=%v rule=%q, want allow=%v", tc.cmd, d.Allow, d.Rule, tc.allow)
		}
	}
}

func TestFreezeEveryone(t *testing.T) {
	for _, cmd := range []string{"bflow freeze GH-1", "./bin/bflow.exe freeze GH-1 --allow a_test.go", "X=1 bflow --json freeze GH-1"} {
		for _, subagent := range []bool{true, false} {
			d := Evaluate(Action{Tool: Bash, Command: cmd, Subagent: subagent}, ctx())
			if d.Allow || d.Rule != "human_only" {
				t.Errorf("%q (subagente=%v) → allow=%v rule=%q", cmd, subagent, d.Allow, d.Rule)
			}
		}
	}
}

func TestBflowSubcommand(t *testing.T) {
	cases := []struct{ seg, want string }{
		{"bflow approve GH-1", "approve"},
		{"bflow", ""},
		{"BFLOW.EXE Approve GH-1", "approve"},
		{"./bin/bflow.exe freeze GH-1", "freeze"},
		{`C:\tools\bflow.exe reject GH-1`, "reject"},
		{`"/usr/local/bin/bflow" start GH-1`, "start"},
		{`'bflow' new idea`, "new"},
		{"X=1 bflow approve GH-1", "approve"},
		{"X=1 Y=2 bflow approve GH-1", "approve"},
		{"env -i X=1 bflow unblock GH-1", "unblock"},
		{"go run ./cmd/bflow approve GH-1", "approve"},
		{`go run -race C:\jaad\bflow\cmd\bflow\ approve GH-1`, "approve"},
		{"bflow --json approve GH-1", "approve"},
		{"bflow -json status", "status"},
		{"bflowx approve GH-1", ""},
		{"echo bflow approve", ""},
		{"go run ./cmd/other approve", ""},
		{"go test ./...", ""},
		{"", ""},
		{`"C:\Program Files\bflow\bflow.exe" approve X`, "approve"},
	}
	for _, c := range cases {
		if got := BflowSubcommand(c.seg); got != c.want {
			t.Errorf("BflowSubcommand(%q) = %q, want %q", c.seg, got, c.want)
		}
	}
}

func TestShellWrapped(t *testing.T) {
	active := ctx()
	active.Phase = flow.Implementing
	sub := func(cmd string) Decision {
		return Evaluate(Action{Tool: Bash, Command: cmd, Subagent: true, Agent: "implementer"}, active)
	}
	if d := sub(`bash -c "bflow approve X"`); d.Allow || d.Rule != "human_only" {
		t.Errorf("subagente bash -c approve → %+v", d)
	}
	if d := Evaluate(Action{Tool: Bash, Command: `bash -c "bflow approve X"`}, active); !d.Allow {
		t.Errorf("sesión principal debe poder: %+v", d)
	}
	for cmd, rule := range map[string]string{
		`sh -c 'git reset --hard'`:                 "git_destructive",
		`bash -lc "git push -f origin x"`:          "force_push",
		`/usr/bin/zsh -c "git push origin main"`:   "protected_branch",
		`bash -c "sh -c 'bflow approve X'"`:        "human_only",
		`bash -c "sh -c 'zsh -c bflow approve X'"`: "human_only",
	} {
		if d := sub(cmd); d.Allow || d.Rule != rule {
			t.Errorf("%q → allow=%v rule=%q, quería %q", cmd, d.Allow, d.Rule, rule)
		}
	}
	if d := sub(`"C:\Program Files\Git\bin\bash.exe" -c "bflow approve X"`); d.Allow || d.Rule != "human_only" {
		t.Errorf("bash entre comillas con espacios → %+v", d)
	}
	if !TaskScoped(`bash -c "gh pr create"`) {
		t.Error("TaskScoped debe ver dentro de bash -c")
	}
	if d := Evaluate(Action{Tool: Bash, Command: `bash -c "gh pr create"`}, active); d.Rule != "bflow_pr" {
		t.Errorf("bflow_pr dentro de bash -c → %+v", d)
	}
}

func TestSegmentsUnwrap(t *testing.T) {
	for _, cmd := range []string{
		`bash -c "x y"`, `/bin/bash -c "x y"`, `C:\Git\bin\bash.exe -c "x y"`, `BASH -c "x y"`,
		`"bash" -c "x y"`, `FOO=1 bash -c "x y"`, `env -i FOO=1 sh -c "x y"`, `bash -ec "x y"`,
		`bash -l -c "x y"`, `bash -o pipefail -c "x y"`, `zsh +O extglob -c 'x y'`, `sh -c x y`,
	} {
		if got := Segments(cmd); len(got) != 1 || got[0] != "x y" {
			t.Errorf("%q → %q", cmd, got)
		}
	}
	for _, cmd := range []string{`bash script.sh`, `bashx -c x`, `echo bash -c x`, `bash -x script.sh`, `bash -o pipefail`} {
		if got := Segments(cmd); len(got) != 1 || got[0] != cmd {
			t.Errorf("%q debía quedar igual, dio %q", cmd, got)
		}
	}
}

func TestSegmentsQuotedExe(t *testing.T) {
	for _, cmd := range []string{
		`'C:\Program Files\Git\bin\sh.exe' -c "x y"`,
		`"C:\Program Files\Git\bin\bash.exe" -l -c 'x y'`,
	} {
		if got := Segments(cmd); len(got) != 1 || got[0] != "x y" {
			t.Errorf("%q → %q", cmd, got)
		}
	}
	if !feedsShell(`"C:\Program Files\Git\bin\bash.exe" <<EOF`) {
		t.Error("feedsShell no reconoce el shell entre comillas con espacios")
	}
}

func TestQuotedFields(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{`"C:\Program Files\Git\bin\bash.exe" -c "bflow approve X"`, []string{`"C:\Program Files\Git\bin\bash.exe"`, `-c`, `"bflow approve X"`}},
		{`a  b	c`, []string{"a", "b", "c"}},
		{`a"b c"d e`, []string{`a"b c"d`, "e"}},
		{`'a b' "c d"`, []string{`'a b'`, `"c d"`}},
		{`a "b c`, []string{"a", `"b c`}},
		{`"C:\dir\" x`, []string{`"C:\dir\"`, "x"}},
		{`""`, []string{`""`}},
		{"", nil},
		{"   ", nil},
	} {
		got := quotedFields(c.in)
		if !slices.Equal(got, c.want) {
			t.Errorf("quotedFields(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	for _, in := range []string{"git status -s", " a  b ", "x=1 y"} {
		if !slices.Equal(quotedFields(in), strings.Fields(in)) {
			t.Errorf("sin comillas debe igualar strings.Fields: %q", in)
		}
	}
}

func TestShellEmptyTokens(t *testing.T) {
	for _, cmd := range []string{`bash "" -c x`, `bash '' -c "bflow approve X"`, `sh ""`, `bash -c`} {
		_ = segments(cmd, 0) // no debe entrar en pánico
	}
}

// R12: retirar una tarea es decisión de una persona.
func TestHumanOnlyDrop(t *testing.T) {
	active := ctx()
	active.Phase = flow.Implementing
	cmd := `bflow drop GH-1 --note "ya no aplica"`
	d := Evaluate(Action{Tool: Bash, Command: cmd, Subagent: true, Agent: "implementer"}, active)
	if d.Allow || d.Rule != "human_only" {
		t.Errorf("subagente %q → allow=%v rule=%q", cmd, d.Allow, d.Rule)
	}
	if d := Evaluate(Action{Tool: Bash, Command: cmd}, active); !d.Allow {
		t.Errorf("la sesión principal corre %q: %+v", cmd, d)
	}
	if !slices.Contains(HumanOnly, "drop") {
		t.Errorf("HumanOnly: %v", HumanOnly)
	}
}

func TestBflowArgs(t *testing.T) {
	for _, c := range []struct {
		seg  string
		want []string
	}{
		{"bflow status GH-51", []string{"GH-51"}},
		{"./bin/bflow.exe --json approve GH-51 --note x", []string{"GH-51", "x"}},
		{"go run ./cmd/bflow next GH-2", []string{"GH-2"}},
		{"FOO=1 bflow report GH-7 --agent implementer --verdict DONE", []string{"GH-7", "implementer", "DONE"}},
		{"bflow status", nil},
		{"git status GH-51", nil},
		{"echo bflow status GH-51", nil},
		{`bflow new "título con espacios"`, []string{`título con espacios`}},
	} {
		got := BflowArgs(c.seg)
		if len(got) != len(c.want) {
			t.Errorf("%q → %v, want %v", c.seg, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%q → %v, want %v", c.seg, got, c.want)
				break
			}
		}
	}
}

// El cuerpo de un heredoc que ejecuta un shell son comandos; si lo lee otro
// programa, es texto.
func TestHeredocExecutedByShell(t *testing.T) {
	active := ctx()
	active.Phase = flow.Implementing
	cases := []struct {
		cmd, rule string
	}{
		{"bash <<EOF\ngit add -f .bflow/x\nEOF", "bflow_tracked"},
		{"sh <<'EOF'\nbflow approve X\nEOF", "human_only"},
		{"cat <<EOF | bash\ngit add -f .bflow/x\nEOF", "bflow_tracked"},
		{"bash -s <<EOF\ngit push --force origin x\nEOF", "force_push"},
		{"cat <<EOF | tee n.md\ngit add -f .bflow/x\nEOF", ""},
		{"python - <<EOF\ngit add -f .bflow/x\nEOF", ""},
		{"cat <<< \"x\"\ngit add -f .bflow/x", "bflow_tracked"},
		{"echo $((1 << 2))\ngit add -f .bflow/x", "bflow_tracked"},
		{"echo \"$(git add -f .bflow/x)\"", "bflow_tracked"},
		{"echo \"`git add -f .bflow/x`\"", "bflow_tracked"},
	}
	for _, c := range cases {
		d := Evaluate(Action{Tool: Bash, Command: c.cmd, Subagent: true, Agent: "implementer"}, active)
		if c.rule == "" && !d.Allow || c.rule != "" && (d.Allow || d.Rule != c.rule) {
			t.Errorf("%q → %+v, quería regla %q", c.cmd, d, c.rule)
		}
	}
}

// frozenCtx es una tarea en implementing con dos pruebas congeladas (GH-86).
func frozenCtx() Context {
	c := ctx()
	c.Root = "/repo"
	c.Phase = flow.Implementing
	c.Frozen = []string{"internal/a_test.go", "internal/b_test.go"}
	return c
}

func bashCmd(cmd string) Action {
	return Action{Tool: Bash, Command: cmd, Subagent: true, Agent: "bflow-implementer"}
}

// R1-R7: escribir una prueba congelada desde Bash se niega con frozen_test.
func TestBashFrozenWrites(t *testing.T) {
	root := filepath.ToSlash(testutil.TempDir(t))
	diff := "diff --git a/internal/a_test.go b/internal/a_test.go\n--- a/internal/a_test.go\n+++ b/internal/a_test.go\n@@ -1 +1 @@\n-x\n+y\n"
	if err := os.WriteFile(filepath.Join(root, "x.diff"), []byte(diff), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, cmd string }{
		{"sed -i", `sed -i 's/a/b/' internal/a_test.go`},
		{"sed -i.bak", `sed -i.bak 's/a/b/' internal/a_test.go`},
		{"sed -Ei agrupado", `sed -Ei 's/a/b/' internal/a_test.go`},
		{"sed --in-place", `sed --in-place -e 's/a/b/' internal/a_test.go`},
		{"perl -pi", `perl -pi -e 's/a/b/' internal/a_test.go`},
		{"redirección >", `echo x > internal/a_test.go`},
		{"redirección sin espacio", `echo x >internal/a_test.go`},
		{"redirección >>", `echo x >> internal/a_test.go`},
		{"redirección 2>", `go test ./... 2> internal/a_test.go`},
		{"redirección &>", `go test ./... &> internal/a_test.go`},
		{"redirección >|", `echo x >| internal/a_test.go`},
		{"tee", `echo x | tee internal/a_test.go`},
		{"tee -a", `echo x | tee -a internal/a_test.go`},
		{"cp destino", `cp /tmp/x internal/a_test.go`},
		{"install destino", `install -m 644 /tmp/x internal/a_test.go`},
		{"comodín en sed -i", `sed -i 's/a/b/' internal/*_test.go`},
		{"comodín ? en tee", `echo x | tee internal/?_test.go`},
		{"python con heredoc", "python - <<'EOF'\nopen('internal/a_test.go','w').write('')\nEOF"},
		{"cat heredoc a python", "cat <<EOF | python3\nopen('internal/a_test.go','w').write('')\nEOF"},
		{"python -c", `python -c "open('internal/a_test.go','w').write('')"`},
		{"node -e", `node -e "require('fs').writeFileSync('internal/a_test.go','')"`},
		{"git apply con el parche en un archivo", "git apply x.diff"},
		{"patch con <", "patch -p1 < x.diff"},
		{"git apply con heredoc", "git apply <<EOF\n--- a/internal/a_test.go\n+++ b/internal/a_test.go\nEOF"},
		{"dentro de bash -c", `bash -c "sed -i 's/a/b/' internal/a_test.go"`},
		{"cadena con &&", `go vet ./... && echo x > internal/a_test.go`},
		{"bash entre comillas con espacios", `"C:\Program Files\Git\bin\bash.exe" -c "echo x > internal/a_test.go"`},
		{"ruta absoluta bajo Root", `sed -i 's/a/b/' ` + root + `/internal/a_test.go`},
	}
	for _, tc := range cases {
		c := frozenCtx()
		c.Root = root
		d := Evaluate(bashCmd(tc.cmd), c)
		if d.Allow || d.Rule != "frozen_test" {
			t.Errorf("%s: %q → allow=%v rule=%q", tc.name, tc.cmd, d.Allow, d.Rule)
		}
	}
}

// R12: redirección a una ruta entre comillas con espacios.
func TestBashFrozenQuotedPath(t *testing.T) {
	c := frozenCtx()
	c.Frozen = []string{"dir con espacio/a_test.go"}
	d := Evaluate(bashCmd(`echo x > "dir con espacio/a_test.go"`), c)
	if d.Allow || d.Rule != "frozen_test" {
		t.Errorf("allow=%v rule=%q", d.Allow, d.Rule)
	}
}

// R8: leer la prueba congelada no se niega.
func TestBashFrozenReads(t *testing.T) {
	for _, cmd := range []string{
		"cat internal/a_test.go",
		"grep -n Test internal/a_test.go",
		"rg Test internal/a_test.go",
		"head -20 internal/a_test.go",
		"tail -5 internal/a_test.go",
		"less internal/a_test.go",
		"sed -n '1,5p' internal/a_test.go",
		"sed -n 1p internal/a_test.go > /tmp/y",
		"go test ./...",
		"go test ./internal/... 2>&1",
		"go test ./... > /tmp/out.txt",
		"git diff internal/a_test.go",
		"git log -p -- internal/a_test.go",
		"git show HEAD:internal/a_test.go",
		"cp internal/a_test.go /tmp/x",
		"wc -l < internal/a_test.go",
		"python x.py",
		"cat internal/a_test.go | grep Test",
		"echo hola > /tmp/x",
		"tee /tmp/log.txt",
		"cp /tmp/a.go internal/a.go",
	} {
		if d := Evaluate(bashCmd(cmd), frozenCtx()); !d.Allow {
			t.Errorf("%q es lectura: %+v", cmd, d)
		}
	}
}

// R9: fuera de las fases congeladas, o con la prueba en allow (c.Frozen sin ella), no aplica.
func TestBashFrozenOutsidePhase(t *testing.T) {
	cmds := []string{`sed -i 's/a/b/' internal/a_test.go`, `echo x > internal/a_test.go`, `python -c "open('internal/a_test.go','w')"`}
	for _, p := range []flow.Phase{flow.Contract, flow.Spec, flow.InReview, flow.Done} {
		c := frozenCtx()
		c.Phase = p
		for _, cmd := range cmds {
			if d := Evaluate(bashCmd(cmd), c); !d.Allow {
				t.Errorf("fase %s, %q: %+v", p, cmd, d)
			}
		}
	}
	c := frozenCtx()
	c.Frozen = nil // todas en allow
	for _, cmd := range cmds {
		if d := Evaluate(bashCmd(cmd), c); !d.Allow {
			t.Errorf("sin congeladas, %q: %+v", cmd, d)
		}
	}
	c.Frozen = []string{"internal/b_test.go"} // a_test.go está en allow
	if d := Evaluate(bashCmd(cmds[0]), c); !d.Allow {
		t.Errorf("la prueba en allow se puede cambiar: %+v", d)
	}
	for _, p := range []flow.Phase{flow.Implementing, flow.Paused, flow.Quality, flow.Documenting, flow.Walkthrough} {
		c := frozenCtx()
		c.Phase = p
		if d := Evaluate(bashCmd(cmds[0]), c); d.Allow || d.Rule != "frozen_test" {
			t.Errorf("fase %s debe negar: %+v", p, d)
		}
	}
}

// R10: el motivo nombra la ruta y dice lo mismo que el de Edit/Write.
func TestBashFrozenReason(t *testing.T) {
	c := frozenCtx()
	edit := Evaluate(Action{Tool: Edit, Path: "internal/a_test.go"}, c)
	d := Evaluate(bashCmd(`sed -i 's/a/b/' internal/a_test.go`), c)
	if d.Allow || d.Reason != edit.Reason {
		t.Errorf("mismo motivo que Edit:\n%q\n%q", d.Reason, edit.Reason)
	}
	for _, w := range []string{"internal/a_test.go", "NEEDS_DECISION", "bflow freeze --allow internal/a_test.go"} {
		if !strings.Contains(d.Reason, w) {
			t.Errorf("el motivo no dice %q: %s", w, d.Reason)
		}
	}
	r6 := Evaluate(bashCmd(`python -c "open('internal/b_test.go','w')"`), c)
	if r6.Allow || !strings.HasPrefix(r6.Reason, strings.ReplaceAll(edit.Reason, "internal/a_test.go", "internal/b_test.go")) ||
		(!strings.Contains(r6.Reason, "cat o grep") && !strings.Contains(r6.Reason, "cat or grep")) {
		t.Errorf("R6 suma cómo leerla: %+v", r6)
	}
	if strings.Contains(d.Reason, "cat o grep") || strings.Contains(d.Reason, "cat or grep") {
		t.Errorf("solo R6 habla de leer: %s", d.Reason)
	}
}

// R11: qué comandos necesitan cargar la fase y las pruebas congeladas.
func TestMayWrite(t *testing.T) {
	for cmd, want := range map[string]bool{
		"echo x > a.txt": true, "sed -i s/a/b/ f": true, "perl -pi -e x f": true, "tee f": true, "cp a b": true,
		"install a b": true, "git apply x.diff": true, "patch -p1": true, "python3 x.py": true, "node -e x": true,
		"pwsh -c x": true, "PYTHON -c x": true, "ruby -e x": true,
		"go test ./...": false, "cat internal/a_test.go": false, "git status": false, "ls -la": false,
	} {
		if got := MayWrite(cmd); got != want {
			t.Errorf("MayWrite(%q) = %v", cmd, got)
		}
	}
}

func TestFrozenPhase(t *testing.T) {
	for p, want := range map[flow.Phase]bool{
		flow.Implementing: true, flow.Paused: true, flow.Quality: true, flow.Documenting: true, flow.Walkthrough: true,
		flow.Contract: false, flow.Spec: false, flow.InReview: false, flow.Done: false, "": false,
	} {
		if got := FrozenPhase(p); got != want {
			t.Errorf("FrozenPhase(%q) = %v", p, got)
		}
	}
}

func TestEvalOutWriteAllowed(t *testing.T) {
	const out = ".bflow/cache/evals/2026-10-19-101500/caso-uno/out/"
	cases := []struct {
		path  string
		allow bool
	}{
		{out + "review-map.md", true},
		{out + "security.md", true},
		{out + "review-map.json", false},
		{out + "sub/review-map.md", false},
		{".bflow/cache/evals/2026-10-19-101500/caso-uno/review-map.md", false},
		{".bflow/cache/evals/2026-10-19-101500/caso-uno/wt/x.md", false},
		{".bflow/cache/evals/2026-10-19-101500/run.json", false},
		{".bflow/cache/evals/2026-10-19-101500/../caso-uno/out/x.md", false},
		{".bflow/cache/other/2026-10-19-101500/caso-uno/out/x.md", false},
		{".bflow/evals/reviewer/caso-uno/case.yaml", false},
		{".bflow/log.jsonl", false},
	}
	for _, tc := range cases {
		for _, tool := range []string{Write, Edit} {
			d := Evaluate(Action{Tool: tool, Path: tc.path, Subagent: true}, ctx())
			if d.Allow != tc.allow || (!tc.allow && d.Rule != "bflow_state") {
				t.Errorf("%s %s → allow=%v rule=%q, want allow=%v", tool, tc.path, d.Allow, d.Rule, tc.allow)
			}
		}
	}
}

func TestGitAddEvalsAllowed(t *testing.T) {
	cases := []struct {
		cmd   string
		allow bool
	}{
		{"git add .bflow/evals/2026-10-19-101500.json", true},
		{"git add .bflow/evals/reviewer/caso-uno/case.yaml", true},
		{"git -C . add .bflow/evals/reviewer", true},
		{"git add -f .bflow/evals/reviewer/caso-uno/case.yaml", false},
		{"git add --force .bflow/evals/x.json", false},
		{"git add .bflow/evals/x.json .bflow/log.jsonl", false},
		{"git add .bflow/tasks/X/walkthrough.md", false},
		{"git add .bflow/cache/evals/x", false},
		{`git add .bflow\evals\x.json`, true},
		{"git add .bflow/evals/../log.jsonl", false},
	}
	for _, tc := range cases {
		d := Evaluate(Action{Tool: Bash, Command: tc.cmd}, ctx())
		if d.Allow != tc.allow || (!tc.allow && d.Rule != "bflow_tracked") {
			t.Errorf("%q → allow=%v rule=%q, want allow=%v", tc.cmd, d.Allow, d.Rule, tc.allow)
		}
	}
}

func TestGuardLocalization(t *testing.T) {
	b, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	cEn := Context{Loc: b.New("en")}
	cEs := Context{Loc: b.New("es")}

	// git destructive hard
	actHard := Action{Tool: Bash, Command: "git reset --hard"}
	dEn := Evaluate(actHard, cEn)
	if !strings.Contains(dEn.Reason, "discards work with no way to recover") {
		t.Errorf("expected English denial, got: %s", dEn.Reason)
	}
	dEs := Evaluate(actHard, cEs)
	if !strings.Contains(dEs.Reason, "descarta trabajo sin forma de recuperarlo") {
		t.Errorf("expected Spanish denial, got: %s", dEs.Reason)
	}

	// human only freeze
	actFreeze := Action{Tool: Bash, Command: "bflow freeze"}
	dEn = Evaluate(actFreeze, cEn)
	if !strings.Contains(dEn.Reason, "accepts changes to frozen tests: run by a person") {
		t.Errorf("expected English denial, got: %s", dEn.Reason)
	}
	dEs = Evaluate(actFreeze, cEs)
	if !strings.Contains(dEs.Reason, "acepta cambios a pruebas congeladas: lo corre una persona") {
		t.Errorf("expected Spanish denial, got: %s", dEs.Reason)
	}

	// env file
	actEnv := Action{Tool: Edit, Path: ".env"}
	dEn = Evaluate(actEnv, cEn)
	if !strings.Contains(dEn.Reason, "may contain secrets: cannot be edited") {
		t.Errorf("expected English denial, got: %s", dEn.Reason)
	}
	dEs = Evaluate(actEnv, cEs)
	if !strings.Contains(dEs.Reason, "puede tener secretos: no se edita") {
		t.Errorf("expected Spanish denial, got: %s", dEs.Reason)
	}
}
