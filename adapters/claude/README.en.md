> 🌐 **Language / Idioma:** [Español](README.md) · **English**

# Claude Code Adapter

`bflow install claude` installs the skill and merges the hooks; no manual copying needed. Agents are written by `bflow render` to `<repo>/.claude/agents/bflow-<agent>.md` and committed.

| File (embedded in binary) | Where `bflow install claude` places it | Purpose |
|---|---|---|
| `skill_head.md` + [`../leader/bflow.md`](../leader/bflow.md) | `~/.claude/skills/bflow/` (once per machine) | Main session interprets `next`; contains no flow rules |
| `settings.json` | `<repo>/.claude/settings.json` (merged: yours preserved; bflow entries replaced) | Hooks: `bflow hook session-start`, `bflow guard --reads` before Bash/Edit/Write/Read (Read only logged if from reviewer in quality: `reads.jsonl`), `bflow guard --post` after each Bash (if a frozen test changed, restores it and notifies agent), `bflow hook tokens` on turn and subagent end, `bflow hook subagent-stop` when a `bflow-*` agent ends; status bar `bflow statusline` |

Hooks are per-repo, not global: `bflow guard` enforces rules (protected branches, `.env`, frozen tests) that only make sense where `bflow.yaml` exists.

`bflow doctor` warns if the repo declares `agent: claude` but hooks are missing or the installed skill doesn't match this bflow version.
