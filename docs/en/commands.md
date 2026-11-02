> 🌐 **Language / Idioma:** [Español](../es/comandos.md) · **English**

# Commands

`bflow help` lists all commands. Any command accepts `--json` and responds with the same envelope (see [`next`](concepts.md#the-agent-contract-next)). Exit codes: `0` success, `1` error, `2` flow rejection. Square brackets indicate optional parameters; if `ID` is omitted, the active task is used.

## Flow

| Command | What it does |
|---|---|
| `status [ID] [--brief]` | Status and next step. |
| `new --lane full\|light\|hotfix --title "t" [--file idea.md] [--slug s]` | Creates a task and starts it. |
| `start <ID> --lane full\|light\|hotfix [--slug s] [--fixes ID]` | Starts a task in a lane; `--fixes` links a hotfix to the feature it corrects. |
| `approve [ID] [--gate g] [--choice n] [--note t] [--file f \| --stdin]` | Approves the pending gate. With `--gate budget`, "Yes" continues the phase. `--stdin` (discovery gate only) receives full discovery via stdin, up to 64 KiB; bflow writes it into `## Discovery` of the spec. `--file` continues to work. |
| `reject [ID] --note "reason" [--gate g] [--to phase]` | Rejects the pending gate. With `--gate budget`, "No" blocks the task with the note without advancing the phase. In `quality` and `documenting`, without a pending gate, sends the task back to `implementing` (`--note` mandatory). |
| `report [ID] --agent a --verdict V [--note] [--file] [--option o]... [--stdin] [--no-repro "reason"]` | An agent reports its verdict (`--stdin`: scout report). `--no-repro`: only implementer `DONE` in hotfix; closes without reproduction test and opens the human gate `repro` (`approve --gate repro` accepts; `reject --gate repro --note` requests the test). |
| `block [ID] --reason "reason"` | Blocks the task. |
| `unblock [ID] [--note t] [--no-changelog]` | Unblocks the task and returns to its phase. With `--no-changelog` (requires `--note`), exempts the documenter from changelog when diff touches `flow.public_paths`; reason is recorded in the log. Human only. |
| `pause [ID]` | Requests stopping before quality: when implementing finishes, the `pause` gate opens for you to test. Can be repeated; in quality or later, `pause_late` is shown. Human only. |
| `drop <ID> --note "reason"` | Drops a task without finishing it. |
| `freeze [ID] [--allow <file>]` | Re-freezes contract tests as they currently are; `--allow` lets you modify one once. Human only. |
| `show [ID] task\|brief\|spec\|contract\|review-map\|questions\|decisions\|discovery\|check\|scout [--section s]` | Shows an artifact. |
| `task add "title" [--description t]` | Creates a task (trackers that support it, such as local). |
| `task list` | Lists open tasks in the tracker. |
| `sync [ID]` | Retries pending changes with tracker. |
| `import --from harness [--dir path] [--state-only] [--dry-run]` | Imports previous harness once. |

## Agents

| Command | What it does |
|---|---|
| `render [--check]` | Generates bflow agents for the repo's tool; `--check` fails if they do not match config. |
| `install claude\|opencode [--skill-only]` | Installs skill (or command) and bflow hooks for your tool. |

## Git and PR

| Command | What it does |
|---|---|
| `pr [ID]` | Publishes branch and opens or updates the PR for a task in `in_review`. |
| `panel [--sla]` | Commitments: closes merged or finished tasks in tracker even if in another phase, syncs, and alerts on overdue gates. |

## Quality

| Command | What it does |
|---|---|
| `check [ID] [--quick pkg] [--verify]` | Deterministic gate check. |
| `env check [--quiet]` | Verifies API, ports, and environment variables in local environment. |
| `guard` | PreToolUse hook: reads action via stdin and blocks it (exit code 2) if it breaks a rule. |
| `eval reviewer [--craft ref] [--case c]... [--abort] [--from-fixes]` | Measures known defects caught by reviewer. Runs one case at a time with `bflow-eval-reviewer` agent and writes `.bflow/evals/<date>.json` with caught, uncaught, possible false positives, tokens, and time. Without flags, resumes open run. |
| `eval reviewer report <case> --verdict APPROVED\|REJECTED [--note t]` | Used by eval agent to report its case; not run manually. |

`eval reviewer`:

- `--craft <ref>`: reads reviewer craft from that git ref (empty = working tree). Used to compare crafts by running eval with each version.
- `--case <name>`: runs only that case (repeatable).
- `--abort`: cancels open run and removes worktrees and directory. Writes no result.
- `--from-fixes`: creates a `draft: true` skeleton in `.bflow/evals/reviewer/` for each hotfix started with `start --fixes`, with `base` and `head` populated when possible. Complete `defects` and remove `draft` manually.

Cases live in `.bflow/evals/reviewer/<case>/case.yaml`: `base` and `head` (commits), `defects` (`file`, `lines` as `N` or `N-M`, `what`), and optional `spec`. A malformed case rejects the run with `case_invalid` without creating anything. `.bflow/evals/` directories are versioned; the rest of `.bflow/` is not.

## Metrics

| Command | What it does |
|---|---|
| `stats [ID] [--calls] [--reads]` | Time per phase (agent, human, blocked), iterations, and tokens. Without ID, one line per task. |
| `statusline` | Single line for status bar (reads cache: milliseconds). |
| `watch [--interval 2s] [--once] [--open] [--web]` | Live task dashboard. |
| `ui [--port 7719] [--no-open]` | Browser dashboard across all your repositories. |

`stats` with ID additionally supports:

- `--calls`: table of model calls per run (agent or main session), with new context and re-read cache on each call. Shows where context repeats or if prefix varies across runs.
- `--reads`: which files were read and how many times, how many distinct agents touched them, and what percentage were re-reads. Without `reads-all.jsonl`, reports "no read log".

## Configuration

| Command | What it does |
|---|---|
| `init [--yes] [--profile p] [--tracker local\|plane\|github] [--project P]` | Creates `bflow.yaml` detecting stack, remote, base branch, and check steps (with github, `--project owner/N`). |
| `doctor` | Validates config, tools, connections, environment, and hooks. |
| `connect plane\|github [--token t] [--url u] [--workspace w] [--from-gh]` | Saves validated credential into keychain. |
| `profile add <name> [--tracker --url --workspace --host --base --agent]` | Creates or updates a global profile. |
| `profile list` | Lists global profiles. |
| `profile use <name>` | Uses a profile in this repo. |
| `tracker setup [--dry-run]` | Creates required states in tracker for each phase. |
| `tracker states` | Tracker states and phase mapping used by bflow for reading and writing (read-only). |

## Hooks

Installed by `bflow install`; not run manually.

| Command | What it does |
|---|---|
| `hook session-start` | Environment, commitments, and active task in a few lines. |
| `hook tokens` | Stop and SubagentStop hook: adds new tokens to the phase. In SubagentStop, also sums new calls from main session (without alerting or pruning marks); Stop alerts if task is waiting for a human. |
| `hook subagent-stop` | If a bflow agent finishes without reporting, makes it continue (maximum 2 times) and then blocks the task. |

## Maintenance

| Command | What it does |
|---|---|
| `update [--check] [--force]` | Updates bflow to latest published release. |
| `version` | bflow version. |
| `help` | Lists commands. |
