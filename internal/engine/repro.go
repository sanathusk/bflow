package engine

import (
	"context"
	"strings"

	"innobytes.tech/bflow/internal/flow"
	"innobytes.tech/bflow/internal/guard"
	"innobytes.tech/bflow/internal/store"
)

// reproMissing devuelve el rechazo repro_test_missing si la rama del hotfix no
// trae un commit de solo pruebas cuyo check falló en check.test_step.
func (e *Engine) reproMissing(ctx context.Context, rec store.Record) *flow.Rejection {
	id := rec.Flow.ID
	step := e.Cfg.Check.TestStep
	if step == "" {
		step = "test"
	}
	miss := func(key string, a ...any) *flow.Rejection {
		return &flow.Rejection{Code: "repro_test_missing", Reason: e.loc().T("engine.repro."+key, a...)}
	}
	known := false
	for _, st := range e.Cfg.Check.Steps {
		if st.Name == step {
			known = true
		}
	}
	if !known {
		return miss("unknown_test_step", step)
	}
	base := e.diffBase()
	if base == "" {
		base = rec.StartSHA
	}
	if base == "" {
		return miss("missing_base_branch")
	}
	commits, err := e.Git.Commits(ctx, base)
	if err != nil {
		return miss("list_commits_failed", err)
	}
	head, err := e.Git.HeadSHA(ctx)
	if err != nil {
		return miss("read_head_failed", err)
	}
	var cands []string
	for _, sha := range commits {
		if sha == head { // HEAD no cuenta; si es un merge ni siquiera viene en la lista
			continue
		}
		files, err := e.Git.CommitKept(ctx, sha)
		if err != nil {
			return miss("read_commit_files_failed", short(sha), err)
		}
		if len(files) == 0 {
			continue
		}
		onlyTests := true
		for _, f := range files {
			if !guard.MatchesTest(e.Cfg.Guard.TestPatterns, f) {
				onlyTests = false
				break
			}
		}
		if onlyTests {
			cands = append(cands, sha)
		}
	}
	if len(cands) == 0 {
		return miss("no_test_only_commit", id, step)
	}
	var unchecked []string
	for _, sha := range cands {
		found, status, err := e.Checks.StepAt(ctx, id, sha, step)
		if err != nil {
			return miss("read_check_failed", short(sha), err)
		}
		if !found {
			unchecked = append(unchecked, short(sha))
			continue
		}
		if status == "fail" {
			return nil
		}
	}
	if len(unchecked) == len(cands) {
		return miss("all_unchecked", strings.Join(unchecked, ", "), id, step)
	}
	if len(unchecked) > 0 {
		return miss("partial_unchecked", step, strings.Join(unchecked, ", "), id, step)
	}
	return miss("step_never_failed", step, id)
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
