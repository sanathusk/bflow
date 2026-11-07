package guard

import (
	"strings"
	"testing"

	"innobytes.tech/bflow/internal/i18n"
)

func TestAdvise(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{"go test ./...", true},
		{"cd x && npm test", true},
		{`bash -c "pytest -q"`, true},
		{"CGO_ENABLED=0 go test -run X ./internal/guard", true},
		{`"/usr/local/go/bin/go" test ./...`, true},
		{"npm run test", true},
		{"pnpm test", true},
		{"yarn test", true},
		{"npx vitest run", true},
		{"npx jest", true},
		{"python -m pytest", true},
		{"cargo test", true},
		{"dotnet test", true},
		{"echo go test", false},
		{`grep -r "npm test" .`, false},
		{`git commit -m "go test"`, false},
		{"go build ./...", false},
		{"npm install", false},
		{"bflow check GH-1 --quick go", false},
		{"ls", false},
	}
	for _, c := range cases {
		got := Advise(Action{Tool: Bash, Command: c.cmd})
		if (got != "") != c.want {
			t.Errorf("%q: aviso=%q, quería aviso=%v", c.cmd, got, c.want)
			continue
		}
		if got == "" {
			continue
		}
		if !strings.Contains(got, "bflow check --quick") || (!strings.Contains(got, "sí corrió") && !strings.Contains(got, "ran")) {
			t.Errorf("%q: el texto no sugiere ni dice que corrió: %q", c.cmd, got)
		}
		if len([]rune(got)) > 200 || strings.Contains(got, "\n") {
			t.Errorf("%q: debe caber en una línea de 200: %d", c.cmd, len([]rune(got)))
		}
	}
	if got := Advise(Action{Tool: Edit, Command: "go test"}); got != "" {
		t.Errorf("solo Bash: %q", got)
	}
	if got := Advise(Action{Tool: Bash, Command: "go test"}); !strings.Contains(got, "`go test`") {
		t.Errorf("nombra el comando detectado: %q", got)
	}
}

func TestAdviseLocalization(t *testing.T) {
	b, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	a := Action{Tool: Bash, Command: "go test ./..."}
	en := Advise(a, b.New("en"))
	if !strings.Contains(en, "ran (notice, not a block)") {
		t.Errorf("expected English advise, got: %s", en)
	}
	es := Advise(a, b.New("es"))
	if !strings.Contains(es, "sí corrió (aviso, no bloqueo)") {
		t.Errorf("expected Spanish advise, got: %s", es)
	}
}
