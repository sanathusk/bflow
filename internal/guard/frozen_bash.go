package guard

import (
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"innobytes.tech/bflow/internal/flow"
)

// Escrituras a pruebas congeladas desde Bash (GH-86).

func frozenBashReason(c Context, p string, read bool) string {
	r := c.t("guard.frozen_bash_reason", p, p)
	if read {
		r += c.t("guard.frozen_bash_read_hint")
	}
	return r
}

// FrozenPhase dice si en la fase p las pruebas del contrato no se tocan.
func FrozenPhase(p flow.Phase) bool { return slices.Contains(frozenPhases, p) }

var mayWriteRe = regexp.MustCompile(`(?i)(>|\b(sed|perl|tee|cp|install|apply|patch|python[0-9.]*|py|ruby|node|deno|bun|php|pwsh|powershell)\b)`)

// MayWrite dice si un comando Bash podría escribir un archivo: entonces el guard
// necesita la fase y las pruebas congeladas de la tarea activa (R11).
func MayWrite(cmd string) bool { return mayWriteRe.MatchString(cmd) }

var interpreters = map[string]bool{"python": true, "py": true, "ruby": true, "node": true, "deno": true, "bun": true,
	"php": true, "pwsh": true, "powershell": true, "perl": true}

var (
	versionSuffix = regexp.MustCompile(`[0-9.]+$`)
	inplaceRe     = regexp.MustCompile(`^-[A-Za-z]*i`)
	evalFlagRe    = regexp.MustCompile(`^(-[a-z]*[ce]|-command|--eval|--command)$`)
)

// cmdName es el ejecutable sin ruta, .exe ni sufijo de versión (python3.12 → python).
func cmdName(w string) string {
	n := exeName(w)
	if b := versionSuffix.ReplaceAllString(n, ""); b != "" && interpreters[b] {
		return b
	}
	return n
}

// nonFlags son los tokens que no empiezan por guion.
func nonFlags(t []string) []string {
	var out []string
	for _, w := range t {
		if w != "" && !strings.HasPrefix(w, "-") {
			out = append(out, w)
		}
	}
	return out
}

// matchFrozen devuelve la prueba congelada que arg nombra (con comodines), o "".
func matchFrozen(arg string, c Context) string {
	arg = rel(c.Root, arg)
	if arg == "" {
		return ""
	}
	glob := strings.ContainsAny(arg, "*?[")
	for _, f := range c.Frozen {
		if glob {
			if ok, _ := path.Match(arg, f); ok {
				return f
			}
		} else if arg == f {
			return f
		}
	}
	return ""
}

func matchAny(args []string, c Context) string {
	for _, a := range args {
		if f := matchFrozen(a, c); f != "" {
			return f
		}
	}
	return ""
}

// mentions devuelve la primera congelada que el texto nombra.
func mentions(text string, frozen []string) string {
	text = strings.ReplaceAll(text, `\`, "/")
	for _, f := range frozen {
		if strings.Contains(text, f) {
			return f
		}
	}
	return ""
}

// readPatch lee (hasta 1 MiB) el archivo de un parche; nil si no se puede.
func readPatch(c Context, p string) []byte {
	if !filepath.IsAbs(p) {
		p = filepath.Join(c.Root, p)
	}
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		return nil
	}
	return b
}

var redirPrefixes = []string{"&>>", "&>", ">>", ">|", "1>", "2>", ">"}

// frozenWrite evalúa un segmento: devuelve la prueba congelada que escribe.
// read=true si el motivo debe sugerir cat o grep (intérpretes, R6).
func frozenWrite(s string, c Context) (string, bool) {
	raw := quotedFields(s)
	for i, w := range raw {
		for _, pre := range redirPrefixes {
			if !strings.HasPrefix(w, pre) {
				continue
			}
			dst := strings.Trim(w[len(pre):], `"'`)
			if dst == "" && i+1 < len(raw) {
				dst = strings.Trim(raw[i+1], `"'`)
			}
			if dst != "" && !strings.HasPrefix(dst, "&") {
				if f := matchFrozen(dst, c); f != "" {
					return f, false
				}
			}
			break
		}
	}
	t := skipPrefix(slices.Clone(raw))
	if len(t) == 0 {
		return "", false
	}
	name := cmdName(t[0])
	args := t[1:]
	switch {
	case name == "sed" || name == "perl":
		for _, w := range args {
			if inplaceRe.MatchString(w) || strings.HasPrefix(w, "--in-place") {
				return matchAny(nonFlags(args), c), false
			}
		}
	case name == "tee":
		return matchAny(nonFlags(args), c), false
	case name == "cp" || name == "install":
		if nf := nonFlags(args); len(nf) > 0 {
			return matchFrozen(nf[len(nf)-1], c), false
		}
	case name == "patch" || name == "git" && gitSub(args) == "apply":
		var files []string
		for _, w := range nonFlags(args) {
			if w = strings.TrimLeft(w, "<"); w != "" && w != "apply" {
				files = append(files, w)
			}
		}
		if name == "patch" {
			if f := matchAny(files, c); f != "" {
				return f, false
			}
		}
		for _, w := range files {
			if f := mentions(string(readPatch(c, w)), c.Frozen); f != "" {
				return f, false
			}
		}
	case interpreters[name]:
		for _, w := range args {
			if evalFlagRe.MatchString(strings.ToLower(w)) {
				return mentions(s, c.Frozen), true
			}
		}
	}
	return "", false
}

func gitSub(args []string) string {
	for _, w := range args {
		if !strings.HasPrefix(w, "-") {
			return w
		}
	}
	return ""
}

// heredocWrites revisa los heredocs que alimentan a un intérprete, git apply o
// patch: si el cuerpo nombra una congelada, es una escritura.
func heredocWrites(cmd string, c Context) (string, bool) {
	if !strings.Contains(cmd, "<<") {
		return "", false
	}
	lines := strings.Split(cmd, "\n")
	for i := 0; i < len(lines); i++ {
		m := heredocMatch(lines[i])
		if m == nil {
			continue
		}
		head := lines[i]
		end := m[1] + m[2] + m[3]
		var body []string
		j := i + 1
		for ; j < len(lines) && strings.TrimSpace(lines[j]) != end; j++ {
			body = append(body, lines[j])
		}
		i = j
		for _, seg := range segSplit.Split(head, -1) {
			t := skipPrefix(quotedFields(seg))
			if len(t) == 0 {
				continue
			}
			n := cmdName(t[0])
			if interpreters[n] || n == "patch" || n == "git" && gitSub(t[1:]) == "apply" {
				if f := mentions(strings.Join(body, "\n"), c.Frozen); f != "" {
					return f, interpreters[n]
				}
				break
			}
		}
	}
	return "", false
}

// heredocsAll aplica heredocWrites al comando y a cada cuerpo de bash -c.
func heredocsAll(cmd string, c Context, depth int) (string, bool) {
	if p, r := heredocWrites(cmd, c); p != "" {
		return p, r
	}
	if depth >= 3 {
		return "", false
	}
	for _, seg := range splitCommand(cmd) {
		if inner, ok := unwrapShell(strings.TrimSpace(seg)); ok {
			if p, r := heredocsAll(inner, c, depth+1); p != "" {
				return p, r
			}
		}
	}
	return "", false
}
