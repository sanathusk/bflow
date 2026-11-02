# Byteflow · `bflow`

> 🌐 **Language / Idioma:** [Español](README.md) · **English**

**Spec-Driven Development workflow engine for AI agents.** A single Go binary that takes each task from idea to merged PR (discovery, spec, implementation, review, walkthrough; in the full lane, also an acceptance test contract frozen before code), decides transitions, speaks to your tracker and git, and tells the agent exactly what to do next in a few lines of JSON.

> Status: **v0.1.0**, the first published release. Works end-to-end with the local tracker, Plane, and GitHub, and with Claude Code and OpenCode as agents. The command API may change before 1.0. If you are going to test it, start with [how to test and report](docs/en/how-to-test-and-report.md).

---

## Why it exists

I had a harness per project and they fell out of sync; features took long, hallucinations happened, agents did not always follow the harness, and redundant documentation accumulated. In the two repos I measured, the harness spent around 7,000 fixed tokens per session and 73% to 80% of what was committed was state, not specifications. I built bflow for my team at Innobytes: spend fewer tokens and have everyone solve features consistently with spec-driven development, even if AI is non-deterministic.

Byteflow separates what **requires judgment** (understanding the problem, designing, coding, reviewing)—handled by the agent—from what is **deterministic** (state, transitions, git, tracker, tests, rules)—handled by the CLI. The agent does not need to remember the process: it asks `bflow` what is next and does it. Full background and roadmap: [Why and roadmap](docs/en/why-and-roadmap.md).

## Who it is for (and who it is not for)

bflow is opinionated on purpose: it has a defined flow and fixed human gates. It demonstrates a concrete way to work with agents.

**It works for you if:**

- You develop features with AI agents and want important decisions (what gets built, what gets merged) made by a person, without reading everything the agent outputs.
- You work with git and PRs, and use Claude Code or OpenCode as your agent.
- You want to know what each feature costs (time, tokens, iterations) and where it gets stuck.

**It is not for you if:**

