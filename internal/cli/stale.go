package cli

import (
	"fmt"
	"os"
	"os/exec"

	"innobytes.tech/bflow/internal/i18n"
)

// binStamp identifica el bflow instalado en el PATH (tamaño y fecha). Cambia
// cuando go install o bflow update lo reemplazan; vacío si no se encuentra.
func binStamp() string {
	p, err := exec.LookPath("bflow")
	if err != nil {
		return ""
	}
	st, err := os.Stat(p)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d-%d", st.Size(), st.ModTime().UnixNano())
}

// staleCheck dice si el binario instalado cambió desde que arrancó el panel.
type staleCheck struct {
	stamp func() string
	start string
}

// newStaleCheck toma la huella del binario al arrancar el panel.
func newStaleCheck(stamp func() string) *staleCheck {
	return &staleCheck{stamp: stamp, start: stamp()}
}

// Stale dice si la huella actual difiere de la de arranque. Nil, huella
// inicial vacía o huella actual vacía nunca avisan.
func (s *staleCheck) Stale() bool {
	if s == nil || s.start == "" {
		return false
	}
	now := s.stamp()
	return now != "" && now != s.start
}

// staleNotice devuelve el aviso de binario nuevo, localizado si se pasa loc.
func staleNotice(loc ...*i18n.Localizer) string {
	l := i18n.DefaultLocalizer()
	if len(loc) > 0 && loc[0] != nil {
		l = loc[0]
	}
	return l.T("watch.stale_notice")
}
