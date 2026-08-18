---
name: address-pr-review
description: Address PR review comments (e.g. Copilot or human reviewers) and push a clean, signed update. Use when the user asks to fix/address review comments, resolve reviewer feedback, or apply changes to an open PR and re-request review. Folds fixes into the original commit via fixup + autosquash, keeps history clean and SSH-signed, replies inline, updates the PR template, and re-requests Copilot review.
---

# Addressing PR review comments

Workflow for turning reviewer feedback on an open PR into a clean, signed history update — without corrupting upstream or leaking git bookkeeping into the PR.

Follow the steps in order. Do NOT skip the pre-flight inspection: a wrong force-push target or a missed upstream commit corrupts the PR.

## 0. Gather state first (read-only)

Before changing anything, establish:

- **PR + review comments** — fetch inline review comments (thread ids matter for replying):
  ```bash
  gh pr view <PR> --json number,title,headRefName,baseRefName,headRepositoryOwner,isCrossRepository,url,commits
  gh api repos/<owner>/<repo>/pulls/<PR>/comments \
    --jq '.[] | {id, user: .user.login, path, line, in_reply_to_id, body}'
  gh api repos/<owner>/<repo>/pulls/<PR>/reviews \
    --jq '.[] | {id, user: .user.login, state, body}'
  ```
- **Which remote hosts the PR head.** `isCrossRepository:false` → head is on `origin` (upstream); push there. Cross-repo fork PRs push to the fork remote (e.g. `contrib`). Confirm with `git remote -v`.
- **Local vs remote divergence.** `git fetch origin` then check for commits on the remote branch you don't have locally — especially an **upstream bot coverage/chore commit** (code-coverage update). See step 4.
- **Signing config** — `git config commit.gpgsign` (want `true`), `git config gpg.format` (want `ssh`). Verify HEAD is signed with `git log -1 --format='%G?'` (want `G`).

## 1. Make the code fixes

**Don't assume every review comment is correct and immediately make the suggested change.** Understand the reasoning behind each comment and verify whether it is actually valid for your code — reviewers (Copilot especially) misread control flow, apply another language's semantics, or flag a non-issue. Where cheap, confirm the premise empirically (a quick test/repro, checking the actual API behavior, re-reading the surrounding code) before touching anything. If a comment is wrong or the "fix" would regress correctness, leave the code as-is and reply (step 6) explaining why. When a comment is valid but its suggested fix is not the best one, apply the fix that actually fits the code.

Apply the changes that address each valid review comment. Match the surrounding code style. Keep each fix minimal and scoped to the comment.

## 2. Run tests and linting BEFORE pushing

Not applicable for ADR / docs-only PRs (skip this step for those).

Run the repo's tests and linter for the affected packages, e.g.:
```bash
go build ./<affected>/...
go test ./<affected>/... 
$(go env GOPATH)/bin/golangci-lint run <affected>/...
```
Do not push if tests or lint fail. (Locate the linter — it may not be on `PATH`; check `$(go env GOPATH)/bin`.)

## 3. Fixup commit + autosquash into the original commit

Fold the fix into the commit it fixes so history stays clean — do NOT add a separate "address review" commit.

```bash
git branch backup/<branch>-pre-autosquash-<shortsha>          # safety net
git add <files>
git commit --fixup=<target-sha> -s                            # -s = DCO sign-off
GIT_SEQUENCE_EDITOR=true GIT_EDITOR=true \
  git rebase -i --autosquash --gpg-sign <base-before-target>  # non-interactive autosquash
```

Rules:
- **No `Co-Authored-By` trailer.** (This repo's commits are SSH-signed automatically; keep the DCO `Signed-off-by` line via `-s`.)
- **Keep the SSH signing** — pass `--gpg-sign` on the rebase so rewritten commits stay signed. Verify afterward: `git log -1 --format='%G? %GK'` → `G`.
- **Don't mesh anything.** `--autosquash` only touches the `fixup!` and its target. Confirm the log is exactly the intended commits and nothing else moved.
- Delete the backup branch once the final state is verified.

## 4. Rebase the upstream bot coverage chore commit

If the upstream bot pushed a **code-coverage update chore commit** onto the PR branch (visible as a commit on the remote branch not in your local history), preserve it — do not drop it in the force-push.

```bash
git fetch origin
git log origin/<branch> --not HEAD --oneline    # shows remote-only commits
```
If a bot chore commit shows up, rebase your rewritten work so that commit is retained (cherry-pick it back on top, or rebase onto it) before force-pushing. Never force-push in a way that clobbers an upstream commit you didn't recreate.

## 5. Force-push carefully with a lease

Push to the remote/branch that hosts the PR head (step 0). Use a lease pinned to the SHA you observed, so a concurrent update aborts the push instead of overwriting it:

```bash
git push --force-with-lease=<branch>:<observed-remote-sha> <remote> HEAD:<branch>
```

Verify the reported `old...new` range matches your expectation. Never `--force` without a lease.

## 6. Reply to review comments INLINE

Reply threaded under each review comment — never as a standalone top-level PR issue comment:

```bash
gh api repos/<owner>/<repo>/pulls/<PR>/comments/<comment-id>/replies \
  -f body="<reply describing the code change, or why no change was made>"
```

For a comment you determined was invalid (step 1), reply with the reasoning — what the comment assumed, why it doesn't hold for this code — rather than silently ignoring it.

## 7. Keep git bookkeeping OUT of the PR

PR replies, the PR body, and commit messages describe the **code change and reasoning only**. Never surface internal mechanics in anything pushed to the PR:
- autosquash / fixup / rebase
- force-push
- SSH-signing
- `Co-Authored-By` removal
- "tests passing locally"

These belong in your working notes to the user, not the PR.

## 8. Update the PR template

Fill in the PR body's required sections (Description, dependencies, "How Has This Been Tested?", merge checklist) if they're empty or stale:
```bash
gh pr edit <PR> --body-file <file>
```

## 9. Re-request Copilot review after the force-push

The repo's ruleset disables auto re-review on push, so request it explicitly:
```bash
./copilot-rereview.sh <PR>
```
(Run from the repo root; the script only touches the origin PR and never pushes code.)
