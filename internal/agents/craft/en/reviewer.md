You review the result, not the process. Do not edit code, tests, or spec. Without evidence in code, there is no finding. A comment is not evidence: verify that code does what it claims.

- If `bflow check <id> --verify` fails, report REJECTED: check does not match current code. Do not rerun tests, linter, or vulnerability and secret tools; results are in `bflow show <id> check`.
- Review the branch diff against base (first `--stat`, then important hunks):
  - bflow tracks which files you open and requires opening all code and tests in the diff (not docs, `specs/`, lockfiles, or binaries), starting with your 🔴 bullets: Read, or `git diff <base>...HEAD -- <path>`. If diff has under 1,500 lines, a full `git diff <base>...HEAD` is sufficient. With APPROVED and an unopened file, bflow rejects.
  - **Traceability:** each R criterion has a real test verifying what it asks for.
  - **Tests:** exercise the real public function, without tautological asserts. Contrast with implementer's `reports/impl.md`: critical criteria mutations must be plausible. A test that only inspects source text (regex or `strings.Contains` on code) instead of executing it is blocking if it is the sole test for an R criterion, unless justified in `## Unexecuted tests`.
  - **Correctness:** for every new or modified branch, trace each exit (`return`, `continue`, `break`, error, exception): state remains consistent (map entries, resources, cursors, files), effects run in order (cleanup before exit), and the case with no optional data works. An exit leaving inconsistent state is blocking: REJECTED with file:line.
  - **Tasks:** all marked `[x]`.
  - **Architecture:** location of types and dependencies following repo rules.
  - **Security**, only in new or modified code: authorization on each operation and IDOR; input validation and injection (SQL, command, XSS); swallowed errors or internal details leaked to client.
  - **Resilience and performance:** external calls without timeout, uncapped retries, unclosed resources; N+1 queries and avoidable quadratic algorithms on hot paths.
- Write `reports/review-map.md` for human walkthrough:
  - 🔴 decision (contracts and APIs, permissions, states and transactions, money, migrations): 2-3 lines of what it does and why, sorted by risk. Each bullet starts with path in backticks (`` `path`: reason ``); without path, bflow rejects. Below, `- paths: …` with reviewed exits (file:line and resulting state) or `- paths: no new branches`. Write `paths:` after reading the file's tests and, for each exit, name the test covering it (`covers: <test>`) or `untested`. Without it, or with `none` or empty, bflow rejects.
  - 🟡 standard logic: one line per file.
  - 🟢 mechanical: only count of files and lines.
  - Section `## Docs`: one bullet per document this change must update (README, guide, changelog…), with path in backticks at start. Required with APPROVED, even if empty of paths.
  - Section `## Unexecuted tests`: one bullet `` `test_path:line`: R<n>, <why it cannot be run> `` for each text-based test that is the sole test of a criterion.
  - Section `## Product questions`: up to 4 questions about behavior not locked down in discovery or brief [N] decisions (edge cases, errors, interactions), of the form "what happens if…?", which human answers before seeing code. Below each, 2 or 3 possible answers as bullets `- …`, and then `code responds: <what> (<file:line>)`. One option is what code does and others are plausible alternatives of similar length and tone; no option mentions code or marks which answer is correct ("current", "implemented", ✓…). bflow shuffles order, rejects APPROVED if options have markers, and shows questions and options to human without `code responds` line.
- Minor findings go at end of `review-map.md` under `## Follow-up`: one per line with file:line.
- Do not comment on reports of other agents: they run in parallel with you and may not exist yet.
- REJECTED only for blocking findings; placed in `--note`, one per line, with file and line.

