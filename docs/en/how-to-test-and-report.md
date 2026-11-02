> 🌐 **Language / Idioma:** [Español](../es/como-probar-y-reportar.md) · **English**

# How to test bflow and report issues

Thank you for testing bflow. It is at v0.1.0, the first published release: things can fail, and the most helpful thing you can do is tell us what fails.

## What changed since rc.9

- `guard` covers more Bash cases: git global options, redirections, `tee`, `rm`, `mv`, and `cp` toward `.env` and `.bflow/`, and writes to frozen tests (#150).
- `report DONE` rejects comments that cite the ID, `R<n>`, `T<n>`, or the spec with `narrative_comment` (#151).
- The reviewer has its own eval to measure whether it catches defects (#152).
- The full lane no longer opens contract or pause gates by default: the contract is validated with the check and only opens a gate in the cases documented in [concepts](concepts.md#gates); pause is opt-in via `bflow pause` (#153, #173).
- In hotfix, `report DONE` requires a test-only commit whose check failed, or `--no-repro` with the `repro` gate (#159).

## What we expect from you

1. **Install** ([installation](installation.md)) and run `bflow version` and `bflow doctor`.
2. **Walk through a task using the local tracker**, without connecting anything, in a test repo:

   ```bash
   cd my-repo
   bflow init
   bflow install claude          # or: bflow install opencode
   bflow new --lane light --title "A small task"
   ```

   Then open your agent and tell it "what's next". Each bflow response states the next step; follow it through to the PR.
3. **Repeat with your real tracker** (Plane or GitHub, see [configuration](configuration.md)) and with the agent you actually use.
4. Note what surprised you: a confusing message, a step you did not understand, something you had to ask about.

To understand what to expect at each step, read the [guide](guide.md) and [concepts](concepts.md).

## What we ask you to test

- **A `full` task with a configured check** (with a `test` step): confirm that the contract gate does not appear. T1 reports `CONTRACT_READY`, bflow validates it, and the task proceeds autonomously to implementing. If the gate appeared, note why bflow indicated it did.
- **A hotfix with a red test and then green:** a commit with only the test and a red `bflow check`; then the fix, a green `bflow check`, and `DONE`. Also test that `DONE` without that commit is rejected with `repro_test_missing`.
- **`bflow pause <ID>`** on a task before quality: the gate appears when implementing finishes.

## One question per closed feature

When a feature finishes and gets merged, answer: **was there anything that had to be fixed after merge that the reviewer should have caught?** Answer in a comment on the feature issue or open a new issue titled "Post-merge: <feature>". A simple "no" is enough.

## How to report a useful issue

Open an issue at <https://github.com/innobytestech/bflow/issues> with:

- The output of `bflow version`.
- The output of `bflow doctor`.
- Which agent (Claude Code or OpenCode, and its version) and which tracker you use.
- Your operating system.
- The steps, in order, leading up to the problem, and what you expected to happen.
- The full output of the command that failed. If it exited with code 2, repeat it with `--json` and paste everything: it includes the reason and `next`.

Do not paste tokens or `.env` contents. If the problem relates to a task, `bflow show <ID> check` and `bflow stats <ID>` help.

## Known issues

Today there are no open bugs that we know of: those listed in rc.9 (#39, #52, #53, #86, and #91) are closed. Remaining open items are enhancements and planned work; check [open issues](https://github.com/innobytestech/bflow/issues) before reporting.
