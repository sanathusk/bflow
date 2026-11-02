package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mkTree crea bajo un dir temporal los 10 pares con contenido mínimo válido:
// banner al par y sin enlaces cruzados. Devuelve la raíz.
func mkTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(p, content string) {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, pr := range pairs {
		es, en := pr[0], pr[1]
		wantEn := filepath.Base(en)
		if strings.HasPrefix(es, "docs/") {
			wantEn = "../" + strings.TrimPrefix(en, "docs/")
		}
		wantEs := filepath.Base(es)
		if strings.HasPrefix(en, "docs/") {
			wantEs = "../" + strings.TrimPrefix(es, "docs/")
		}
		write(es, "# T\n> [English]("+wantEn+")\n\nVer [local](vecino.md).\n")
		write(en, "# T\n> [Español]("+wantEs+")\n\nSee [local](neighbour.md).\n")
		// Destinos de los enlaces "locales" para que no cuelguen.
		write(filepath.Join(filepath.Dir(es), "vecino.md"), "# V\n")
		write(filepath.Join(filepath.Dir(en), "neighbour.md"), "# N\n")
	}
	return root
}

func contains(errs []string, sub string) bool {
	for _, e := range errs {
		if strings.Contains(e, sub) {
			return true
		}
	}
	return false
}

func TestCheckOK(t *testing.T) {
	if errs := check(mkTree(t)); len(errs) != 0 {
		t.Fatalf("errores inesperados: %v", errs)
	}
}

func TestCheckMissingPair(t *testing.T) {
	root := mkTree(t)
	if err := os.Remove(filepath.Join(root, "docs/en/numbers.md")); err != nil {
		t.Fatal(err)
	}
	if errs := check(root); !contains(errs, "missing file docs/en/numbers.md") {
		t.Fatalf("se esperaba missing file, got: %v", errs)
	}
}

func TestCheckLeak(t *testing.T) {
	root := mkTree(t)
	p := filepath.Join(root, "docs/es/guia.md")
	b, _ := os.ReadFile(p)
	b = append(b, "\nFuga [x](../en/other.md).\n"...)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if errs := check(root); !contains(errs, "leaks into English") {
		t.Fatalf("se esperaba leak, got: %v", errs)
	}
}

func TestCheckDangling(t *testing.T) {
	root := mkTree(t)
	p := filepath.Join(root, "docs/en/guide.md")
	b, _ := os.ReadFile(p)
	b = append(b, "\nRoto [x](noexiste.md).\n"...)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if errs := check(root); !contains(errs, "dangling link to noexiste.md") {
		t.Fatalf("se esperaba dangling, got: %v", errs)
	}
}

func TestCheckBanner(t *testing.T) {
	root := mkTree(t)
	p := filepath.Join(root, "docs/es/guia.md")
	if err := os.WriteFile(p, []byte("# Sin banner\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if errs := check(root); !contains(errs, "has no banner/link") {
		t.Fatalf("se esperaba banner, got: %v", errs)
	}
}
