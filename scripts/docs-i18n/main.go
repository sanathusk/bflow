// Command docs-i18n verifica la paridad de la documentación bilingüe
// (carpetas docs/es y docs/en con slugs traducidos; ver CONTRIBUTING.md,
// "Documentación bilingüe"). Falla si falta un par, si el banner no enlaza
// al par, si hay enlaces que cruzan de idioma o si un destino .md no existe.
//
// Uso (desde la raíz del repo):
//
//	go run ./scripts/docs-i18n
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// pairs son los pares ES:EN, relativos a la raíz del repo. La tabla es
// explícita porque los slugs están traducidos (instalacion ↔ installation).
var pairs = [][2]string{
	{"docs/es/cifras.md", "docs/en/numbers.md"},
	{"docs/es/comandos.md", "docs/en/commands.md"},
	{"docs/es/como-probar-y-reportar.md", "docs/en/how-to-test-and-report.md"},
	{"docs/es/conceptos.md", "docs/en/concepts.md"},
	{"docs/es/configuracion.md", "docs/en/configuration.md"},
	{"docs/es/guia.md", "docs/en/guide.md"},
	{"docs/es/instalacion.md", "docs/en/installation.md"},
	{"docs/es/por-que-y-rumbo.md", "docs/en/why-and-roadmap.md"},
	{"README.md", "README.en.md"},
	{"adapters/claude/README.md", "adapters/claude/README.en.md"},
}

var linkRe = regexp.MustCompile(`\]\(([^)]*)\)`)

// linkTargets devuelve los destinos crudos de los enlaces [texto](destino).
func linkTargets(data string) []string {
	var out []string
	for _, m := range linkRe.FindAllStringSubmatch(data, -1) {
		out = append(out, m[1])
	}
	return out
}

func isExternal(t string) bool {
	return strings.HasPrefix(t, "http") || strings.HasPrefix(t, "mailto:")
}

// check verifica todos los pares bajo root y devuelve la lista de errores.
func check(root string) []string {
	var errs []string
	errf := func(format string, args ...any) {
		errs = append(errs, "docs-i18n: ERROR: "+fmt.Sprintf(format, args...))
	}
	read := func(p string) (string, bool) {
		b, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			errf("missing file %s", p)
			return "", false
		}
		return string(b), true
	}

	for _, pr := range pairs {
		es, en := pr[0], pr[1]
		esData, ok1 := read(es)
		enData, ok2 := read(en)
		if !ok1 || !ok2 {
			continue
		}

		// 1. Banner: cada archivo debe enlazar a su par.
		wantEn := filepath.Base(en)
		if strings.HasPrefix(es, "docs/") {
			wantEn = "../" + strings.TrimPrefix(en, "docs/")
		}
		wantEs := filepath.Base(es)
		if strings.HasPrefix(en, "docs/") {
			wantEs = "../" + strings.TrimPrefix(es, "docs/")
		}
		if !strings.Contains(esData, "]("+wantEn+")") {
			errf("%s has no banner/link to %s", es, wantEn)
		}
		if !strings.Contains(enData, "]("+wantEs+")") {
			errf("%s has no banner/link to %s", en, wantEs)
		}

		// 2. Sin fugas entre idiomas (fuera del banner).
		for _, t := range linkTargets(esData) {
			if isExternal(t) {
				continue
			}
			if strings.Contains(t, "../en/") && !strings.Contains(t, wantEn) {
				errf("%s leaks into English: %s", es, t)
			}
		}
		for _, t := range linkTargets(enData) {
			if isExternal(t) {
				continue
			}
			if strings.Contains(t, "../es/") && !strings.Contains(t, wantEs) {
				errf("%s leaks into Spanish: %s", en, t)
			}
		}
		// Pares fuera de docs/: el lado ES no referencia *.en.md salvo a su par.
		if !strings.HasPrefix(es, "docs/") {
			for _, t := range linkTargets(esData) {
				if isExternal(t) {
					continue
				}
				if strings.Contains(t, ".en.md") && !strings.Contains(t, filepath.Base(en)) {
					errf("%s leaks into English: %s", es, t)
				}
			}
		}

		// 3. Todo destino .md relativo debe existir en disco.
		for _, f := range []struct {
			path, data string
		}{{es, esData}, {en, enData}} {
			dir := filepath.Dir(f.path)
			for _, t := range linkTargets(f.data) {
				base, _, _ := strings.Cut(t, "#")
				if base == "" || isExternal(base) || strings.HasPrefix(base, "/") {
					continue
				}
				if !strings.HasSuffix(base, ".md") {
					continue
				}
				if _, err := os.Stat(filepath.Join(root, dir, base)); err != nil {
					errf("%s has dangling link to %s", f.path, t)
				}
			}
		}
	}
	return errs
}

func main() {
	if errs := check("."); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, e)
		}
		fmt.Fprintln(os.Stderr, "docs-i18n: FAILED — fix the errors above (see CONTRIBUTING.md \"Documentación bilingüe\").")
		os.Exit(1)
	}
	fmt.Printf("docs-i18n: OK — %d pairs, banners and in-locale links check out.\n", len(pairs))
}
