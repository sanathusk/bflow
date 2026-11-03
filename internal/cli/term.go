package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"innobytes.tech/bflow/internal/i18n"
)

// isTerminal indica si f es una consola interactiva.
func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// readSecret lee un secreto. En una consola no muestra lo que se escribe; si
// la entrada viene de un pipe, lee la primera línea (útil en CI).
func readSecret(in io.Reader, errw io.Writer, prompt string, loc ...*i18n.Localizer) (string, error) {
	f, isFile := in.(*os.File)
	if isFile && isTerminal(f) {
		fmt.Fprint(errw, prompt)
		restore := disableEcho(f)
		defer func() {
			restore()
			fmt.Fprintln(errw)
		}()
	}
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		l := i18n.DefaultLocalizer()
		if len(loc) > 0 && loc[0] != nil {
			l = loc[0]
		}
		return "", fmt.Errorf("%s", l.T("term.no_token_received", err))
	}
	return strings.TrimSpace(line), nil
}
