package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"innobytes.tech/bflow/internal/adapters/secrets/keyring"
	ghtracker "innobytes.tech/bflow/internal/adapters/tracker/github"
	"innobytes.tech/bflow/internal/adapters/tracker/local"
	"innobytes.tech/bflow/internal/adapters/tracker/plane"
	gitad "innobytes.tech/bflow/internal/adapters/vcs/git"
	"innobytes.tech/bflow/internal/adapters/vcs/github"
	"innobytes.tech/bflow/internal/check"
	"innobytes.tech/bflow/internal/cli"
	"innobytes.tech/bflow/internal/config"
	"innobytes.tech/bflow/internal/engine"
	"innobytes.tech/bflow/internal/i18n"
	"innobytes.tech/bflow/internal/secrets"
	"innobytes.tech/bflow/internal/store"
	"innobytes.tech/bflow/internal/tracker"
	"innobytes.tech/bflow/internal/vcs"
)

// build arma el engine con los adaptadores que pide la configuración del repo.
func build(dir string) (*engine.Engine, error) {
	cfg, err := config.Load(dir)
	if err != nil {
		return nil, err
	}
	sec := secrets.Resolver{Store: secretStore(), Getenv: os.Getenv}
	var g *gitad.Git
	if isGitRepo(cfg.Root) {
		g = gitad.New(cfg.Root)
		g.Remote = cfg.VCS.Remote
		g.Protected = cfg.VCS.ProtectedBranches
	}
	tr, err := buildTracker(cfg, g, sec)
	if err != nil {
		return nil, err
	}
	e := &engine.Engine{Cfg: cfg, Store: store.Open(cfg.Root), Tracker: tr, Now: time.Now}
	if bundle != nil {
		e.Loc = bundle.New(i18n.DetectLang(cfg.UI.Lang))
	}
	if g != nil {
		e.Git = g
	}
	if h, err := buildHost(cfg, g, sec); err != nil {
		return nil, err
	} else if h != nil {
		e.Host = h
	}
	e.User = currentUser(g)
	if g != nil && len(cfg.Check.Steps) > 0 {
		v := check.Verifier{Store: e.Store, Git: g, CodePaths: cfg.Check.CodePaths}
		e.Verifier, e.Checks, e.Contract = v, v, v
	}
	return e, nil
}

func isGitRepo(dir string) bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = dir
	return cmd.Run() == nil
}

func buildTracker(cfg *config.Config, g *gitad.Git, sec secrets.Resolver) (tracker.Tracker, error) {
	switch cfg.Tracker.Adapter {
	case "plane":
		token, _, _ := sec.Get(secrets.Key("plane", cfg.Tracker.URL), "BFLOW_PLANE_TOKEN")
		return plane.New(plane.Options{URL: cfg.Tracker.URL, Workspace: cfg.Tracker.Workspace, Project: cfg.Tracker.Project,
			Token: token, States: cfg.Tracker.States, CachePath: filepath.Join(cfg.Root, ".bflow", "cache", "plane.json")}), nil
	case "github":
		repo, api, token, err := githubAccess(cfg, g, sec)
		if err != nil {
			return nil, err
		}
		return ghtracker.New(ghtracker.Options{API: api, Repo: repo, Prefix: cfg.Tracker.Prefix, Project: cfg.Tracker.Project,
			StartField: cfg.Tracker.StartField, Token: token, States: cfg.Tracker.States,
			CachePath: filepath.Join(cfg.Root, ".bflow", "cache", "github.json")}), nil
	case "local":
		path := cfg.Tracker.Path
		if path == "" {
			path = filepath.Join(".bflow", "local")
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(cfg.Root, path)
		}
		return local.New(path, cfg.Tracker.Project), nil
	}
	return nil, fmt.Errorf("el tracker %q todavía no está disponible en este binario", cfg.Tracker.Adapter)
}

func buildHost(cfg *config.Config, g *gitad.Git, sec secrets.Resolver) (vcs.Host, error) {
	if cfg.VCS.Host != "github" {
		return nil, nil
	}
	repo, api, token, err := githubAccess(cfg, g, sec)
	if err != nil {
		return nil, err
	}
	c := github.New(repo, token)
	if api != "" {
		c.API = api
	}
	c.Web = "https://github.com"
	return c, nil
}