- You want the agent to work end-to-end on its own without approvals.
- Your workflow does not use branches and PRs.
- You use another agent tool and need full guarantees: today only Claude Code and OpenCode have adapters (see [what it guarantees](docs/en/concepts.md#what-it-guarantees-and-what-it-does-not)).

## Install

Linux and macOS (in `~/.local/bin`):

```bash
curl -fsSL https://raw.githubusercontent.com/innobytestech/bflow/main/install.sh | sh
```

Windows (in `%LOCALAPPDATA%\Programs\bflow`):

```powershell
irm https://raw.githubusercontent.com/innobytestech/bflow/main/install.ps1 | iex
```

The scripts verify the SHA-256 of the archive before installing. Pinned version, Go, upgrade, and uninstall instructions: [Installation](docs/en/installation.md).

## Quick start

```bash
cd my-repo
bflow init                 # detects stack, remote, base branch, and proposes check steps
bflow doctor               # validates config, tools, connections, and hooks
bflow install claude       # or: bflow install opencode (skill or command, and hooks)
```

Without `bflow.yaml`, bflow runs with the local tracker and git only. To walk through a task:

```bash
bflow new --lane light --title "RFC validation" --file idea.md       # creates the task and starts it
bflow report LOCAL-1 --agent spec-author --verdict READY            # run by the agent
bflow show LOCAL-1 brief
bflow approve LOCAL-1                                                # creates the branch
# ... the implementer writes code and runs `bflow check` ...
bflow report LOCAL-1 --agent implementer --verdict DONE             # with check steps, requires a green check
bflow status
```

Each command prints the next step; with `--json`, that step arrives in `next`. In practice, you tell your agent "what's next" and it runs it. To connect Plane or GitHub: [Configuration](docs/en/configuration.md).

## What it guarantees

What lives in the CLI is enforced with any tool (state and gates are managed by bflow, `DONE` requires a green check, contract tests cannot change). What depends on hooks only applies where hooks exist (Claude Code and OpenCode): blocking `git reset --hard`, force push, `.env`, and `.bflow/`. bflow does not guarantee code quality or contain a malicious agent. Full table: [Concepts](docs/en/concepts.md#what-it-guarantees-and-what-it-does-not).

## What bflow does and does not do

Fixed human gates per lane: `full` has 5 (lane, discovery, spec, product questions, and walkthrough), `light` 4 (no discovery), and `hotfix` 3 (lane, questions, and walkthrough). Other gates open only in specific cases: decision, rounds, `contract` (full, only if bflow cannot validate the contract), `pause` (only with `bflow pause`), and `repro` (hotfix, only with `--no-repro`). Conditions and details: [Concepts](docs/en/concepts.md#gates).

- **Yes:** manages state and transitions, creates branches and opens PRs, requires a green check for `DONE`, freezes contract tests, and tracks time, tokens, and rejections.
- **Does not guarantee code quality:** you and the agents review it; bflow ensures those steps take place.
- **Does not contain a malicious agent:** `guard` is a rail against common mistakes, not a sandbox.
- **Does not cover everything in Bash:** `guard` recognizes the commands and paths listed in [Concepts](docs/en/concepts.md#what-it-guarantees-and-what-it-does-not); an obscure command might get through.

## Real numbers

There are two benchmarks. The team benchmark (rc.9): 51 tasks completed across 6 repos, 48 by the author and 3 by other devs, with post-merge quality not yet measured; the table and notes are in [Team numbers](docs/en/why-and-roadmap.md#team-numbers). The benchmark of bflow building bflow (58 tasks, cutoff 2026-10-16): median 33m wall-clock time, 14m agent time, and 15m waiting for a human; per-task breakdown in [Numbers](docs/en/numbers.md).

There is no comparison with/without bflow, and this is not a savings guarantee. To measure yours: `bflow stats`.

## Roadmap

Today: v0.1.0, the first announced release. Planned: [v0.1.1](https://github.com/innobytestech/bflow/milestone/8), improvements from v0.1.0; [Milestone 2 · Outreach](https://github.com/innobytestech/bflow/milestone/6); [Milestone 3 · Post-launch](https://github.com/innobytestech/bflow/milestone/7); and 1.0 with a stable command API. Criteria for each milestone: [Why and roadmap](docs/en/why-and-roadmap.md#roadmap).

## Documentation

| Document | Purpose |
|---|---|
| [Why and roadmap](docs/en/why-and-roadmap.md) | Motivation, goals, and milestones. |
| [Installation](docs/en/installation.md) | Requirements, install, update, verify, uninstall. |
| [Concepts](docs/en/concepts.md) | Core and adapters, phases and lanes, gates, `next`, artifacts left in repo, guarantees. |
| [How to work with bflow](docs/en/guide.md) | Day-to-day guide: what you do at each gate, tuning agents, resolving blockers. |
| [Configuration](docs/en/configuration.md) | `bflow.yaml`, profiles, trackers, agents, check, Claude Code, and OpenCode. |
| [Commands](docs/en/commands.md) | Reference for all commands. |
| [Numbers](docs/en/numbers.md) | Time, tokens, rejections, and rounds per task building bflow in its own repo. |
| [How to test and report](docs/en/how-to-test-and-report.md) | What we expect from testers, how to report, and known issues. |

## Development

```bash
go vet ./...
go test ./...        # includes E2E tests that build the binary and use real git
```

- Dependencies: standard library, `gopkg.in/yaml.v3`, and `github.com/zalando/go-keyring`.
- The core (`internal/...`) never imports `internal/adapters/...`; verified by `internal/archtest`. The only place connecting both is `cmd/bflow`.
- External adapters are tested with `httptest` and recorded responses, never against real services.
- Contributing guide and how to help: [`CONTRIBUTING.md`](CONTRIBUTING.md#cómo-ayudar). Vulnerabilities: [`SECURITY.md`](SECURITY.md). Code of conduct: [`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md).

## License

[Apache License 2.0](LICENSE). Copyright 2026 Innobytes ([innobytes.tech](https://innobytes.tech)).
