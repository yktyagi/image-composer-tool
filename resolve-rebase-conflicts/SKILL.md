---
name: resolve-rebase-conflicts
description: Resolve merge conflicts during a git rebase (or merge/cherry-pick), drive it to completion, and print a structured summary of how each conflict was resolved. Use when the user asks to resolve/fix rebase conflicts, continue a stuck rebase, or is mid-rebase with conflicts. Keeps rewritten commits SSH-signed and never force-pushes without being asked.
---

# Resolving rebase conflicts

Turn a conflicted rebase into a clean, completed one — resolving each hunk on its merits, not by blindly picking a side — and end with a summary table the user can review. Also handles conflicts from `git merge` and `git cherry-pick` (same conflict-marker mechanics; the continue/abort commands differ, see step 5).

Follow the steps in order. Do NOT run `git rebase --continue` until every conflict is resolved and staged, and never `git checkout --ours/--theirs` a whole file to make markers disappear — that silently discards real work.

## 0. Establish state first (read-only)

Before touching anything, find out where you are:

```bash
git status
git rebase --show-current-patch --stat 2>/dev/null | head -1   # which commit is being applied
```

- Confirm what's in flight: `.git/rebase-merge/` (interactive/merge rebase), `.git/rebase-apply/` (am-style), `.git/MERGE_HEAD` (merge), `.git/CHERRY_PICK_HEAD` (cherry-pick).
- Note the progress (`.git/rebase-merge/msgnum` of `.../end`) so you know how many commits remain — conflicts can recur on later commits.
- List conflicted files from `git status` ("Unmerged paths"). These are what you must resolve.

**During a rebase the sides are swapped from intuition:** `HEAD`/`--ours` is the commit already replayed onto the new base; the incoming side (`>>>>>>>`) is *your* commit being replayed. Keep this straight when reasoning about "mine vs theirs."

## 1. Understand each conflict before resolving

For each conflicted file, read the conflict hunks and the surrounding context:

```bash
grep -n -E '^(<<<<<<<|=======|>>>>>>>|\|\|\|\|\|\|\|)' <file>   # locate every marker
```

Classify each hunk before editing:
- **Additive / non-overlapping** (both sides added distinct list items, imports, cases, release-note bullets) → usually **keep both**, in a sensible order.
- **Same line edited two ways** → understand the intent of each side; combine so both intents survive, or pick the one that's correct and explain why.
- **One side deleted, other modified** → decide from intent; don't default to "keep the edit."

Use `git log`/`git show` on the two commits when the intent isn't obvious. If a hunk is genuinely ambiguous and picking wrong would lose real work or change behavior, **ask the user** rather than guess.

## 2. Resolve the markers

Edit the file so the result is correct code/prose with **all three markers removed** (`<<<<<<<`, `=======`, `>>>>>>>`, and any `|||||||` base section from `diff3`/`zdiff3` style). The resolved region must read like intended code, not a mechanical concatenation — fix ordering, spacing, and duplication.

Record for the summary, per file: what conflicted, and the resolution (kept both / took HEAD side / took incoming side / merged manually / asked user).

## 3. Verify nothing is left unresolved

```bash
git diff --check                     # flags leftover conflict markers + whitespace errors
grep -rn -E '^(<<<<<<<|>>>>>>>|=======)' <resolved-files>   # belt-and-suspenders
```

For code changes, build/test the affected package before continuing (skip for docs/ADR-only conflicts):

```bash
go build ./<affected>/... && go test ./<affected>/...   # adapt to the repo's toolchain
```

Don't continue the rebase if markers remain or the build/tests fail.

## 4. Stage the resolutions

```bash
git add <each-resolved-file>
git status        # confirm "Unmerged paths" is now empty
```

Stage only the files you resolved. Don't `git add -A` — leave untracked scratch files out.

## 5. Continue (keep commits signed)

Continue the operation without opening an editor, preserving SSH signatures on the rewritten commit(s):

```bash
# rebase:
GIT_EDITOR=true git -c commit.gpgsign=true rebase --continue
# merge:       git commit -S --no-edit
# cherry-pick: git -c commit.gpgsign=true cherry-pick --continue --no-edit
```

If more conflicts surface on a later commit, loop back to step 1. Repeat until `git status` reports the rebase is done.

Then verify the head is signed and the log is what you expect:
```bash
git log -1 --format='%G? %GK'        # want 'G'
git log --oneline -n 5
```

## 6. Show the resolution summary

**Always end with this.** Print a concise, structured summary so the user can audit the resolution without re-reading diffs:

- **Operation + target**: e.g. "Rebased `overlay-work` onto `b077beca` (2 commits replayed)".
- **Per-conflict table**:

  | File | What conflicted | Resolution |
  |------|-----------------|------------|
  | docs/…/release-notes.md | Both sides added a new bullet at the same insertion point | Kept both, ordered HEAD-first |

- **Anything dropped or judged**: hunks where one side won, and why; anything you asked the user about.
- **Completion state**: final commit SHA(s), signature status, and that no conflict markers remain.
- **Not done unless asked**: note that nothing was force-pushed. Offer it as the next step if the branch is already published, but don't push without explicit approval.

## Bail-out

If the resolution is going wrong or the user wants to start over:
```bash
git rebase --abort      # (or: git merge --abort / git cherry-pick --abort)
```
This restores the pre-rebase state. Suggest it rather than leaving a half-resolved mess.