// githubAccess es lo que el host y el tracker de GitHub comparten: el repo
// (vcs.repo o el del remoto), la API de vcs.api_url (vacía = pública) y el token
// de bflow connect github (GH_TOKEN/GITHUB_TOKEN, luego el llavero github:<host>).
func githubAccess(cfg *config.Config, g *gitad.Git, sec secrets.Resolver) (repo, api, token string, err error) {
	repo = cfg.VCS.Repo
	if repo == "" && g != nil {
		if url, err := g.RemoteURL(context.Background()); err == nil {
			_, repo, _ = gitad.ParseRemote(url)
		}
	}
	if repo == "" {
		return "", "", "", fmt.Errorf("vcs.host es github pero no se pudo deducir el repo: agrega vcs.repo (owner/nombre) a bflow.yaml")
	}
	host := "github.com"
	if cfg.VCS.APIURL != "" {
		host = cfg.VCS.APIURL
	}
	token, _, _ = sec.Get(secrets.Key("github", host), "GH_TOKEN", "GITHUB_TOKEN")
	return repo, cfg.VCS.APIURL, token, nil
}

func currentUser(g *gitad.Git) string {
	if v := os.Getenv("BFLOW_USER"); v != "" {
		return v
	}
	if g != nil {
		if u := g.UserName(context.Background()); u != "" {
			return u
		}
	}
	for _, k := range []string{"USER", "USERNAME"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return "desconocido"
}

// secretStore es el llavero del sistema, salvo que BFLOW_NO_KEYRING lo apague.
func secretStore() secrets.Store {
	if os.Getenv("BFLOW_NO_KEYRING") != "" {
		return nil
	}
	return keyring.Store{}
}

// connect valida una credencial con una llamada de solo lectura.
func connect(ctx context.Context, service string, o cli.ConnectOpts) (string, error) {
	switch service {
	case "plane":
		c := plane.New(plane.Options{URL: o.URL, Workspace: o.Workspace, Token: o.Token})
		ps, err := c.Projects(ctx)
		if err != nil {
			return "", err
		}
		var ids []string
		for _, p := range ps {
			ids = append(ids, p.ID)
		}
		return fmt.Sprintf("%d proyecto(s) visibles en %s: %s", len(ps), o.Workspace, strings.Join(ids, ", ")), nil
	case "github":
		c := github.New("", o.Token)
		if o.URL != "" {
			c.API = o.URL
		}
		login, err := c.User(ctx)
		if err != nil {
			return "", err
		}
		return "usuario " + login, nil
	}
	return "", fmt.Errorf("servicio %q no soportado", service)
}

// projects lista los proyectos de un tracker con la credencial guardada.
func projects(ctx context.Context, service, url, workspace string) ([]tracker.Project, error) {
	if service == "github" { // url es la API (vacía = pública) y workspace el repo owner/nombre
		sec := secrets.Resolver{Store: secretStore(), Getenv: os.Getenv}
		host := "github.com"
		if url != "" {
			host = url
		}
		token, _, err := sec.Get(secrets.Key("github", host), "GH_TOKEN", "GITHUB_TOKEN")
		if err != nil {
			return nil, fmt.Errorf("sin token de GitHub (bflow connect github)")
		}
		return ghtracker.New(ghtracker.Options{API: url, Repo: workspace, Token: token}).Projects(ctx)
	}
	if service != "plane" {
		return nil, fmt.Errorf("listar proyectos no está disponible para %s", service)
	}
	sec := secrets.Resolver{Store: secretStore(), Getenv: os.Getenv}
	token, _, err := sec.Get(secrets.Key("plane", url), "BFLOW_PLANE_TOKEN")
	if err != nil {
		return nil, fmt.Errorf("sin token de Plane para %s (bflow connect plane)", url)
	}
	return plane.New(plane.Options{URL: url, Workspace: workspace, Token: token}).Projects(ctx)
}
