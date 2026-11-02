// Command bflow es Byteflow by innobytes: motor de flujo Spec-Driven Development.
//
// Este paquete es la raíz de composición: el único lugar que conoce a la vez el
// núcleo (internal/...) y los adaptadores (internal/adapters/...).
package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"innobytes.tech/bflow/internal/adapters/agent/claude"
	"innobytes.tech/bflow/internal/adapters/agent/opencode"
	gitad "innobytes.tech/bflow/internal/adapters/vcs/git"
	"innobytes.tech/bflow/internal/cli"
	"innobytes.tech/bflow/internal/config"
	"innobytes.tech/bflow/internal/eval"
	"innobytes.tech/bflow/internal/i18n"
)

// version se inyecta con -ldflags "-X main.version=…".
var version = "dev"

var bundle *i18n.Bundle

func main() {
	dir, _ := os.Getwd()
	var err error
	bundle, err = i18n.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cfgLang := ""
	if cfg, err := config.Load(dir); err == nil {
		cfgLang = cfg.UI.Lang
	}
	loc := bundle.New(i18n.DetectLang(cfgLang))
	os.Exit(cli.Run(os.Args[1:], &cli.Env{
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
		Stdin:    os.Stdin,
		Version:  buildVersion(version, debug.ReadBuildInfo),
		Dir:      dir,
		Loc:      loc,
		Build:    build,
		Connect:  connect,
		Secrets:  secretStore(),
		Agent:    claude.Agent{},
		Tools:    []cli.ToolAdapter{claude.Agent{}, opencode.Agent{}},
		Projects: projects,
		EvalGit:  func(dir string) eval.Git { return gitad.New(dir) },
	}))
}
