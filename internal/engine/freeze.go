package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"innobytes.tech/bflow/internal/flow"
	"innobytes.tech/bflow/internal/guard"
	"innobytes.tech/bflow/internal/i18n"
	"innobytes.tech/bflow/internal/store"
)

// FrozenFile guarda las pruebas congeladas de una tarea: ruta → hash del
// contenido al congelar. Las versiones anteriores guardaban solo la lista de
// rutas; esas se siguen leyendo, sin hash que comparar.
const FrozenFile = "frozen-tests.json"

// absent es el hash de una prueba que no existía al congelar.
const absent = "absent"

// allowed marca una prueba congelada que una persona dejó cambiar una vez: se
// vuelve a congelar cuando el implementer reporta DONE.
const allowed = "allow"

// freezeTests congela las pruebas que la tarea escribió o tocó hasta aprobar
// el contrato: desde ahí el implementer hace pasar esas pruebas sin cambiarlas.
func (e *Engine) freezeTests(ctx context.Context, id string) (int, error) {
	if e.Git == nil {
		return 0, nil
	}
	frozen := e.taskTests(ctx)
	return len(frozen), e.writeFrozen(id, frozen)
}

// taskTests son las pruebas que la rama agregó o cambió, más las que están
// sin commitear.
func (e *Engine) taskTests(ctx context.Context) []string {
	if e.Git == nil {
		return nil
	}
	base := e.diffBase()
	var files []string
	if base != "" {
		if d, err := e.Git.DiffNames(ctx, base); err == nil {
			files = append(files, d...)
		}
	}
	if d, err := e.Git.Dirty(ctx, nil); err == nil {
		files = append(files, d...)
	}
	var frozen []string
	for _, f := range files {
		f = strings.ReplaceAll(f, `\`, "/")
		if guard.MatchesTest(e.Cfg.Guard.TestPatterns, f) && !slices.Contains(frozen, f) {
			frozen = append(frozen, f)
		}
	}
	return frozen
}

// Refreeze vuelve a tomar el hash de las pruebas congeladas tal como están
// ahora. Es para una persona que cambió una prueba a propósito; guard impide
// que lo corra un agente.
func (e *Engine) Refreeze(id string) (int, error) {
	if _, err := e.Store.Load(id); err != nil {
		return 0, err
	}
	files := e.Frozen(id)
	if len(files) == 0 {
		return 0, errors.New(id + " no tiene pruebas congeladas")
	}
	if err := e.writeFrozen(id, files); err != nil {
		return 0, err
	}
	_ = e.Store.Append(store.Entry{TS: e.now(), ID: id, Event: "refreeze", By: e.User, Data: map[string]any{"files": len(files)}})
	return len(files), nil
}

// AllowFrozen deja cambiar una vez una prueba congelada. Es para una persona
// que aprobó el cambio (guard impide que lo corra un agente).
func (e *Engine) AllowFrozen(id, file string) error {
	if _, err := e.Store.Load(id); err != nil {
		return err
	}
	file = strings.ReplaceAll(filepath.ToSlash(filepath.Clean(file)), `\`, "/")
	m := e.readFrozen(id)
	if _, ok := m[file]; !ok {
		return errors.New(file + " no es una prueba congelada de " + id + " (bflow status " + id + " --json las lista)")
	}
	m[file] = allowed
	if err := e.saveFrozen(id, m); err != nil {
		return err
	}
	_ = e.Store.Append(store.Entry{TS: e.now(), ID: id, Event: "freeze_allow", By: e.User, Data: map[string]any{"file": file}})
	return nil
}

// refreezeAllowed vuelve a congelar, con su contenido actual, las pruebas que
// se dejaron cambiar una vez.
func (e *Engine) refreezeAllowed(id string) {
	m := e.readFrozen(id)
	n := 0
	for f, h := range m {
		if h == allowed {
			m[f] = e.testHash(f)
			n++
		}
	}
	if n > 0 && e.saveFrozen(id, m) == nil {
		_ = e.Store.Append(store.Entry{TS: e.now(), ID: id, Event: "refreeze", By: e.User, Data: map[string]any{"files": n}})
	}
}

func (e *Engine) writeFrozen(id string, files []string) error {
	m := make(map[string]string, len(files))
	for _, f := range files {
		m[f] = e.testHash(f)
	}
	return e.saveFrozen(id, m)
}

func (e *Engine) saveFrozen(id string, m map[string]string) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := e.Store.WriteFile(id, FrozenFile, b); err != nil {
		return err
	}
	e.saveSnapshots(id, m)
	return nil
}

var hashNameRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// saveSnapshots deja en frozen-tests/ una copia de cada prueba congelada
// (nombrada por su hash) y poda las que ya nadie referencia (GH-86 R12). Es
// de mejor esfuerzo: sin copia, RestoreFrozen avisa en vez de restaurar.
func (e *Engine) saveSnapshots(id string, m map[string]string) {
	dir, err := e.Store.Path(id, FrozenSnapshots)
	if err != nil {
		return
	}
	keep := map[string]bool{}
	for f, h := range m {
		if !hashNameRe.MatchString(h) {
			continue
		}
		keep[h] = true
		if _, err := os.Stat(filepath.Join(dir, h)); err == nil || e.testHash(f) != h {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(e.Cfg.Root, filepath.FromSlash(f))); err == nil {
			_ = e.Store.WriteFile(id, FrozenSnapshots+"/"+h, b)
		}
	}
	ents, _ := os.ReadDir(dir)
	for _, en := range ents {
		if !keep[en.Name()] {
			_ = os.Remove(filepath.Join(dir, en.Name()))
		}
	}
}

func (e *Engine) readFrozen(id string) map[string]string {
	b, err := e.Store.ReadFile(id, FrozenFile)
	if err != nil {
		return nil
	}
	m := map[string]string{}
	if json.Unmarshal(b, &m) == nil {
		return m
	}
	var old []string
	if json.Unmarshal(b, &old) != nil {
		return nil
	}
	for _, f := range old {
		m[f] = ""
	}
	return m
}

// Frozen devuelve las pruebas congeladas de una tarea, sin las que una persona
// dejó cambiar una vez.
func (e *Engine) Frozen(id string) []string {
	m := e.readFrozen(id)
	out := make([]string, 0, len(m))
	for f, h := range m {
		if h != allowed {
			out = append(out, f)
		}
	}
	slices.Sort(out)
	return out
}

// FrozenChanged devuelve las pruebas congeladas cuyo contenido ya no es el
// del momento en que se congelaron. No depende de hooks: lo revisa el CLI al
// reportar DONE y en check --verify, sea cual sea la herramienta del agente.
func (e *Engine) FrozenChanged(id string) []string {
	var changed []string
	for f, h := range e.readFrozen(id) {
		if h != "" && h != allowed && e.testHash(f) != h {
			changed = append(changed, f)
		}
	}
	slices.Sort(changed)
	return changed
}

func frozenRejection(changed []string) *flow.Rejection {
	return &flow.Rejection{Code: "frozen_changed", Reason: "cambiaron pruebas congeladas al aprobar el contrato: " + strings.Join(changed, ", ") +
		". Déjalas como estaban; si una prueba está mal, reporta NEEDS_DECISION. Si una persona aprueba el cambio, que corra bflow freeze --allow <archivo> desde su terminal."}
}

// testHash normaliza los finales de línea para que un checkout con autocrlf
// no cuente como cambio.
func (e *Engine) testHash(rel string) string {
	b, err := os.ReadFile(filepath.Join(e.Cfg.Root, filepath.FromSlash(rel)))
	if err != nil {
		return absent
	}
	sum := sha256.Sum256(bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")))
	return hex.EncodeToString(sum[:])
}

// FrozenSnapshots es la carpeta, dentro de la tarea, con una copia de cada
// prueba congelada nombrada por su hash (GH-86 R12).
const FrozenSnapshots = "frozen-tests"

// FrozenRestore es lo que RestoreFrozen hizo con las pruebas congeladas que
// cambiaron: las que restauró, las que borró (no existían al congelar) y las
// que no pudo restaurar por falta de copia válida.
type FrozenRestore struct{ Restored, Deleted, Missing []string }

// Empty dice si no hubo nada que restaurar.
func (r FrozenRestore) Empty() bool {
	return len(r.Restored)+len(r.Deleted)+len(r.Missing) == 0
}

// RestoreFrozen devuelve las pruebas congeladas de la tarea a la versión que
// bflow copió al congelar (GH-86 R13, R14, R17).
func (e *Engine) RestoreFrozen(id string) FrozenRestore {
	var r FrozenRestore
	for f, h := range e.readFrozen(id) {
		if h == "" || h == allowed || e.testHash(f) == h {
			continue
		}
		abs := filepath.Join(e.Cfg.Root, filepath.FromSlash(f))
		if h == absent {
			if os.Remove(abs) == nil {
				r.Deleted = append(r.Deleted, f)
			} else {
				r.Missing = append(r.Missing, f)
			}
			continue
		}
		b, err := []byte(nil), errors.New("hash inválido")
		if hashNameRe.MatchString(h) {
			b, err = e.Store.ReadFile(id, FrozenSnapshots+"/"+h)
		}
		sum := sha256.Sum256(bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")))
		if err != nil || hex.EncodeToString(sum[:]) != h || os.MkdirAll(filepath.Dir(abs), 0o755) != nil || store.WriteAtomic(abs, b) != nil {
			r.Missing = append(r.Missing, f)
			continue
		}
		r.Restored = append(r.Restored, f)
	}
	slices.Sort(r.Restored)
	slices.Sort(r.Deleted)
	slices.Sort(r.Missing)
	if !r.Empty() {
		_ = e.Store.Append(store.Entry{TS: e.now(), ID: id, Event: "frozen_restore", By: e.User,
			Data: map[string]any{"restored": r.Restored, "deleted": r.Deleted, "missing": r.Missing}})
	}
	return r
}

// FrozenRestoreReason es el aviso al agente tras restaurar (o no poder).
func FrozenRestoreReason(r FrozenRestore, locs ...*i18n.Localizer) string {
	loc := i18n.DefaultLocalizer()
	if len(locs) > 0 && locs[0] != nil {
		loc = locs[0]
	}
	var parts []string
	if ch := slices.Concat(r.Restored, r.Deleted); len(ch) > 0 {
		parts = append(parts, loc.T("engine.frozen_restore_changed", strings.Join(ch, ", ")))
	}
	if len(r.Missing) > 0 {
		parts = append(parts, loc.T("engine.frozen_restore_missing", strings.Join(r.Missing, ", ")))
	}
	return strings.Join(parts, " ")
}
