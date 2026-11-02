> 🌐 **Language / Idioma:** [Español](../es/por-que-y-rumbo.md) · **English**

# Why bflow exists and where it is headed

I am Alfonso, from [Innobytes](https://innobytes.tech). I created bflow for my team and where I work, and now I want others to try it.

## My story

I have been working with AI for nearly a year, and with a *harness* (prompts, subagents, and scripts that guide the agent) to solve features for 6 or 7 months. I know AI is constantly improving. I don't want to invent anything new or tamper with the models: I want to guide them to deliver features using spec-driven development (SDD).

I started with one harness per project. I used one of them heavily and changed it constantly, while harnesses in other projects fell out of date. Every time I returned to another project, I had to manually update its harness, which took up time. Furthermore:

- Features took a long time to complete.
- Hallucinations occurred.
- Agents did not always follow the harness to the letter.
- A lot of redundant documentation was generated.

I did not try other tools. I watched videos of people building their own harnesses to guide agents based on Kiro or Spec Kit, but I never installed or researched them deeply. What I did was organize what already worked for me in my own way.

## Why I made it

- Spend fewer tokens, allowing each feature's context to pass from one agent to another without re-reading entire conversations.
- Follow a consistent development process.
- Make development more enjoyable now that developers write less code manually.
- Guide agents on the right path with spec-driven development.
- Ensure all developers on the team solve a feature in the same way, even if AI is non-deterministic.

I want others to try it and, if it works for them, follow this methodology: SDD my way, without inventing anything new, using trackers to keep track of tasks. I also want to show how I work and that the results are high quality.

## What I measured

This data comes from the harness prior to bflow, across two real repositories (one backend, one frontend). bflow figures are in [Team numbers](#team-numbers):

- **Rules lived in prose.** "MANDATORY", "NEVER", "do not skip the gate". The model followed them almost always, and that exact "almost" is where expensive mistakes slip in.
- **The model transitioned states manually.** It changed statuses in the tracker, created branches, and assembled PR URLs, each time in its own way. In one repository the flow was validated by scripts; in the other, it wasn't.
- **It spent tokens on mechanical work.** Reading the tracker, re-reading logs, parsing outputs of 4 test commands, maintaining state files. In one repo, around 7,000 fixed tokens per session before doing anything useful.
- **It cluttered the repo with working state.** Between 73% and 80% of versioned harness files were logs, reports, and state, not specifications.
- **Each repo had its own copy.** They drifted: different states, different branch prefixes, rules present in one repo but absent in another.

## Team numbers

What the team tested with bflow v0.1.0-rc.9 (with contract and pause gates in the full lane, prior to #153), measured with `bflow stats` on 2026-10-20/21. Anonymous and unvarnished: 51 completed tasks across 6 repos.

| Repo | Type | Dev | Tasks | Done | Median agent | Median human | Median new tokens | Median cache | With gate rejection | With quality round |
|---|---|---|---|---|---|---|---|---|---|---|
| Repo A | backend | dev 1 | 31 | 28 | 49m | 49m | 668.6k | 11.6M | 14 | 2 |
| Repo B | frontend | dev 1 | 6 | 5 | 32m | 36m | 698.8k | 28.0M | 0 | 1 |
| Repo C | website | dev 1 | 12 | 11 | 9m | 9m | 275.1k | 2.5M | 2 | 3 |
| Repo D | API | dev 1 | 3 | 3 | 1h40m | 25m | 991.9k | 31.5M | 1 | 0 |
| Repo E | frontend | dev 1 | 1 | 1 | 35m | 1h11m | 631.2k | 11.4M | 0 | 0 |
| Repo D | API | dev 2 | 3 | 2 | 2h45m | 67h20m | 679.4k | 10.5M | 0 | 0 |
| Repo F | point of sale | dev 3 | 4 | 1 | 19m | 23h16m | 344.8k | 5.9M | 1 | 0 |

The two groups are kept separate and not summed as "the team":

| Group | Done | With gate rejection | With quality round |
|---|---|---|---|
| dev 1 (author of bflow) | 48 | 17 | 6 |
| Other devs (dev 2 and dev 3) | 3 | 1 | 0 |

Notes:

- **Version:** rc.9, before #153.
- **Human time:** waiting time at gates (includes nights and weekends), not time worked.
- **Medians:** row by row only. No global median: median of medians is not a true median.
- **Sample of other devs:** 3 completed tasks; not a trend.
- **Post-merge quality:** not measured yet. Pending for v0.1.1.
- **No comparables:** no equivalent tasks performed without bflow.
- **Missing data from 2 people.**
- bflow building bflow is measured separately, with another cutoff date (2026-10-16): [Numbers](numbers.md).

## The goal

For a human to decide what matters (what gets built, the contract, what gets merged) without reading everything the agent generates, and for the rest of the process not to depend on the model's memory.

## The idea

Separate what **requires judgment** from what is **deterministic**:

- The **agent** understands the problem, designs, writes code, and reviews.
- The **CLI** manages state, transitions, git, the tracker, tests, and rules.

The agent does not need to remember the process: it asks `bflow` what is next and does it. A single binary is installed once per machine; each repository only has a short `bflow.yaml`. Concepts are in [concepts.md](concepts.md).

## Status

bflow is at **v0.1.0**, the first published release. Works end-to-end with the local tracker, Plane, and GitHub Issues, and with Claude Code and OpenCode. The command API may change before 1.0.

## Roadmap

Below is the plan, not what is already completed. Each milestone links to GitHub.

| Milestone | Exit criteria | Milestone |
|---|---|---|
| v0.1.1 | Enhancements postponed from v0.1.0 to avoid delaying release: yellow flags in walkthrough (#154) and PR metrics (#74). | [v0.1.1](https://github.com/innobytestech/bflow/milestone/8) |
| Milestone 2 · Outreach | After publishing v0.1.0: video (#69), topics (#67), English README (#62), sample repo (#76), article in Spanish (#161) and in English (#68), comparisons (#70). | [Milestone 2](https://github.com/innobytestech/bflow/milestone/6) |
| Milestone 3 · Post-launch | In no fixed order: repo memory (#156, decided with usage data), stability policy (#72), and full English bflow (#191, when non-Spanish speaking users arrive). Linear (#73) and Codex (#75) adapters are open for anyone wanting to contribute. | [Milestone 3](https://github.com/innobytestech/bflow/milestone/7) |
| 1.0 | Stable command API. | No milestone yet |

### Product modules

1. Done: workflow engine (state, gates, local tracker, Plane and GitHub Issues & Projects, git, and GitHub).
2. Done (initial version): guards (destructive git, `.env`, `.bflow/`, frozen tests even without hooks, manual branch and PR creation, diff size, agents ending without reporting).
3. In progress: distributor. `bflow render` generates agents for Claude Code and OpenCode; platform binaries exist with checksums and provenance attestation, installers, and `bflow update`. Pending: Codex and code signing on Windows and macOS (pending certificate).
4. Done: metrics (time, tokens per phase, agent, and model, quality, and friction).
5. Planned: context, a codebase index for planner and reviewer.
6. Planned: planning, organizing backlog and distributing it across balanced cycles.

Next: [installation](installation.md) · [how to test and report](how-to-test-and-report.md).
