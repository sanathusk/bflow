> 🌐 **Language / Idioma:** [Español](../es/guia.md) · **English**

# How to work with bflow

This guide is for anyone who has already installed bflow and configured a repo (see [README](../../README.en.md) and [installation](installation.md)). For foundational concepts (phases, lanes, `next`, guarantees), see [concepts](concepts.md); for `bflow.yaml` and trackers, see [configuration](configuration.md); for every command, see [commands](commands.md). It explains what you do and what bflow does at each step, how to tune agents, and what to do when something gets stuck.

## Who does what

| bflow (deterministic) | The agent (judgment) | You (decisions) |
|---|---|---|
| Task state, phases, and transitions | Writes spec and contract | Choose the lane |
| Creates branch, opens PR, closes task on merge | Implements, tests, and commits | Approve or request changes at each gate |
| Moves task in tracker and posts rejection notes | Reviews quality, security, and UX | Resolve decisions the agent cannot make |
| Runs check and blocks unsafe operations | Documents and prepares walkthrough | Inspect diff before opening PR |

If you haven't yet, run `bflow install claude` inside the repo: it sets up the skill and hooks. In Claude Code, tell the session something like "let's continue with API-12" or "what's next". The `bflow` skill runs `bflow status`, reads `next`, and carries out its instructions: prompts you, spawns agents, or waits. You don't have to memorize commands; every bflow response indicates the next step.

## Gates: what you see and decide

| Gate | Lane | When | What you see | What you decide |
|---|---|---|---|---|
| Lane | All (fixed) | At start, if `--lane` was omitted | Options: full, light, and hotfix | How large the change is |
| Discovery | `full` (fixed) | Before spec | The session asks about scope, data, errors, and constraints, up to 3 items per round | When no ambiguity remains |
| Spec | `full` and `light` (fixed) | Spec ready | Brief (≤35 lines) and, if the repo has a UI, the UI blueprint | Approve or request specific changes. New decisions `[N]` are ratified one by one |
| T1 Contract | `full` (conditional) | Only if bflow cannot validate contract automatically (see below) | `contract.md` | Approve, tweak, or return to spec. Once approved, these tests are frozen |
| Decision | All (conditional) | An agent cannot decide on its own | The issue and 2-3 options | Select an option or provide your own decision |
| Pause | All (conditional) | Implementation with green check, only if you ran `bflow pause <ID>` | — | Launch quality review or return to implementation |
| Rounds | All (conditional) | Quality review rejected twice in a row | — | Split feature, return to spec, or run another round |
| Budget | All (conditional) | Only with `flow.budget` in `bflow.yaml`, when a phase exceeds it | Phase spend vs. budget | Continue or block task |
| Repro | `hotfix` (conditional) | Implementer reported `DONE --no-repro "<reason>"` | The stated reason | Accept without reproduction test or request test |
| Product questions | All (fixed) | Everything reviewed and documented | Up to 4 questions about behavior, without telling you what code does | Answer in your own words (or skip) |
| Walkthrough | All (fixed) | After your answers | Your answers alongside review-map: 🔴 decisions, 🟡 logic, 🟢 mechanical | Approve and open PR, return to implementation, or return to spec |

