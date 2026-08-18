---
name: fix-failing-checks
description: Diagnose and fix failing GitHub Actions / CI checks on a PR or branch, then push a clean, signed fix. Use when the user says checks/CI/actions/a workflow are failing, red, or broken, or points at a failing run. Pulls the check rollup, reads the failing job logs, maps log to root cause, verifies the fix locally, and commits via the create-commit conventions. NOT for addressing human/Copilot review comments — that is address-pr-review.
---

# Fixing failing GitHub checks

Workflow for turning a red CI run into a verified, signed fix. The value here is
**diagnosis**: get from "a check is red" to the exact root cause before touching code,
then reuse the existing commit conventions to land the fix.

Follow the steps in order. Do NOT start editing code before you have read the actual
failing-job log — the check name rarely tells you the real cause.

## 0. Gather state first (read-only)

Identify the PR/branch and enumerate check results. Prefer the PR rollup:

```bash
gh pr view <PR> --repo <owner>/<repo> \
  --json title,headRefName,baseRefName,state,url,statusCheckRollup
```

From the rollup, list every check whose `conclusion` is `FAILURE` (ignore `SUCCESS`,
`SKIPPED`, `NEUTRAL`). Note each failing check's `name`, `workflowName`, and the job id
in its `detailsUrl` (`.../job/<jobId>`). If working from a branch instead of a PR, use
`gh run list --branch <branch>` then `gh run view <runId>`.

**Watch for a shared root cause.** Multiple red checks often trace to ONE defect (a
compile error fails lint, unit tests, and coverage at once). Diagnose that first — fixing
it may clear several checks.

## 1. Read the failing logs, not just the summary

For each distinct failure, pull the failed steps:

```bash
gh run view --repo <owner>/<repo> --job <jobId> --log-failed | tail -100
```

Read to the actual error line (compile error, assertion, lint rule, coverage delta,
missing file). Map it back to a file:line in the working tree. If the log is truncated or
ANSI-noisy, widen the `tail` or grep for `error|FAIL|panic|exit code`.

Distinguish failure classes — they need different fixes:
- **Compile / typecheck** — a signature or API changed and a caller/test wasn't updated.
- **Test assertion** — behavior changed; decide whether the code or the test is wrong.
- **Lint** — a rule violation; fix the code, don't blanket-disable the rule.
- **Coverage gate** — often a *downstream* symptom of a package that failed to compile
  (0% coverage drags the total under threshold). Fix the compile/test failure first and
  the coverage usually recovers; only add tests if coverage is genuinely short.
- **Flake / infra** — network, runner, transient. Confirm it's not your change before
  re-running; don't "fix" code for a flake.

Confirm the checkout you're editing is the one that matches the PR head SHA (`git log -1`),
especially if multiple clones/worktrees exist.

## 2. Make the fix

Fix the root cause, minimal and scoped. Match surrounding code style. If the failure is a
genuine flake or pre-existing unrelated breakage, say so to the user rather than forcing a
code change — re-running the job (`gh run rerun <runId> --failed`) may be the right call.

## 3. Verify locally BEFORE pushing

Reproduce the check locally for the affected packages so you don't burn a CI cycle:

```bash
go build ./<affected>/...
go test ./<affected>/...
$(go env GOPATH)/bin/golangci-lint run <affected>/...   # linter may not be on PATH
```

Match the local command to the failing check (build the tree the coverage job builds, run
the lint the lint job runs). Do not push until the local reproduction is green.

## 4. Commit using the user's conventions

Invoke the **create-commit** skill for the commit itself — it owns the signing rules
(SSH-signed, `Signed-off-by`, NO `Co-Authored-By`). Default to a **normal forward commit**:
a CI fix is not a history rewrite.

Only fold the fix into an earlier commit (fixup + autosquash + force-push) if the user
explicitly wants clean history AND the broken commit is unmerged on this PR branch — in
that case follow the fixup/autosquash/force-with-lease steps from **address-pr-review**.
Never force-push without a lease, and never rewrite already-merged commits.

Write the commit message about the code change and the failure it fixes. Keep CI/git
mechanics (which job was red, force-push, signing) OUT of anything pushed to the PR.

## 5. Push and confirm the checks go green

```bash
git push <remote> HEAD:<branch>
```

Push to the remote that hosts the PR head (for a same-repo PR that's `origin`; for a fork
PR it's the fork remote). Then confirm the re-run:

```bash
gh pr checks <PR> --repo <owner>/<repo> --watch
```

Report the outcome to the user faithfully — if a check is still red, read its log and
iterate; don't claim success before the run is green.

## When this is actually a review-comment task

If the "failure" is really a human or Copilot **review comment** (not a CI check), stop and
use **address-pr-review** instead — it handles inline replies, fixup-into-original, and
re-requesting review. This skill is only for red automated checks.