Fixed gate counts per lane (full 5, light 4, hotfix 3) and conditions for each conditional gate are detailed in [concepts](concepts.md#gates). In `full`: when bflow validates the contract, no gate appears. If it passes, tests are frozen and task moves to implementing; otherwise, it rejects with `contract_stale` (missing commit or check), `contract_broken` (failure prior to tests), `contract_green` (T1 tests do not fail), or `contract_hollow` (empty or always-skipped tests). A T1 test counts as red only if its exact name appears in output (in Go, `--- FAIL: <Func>`; in other stacks, full file path). To test before quality review, run `bflow pause <ID>` in any phase prior to quality.

Everything needed to make decisions is printed directly in the chat: bflow provides the session with text (brief, contract, review-map) and the session displays it before prompting. If ever asked to approve something you haven't seen, ask to see it.

The walkthrough proceeds in two steps managed by bflow. First, you state what you expect the change to do without seeing the code. Next, the session compares your answers with what the code actually does, flags discrepancies, and walks through 🔴 changes with you one by one. This allows reviewing without reading the entire diff.

Prior to your answers, the walkthrough reports how much of the diff the reviewer opened: "reviewer read N of M diff files", lists unopened non-test/non-doc files (up to 20), and flags any 🔴 review-map paths not in the diff. bflow measures this using the reviewer's `Read` and Bash hooks; with APPROVED it rejects if any code or test file in the diff was not opened (excluding docs, `specs/`, lockfiles, and binaries; with under 1,500 lines a full `git diff` suffices), if any file in a 🔴 diff bullet point was unopened, if a 🔴 bullet point does not start with a backticked path, or if review-map lacks `## Docs` or a 🔴 bullet point lacks an indented `- paths:` list with outcomes reasoned by reviewer (`- paths: no new branches` if none); empty or placeholder `paths:` values (`none`, `n/a`, `-`) count as missing. A test that only checks source text as the sole criterion requires justification under `## Unexecuted tests`; without this, reviewer rejects it. If the hook does not capture `Read`s (`bflow doctor` warns of this), it reports "reviewer coverage: not measured" and does not enforce it. The documenter cannot close while a path under `## Docs` has not changed in the branch unless noted in `reports/docs.md` as "unchanged" with a valid reason.

Tasks are never manually closed: bflow closes them upon detecting merge (`bflow panel`). If completed on another machine or session, `bflow panel` also closes the local copy when the tracker marks it done/closed or its branch PR is merged, regardless of what phase it was in, reporting ("completed outside this copy").

## Tuning agents

Full agent configuration is in [configuration](configuration.md#agents); here is how to define their craft.

`bflow render` generates agents in `.claude/agents/bflow-<agent>.md` and, if `agent:` includes `opencode` (`agent: [claude, opencode]`), also in `.opencode/agents/bflow-<agent>.md`. For OpenCode, `bflow install opencode` sets up the `/bflow` command, and `models.opencode` translates aliases (`opus`, `sonnet`, `haiku`) to `provider/model` (in `bflow.yaml` for the team; in global config for yourself, mapping into your `opencode.json` without modifying versioned files); `render` also writes `.opencode/plugins/bflow.js` (1.x) and `.opencode/plugins/bflow2/` (2.x), providing OpenCode with guards and token accounting (if `bflow` is not in `PATH`, the guard does not run and the plugin warns once per session). OpenCode does not support subagent nudging or start hooks; this is covered by `bflow report`, `bflow check --verify`, and the AGENTS.md block. Commit these files so the entire team shares them, and avoid editing them manually: each file contains two sections:

- **Contract with bflow:** what the agent receives, what files it writes, what verdicts it can report and how. Provided by the flow engine and not configurable.
- **Craft:** how the agent performs its work. Comes with defaults that you can expand in `bflow.yaml`:

```yaml
agents:
  implementer:
    model: sonnet
    effort: medium
    read: [docs/architecture/, docs/testing.md]   # reads before starting
    extra: docs/bflow/implementer.md              # custom instructions
  reviewer:
    read: [docs/architecture/]
  documenter: { model: haiku, effort: low }
```

After modifying `bflow.yaml` or an `extra` file, run `bflow render` again. In CI, `bflow render --check` fails if this step was omitted.

What belongs in `extra`: repository-specific rules not documented elsewhere. For instance, commit conventions, test naming standards, stack tools agents must use, or antipatterns to avoid. Concrete instructions are far more effective than emphasis: "handlers return errors wrapped with `apperr.Wrap`" is much better than "ALWAYS handle errors properly".

With `read`, the agent skips loading CLAUDE.md: rules arrive through those specified paths, keeping initial context small. If your rules only exist in CLAUDE.md, avoid `read` or specify `omit_claude_md: false`.

To introduce a custom agent (such as a performance auditor), add it to a phase and supply its craft:

```yaml
flow:
  agents:
    quality: [reviewer, perf-auditor]
agents:
  perf-auditor:
    extra: docs/bflow/perf-auditor.md
```

bflow prepends the contract: in quality it writes `reports/perf-auditor.md` and reports APPROVED or REJECTED.

In quality, only the reviewer runs by default, which also inspects security, resilience, and performance. For an independent security review, set `flow: { security_audit: true }`: this activates `security-auditor` running in parallel with the reviewer, consuming its own tokens.

If a task changes interfaces consumed by external teams, the documenter generates a dedicated consumer changelog, committed and included in the PR. By default it sits beside the spec; if your repo has an established directory, specify it with `flow: { consumer_changelog: "docs/consumers/{id}-{slug}.md" }`. Use `flow.public_paths` to declare public surface paths; if touched by the diff without a changelog update, bflow rejects the documenter's DONE (`changelog_required`). If genuinely unnecessary, the documenter reports BLOCKED with the reason, and you can exempt it using `bflow unblock <ID> --no-changelog --note "reason"`.

Comments added by an agent should explain the "why" that code cannot convey (an invariant, a subtlety, an external requirement). They must not narrate the task, spec, or validation history: reviewers tend to trust the comment rather than inspecting code, and agents in subsequent rounds treat them as ground truth. Persistent notes belong in `reports/impl.md` and the PR. bflow enforces this: see `narrative_comment` under "When something gets stuck".

## Skills: which help and which conflict

bflow operates independently of installed skills: state, gates, and rules live in the CLI and cannot be bypassed. However, a skill can confuse agents if it asks them to do manually what bflow handles automatically.

- **Helpful:** domain and craft skills, such as stack conventions, testing patterns, architecture guidelines, or design systems. Describe their activation by technical domain ("when writing HTTP handlers in Go"), not process.
- **Conflicting:** process skills, such as planning features, branching, creating PRs, updating tickets, or approving specs. Remove these from bflow repositories.

`bflow doctor` flags skills that look like process skills. This is heuristic; if a skill is safe, add it to `doctor.ignore_skills` in `bflow.yaml`.

Architecture rules in CLAUDE.md or documents specified in `read` are welcome: they define domain craft. If a rule contradicts the flow (e.g., "push directly to main"), `guard` blocks it and explains why to the agent.

## Reducing context usage

- **Context loaded at start is paid on every call.** CLAUDE.md (including `@imports`), rules without `paths:`, and automatic memory indices (`MEMORY.md`) are sent on every call of the main session. `bflow doctor` estimates their footprint and warns if a file exceeds ~4k tokens or total exceeds ~10k. Prune obsolete rules and keep index files to single-line links. Memory is truncated to 200 lines or 25 KB; anything past that is not loaded.
- **Conversations are disposable; state lives in bflow.** Status, decisions, and reports live in `.bflow/`, specs, and tracker. After approving a gate, run `/clear`: upon restarting, bflow provides the session with the active task and next step.
- **Avoid pasting specs or code dumps into chat.** Agents read by file path and section (`bflow show <ID> spec --section design`).
- **Tailor agent model and effort:** lightweight mechanical tasks (documenter) should use smaller models with low effort.
- `bflow stats <ID>` displays tokens per phase, agent (main session separated), and model, distinguishing new tokens from re-read cache (cache hits cost ~10% of input tokens). Use it to measure if config changes genuinely reduced spend. `bflow stats <ID> --calls` adds a breakdown per model call per run (agent or main session); headers format as "agent · phase" (e.g., `implementer · contract`, with `#2` if repeated), showing context for each call and cumulative counts. How to read it: tokens re-read on call N represent the context of call N-1, as each call re-reads prior context from cache; this is why cumulative cache grows much faster than new tokens. The web UI displays a table under Tokens, one row per agent with runs underneath; clicking a run opens the per-call table. Tasks logged before per-call tracing will indicate data is unavailable. The "by agent" table in `bflow stats <ID>` adds two columns: `written` (total cache written by agent) and `prefix` (average tokens written on first call of each run; "-" if no per-call breakdown). Consistent `prefix` values across runs indicate stable prompt prefixes (instructions and tools) with high cache reuse; fluctuating values indicate varying starting context. Cache lasts several minutes: runs starting after expiration will write cache anew.

## When something gets stuck

| Situation | What happens | What to do |
|---|---|---|
| Command exits with code 2 | Action not permitted at this stage (e.g. approving an unpending gate) | Read the message; repeating the command will yield the same rejection |
| `guard` blocked an action | Agent attempted unsafe action or one managed by bflow | Nothing: agent receives explanation and alternative instruction. Subagents trying `approve`, `reject`, `unblock`, `start`, or `new` are told decisions belong to humans and must finish via `bflow report` |
| OpenCode inactive or guard not triggering | Plugin failed to load or provider encountered error (Global regions, self-signed certificates) leaving agent idle | Run `bflow doctor`: inspects `log`, `provider`, and `models` in OpenCode log (specifying blocked agents/models and how to reconfigure). Issues and fixes detailed in `adapters/opencode/README.md` under "Common Issues" |
| DONE rejected with `check_required` | No green check on current commit | Agent runs `bflow check` and commits |
| DONE rejected with `repro_test_missing` (hotfix) | No test-only commit prior to fix failed `check.test_step`; message indicates which of four cases occurred | Agent commits test, runs `bflow check` (red), then applies fix; if test commit lacks check run: `git switch --detach <sha> && bflow check <ID> && git switch -`. If test impossible: `report DONE --no-repro "<reason>"` for human decision at `repro` gate |
| DONE rejected with `narrative_comment` | Comment in diff references task ID, `R<n>`, `T<n>`, spec, "according to", "decision", or walkthrough | Agent removes or refactors it as explanation of intent/rationale, commits, and reports DONE again. `bflow check --verify` fails similarly (`not_verified`). If false positive (e.g. YAML with `# T1`, OpenAPI spec), adjust `flow.comment_patterns` or disable `flow.narrative_comments` |
| DONE rejected with `frozen_changed` | Test frozen during contract approval was modified | If accidental, revert changes. If intentionally changed, run `bflow freeze` from your terminal (agents cannot run this) |
| Implementer requests modifying frozen test (NEEDS_DECISION) | Contract test incorrect or outdated | If approved, run `bflow freeze --allow <file>` from your terminal: implementer can change it once, refreezing on next report DONE |
| `freeze_allow` gate appears after approving option | `next` pauses implementer until you run `bflow freeze --allow <file>`: guard prevents agents from running it | Run command in terminal and select "Already ran it" (or `bflow status`); if delayed, "Postpone" blocks task |
| Decision requests spawning another agent or manual verification (implementer unable to perform it) | Option would re-spawn same agent unable to resolve it | Choose "Handled by main session" (`bflow approve <ID> --gate decision --manual --note "<instructions>"`): task is blocked. Complete action and run `bflow unblock <ID> --note "<result>"`; agent receives note in args |
| Check reports `vulns (preexisting)` | Vulnerabilities in untouched dependencies | You decide: accept in step's `accept_file` with review date and file separate task, or have implementer bump dependency here. `accept_file` is human-edited only |
| Task blocked: "ended 3 times without reporting" | Agent exited multiple times without reporting to bflow | Inspect work; `bflow unblock <ID> [--note "<result>"]` relaunches agent; note passed in args |
| Switching models or tools mid-feature | State and gates persist in bflow | In alternative tool, run `bflow status --json` and follow `next`. Without hooks you lose `guard` and token tracking, but retain checks and frozen tests |
| Tracker unresponsive | Update queued and retried in sequence | `bflow sync` or subsequent command retries |

## Measuring

All metric commands are listed in [commands](commands.md#metrics); token accounting across tasks is detailed below.

Published reference benchmark using bflow on its own repository: [numbers](numbers.md).

While working, `bflow watch --open` opens a live terminal dashboard: active step (who is working or which gate awaits you), next action, elapsed times, tokens per agent, and recent events. If `flow.budget` is set in `bflow.yaml`, it displays "budget: <spend> / <budget> new" per phase in amber when exceeded. With `flow.loop_repeats` > 0, repeated subagent actions trigger "loop: <tool> repeated N times". To launch automatically with `bflow start`, set `ui: { watch: true }` in global config (`%AppData%\bflow\config.yaml` on Windows, `~/.config/bflow/config.yaml` on Linux/macOS) or `bflow.yaml`. Initial `bflow init` prompts for this preference.

Every model call is attributed to the task active in that session: running `bflow <ID>` or launching a `bflow-*` subagent bills tokens to that task. Calls without an active task (such as answering casual chat queries without bflow commands) accumulate under `no task` in dashboard. Persistent calls under `no task` suggest misconfigured guards or agents.

If using a browser, `bflow ui` serves the dashboard locally. It includes lane visualization where each phase represents a station and the train marks current progress. The responsive single-screen layout (from 1180x720) includes a left sidebar listing all bflow repositories ordered by urgency (those waiting on decisions first). Rows can be hidden via "Remove" (with 6s "Undo"); hidden repos appear under "Hidden (n)" with "Show" to restore, turning amber if action is pending. Hiding does not deregister repositories. The sidebar collapses to the lane line; on narrow screens (<1180px) it opens via "Repos". With `ui: { web: true }` in global config, the web dashboard opens automatically on task start.

`bflow stats <ID>` separates phase durations into agent work, human wait, and blocked time, tracking:

- **Quality:** gate rejections, quality rounds, and linked hotfixes (`bflow start <ID> --lane hotfix --fixes <feature>`).
- **Friction:** rejected flow requests, `guard` blocks, and agents terminating without reporting. Increases suggest tooling confusion: review skills and rules.
- **Review:** a `review:` summary line detailing latest reviewer coverage ("reviewer read N of M diff files", or "not measured"). In `--json` found under `data.stats.review`.
- **Cost:** new and cache tokens per phase, agent, and model.

Without an ID, `bflow stats` displays aggregate metrics across all tasks. Unattributed calls appear under `no task`.

If excessive calls accumulate under `no task`:

- Confirm the agent session executed `bflow <ID>` at task start.
- Verify guard settings allow bflow commands.
- Ensure subagent prompts include the task ID in JSON (`"id":"<ID>"`).

Before removing workflow phases to save tokens, compare metrics across several features: if quality metrics do not degrade, the phase may have been redundant.
