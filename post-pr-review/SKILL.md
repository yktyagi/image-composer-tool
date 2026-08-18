---
name: post-pr-review
description: Post review comments AS A REVIEWER on someone else's GitHub PR. Use when the user wants to review a PR and leave inline/summary comments on it. Drafts all comments locally, shows them for approval, lets the user pick which ones to post, and only posts after explicit permission. NOT for addressing feedback on your own PR — that is address-pr-review.
---

# Posting review comments on a PR (as reviewer)

Turn a code review into inline + summary comments on a GitHub PR — but **draft everything locally first, show it, let the user select, and never post without explicit permission**.

This is the reviewer-side counterpart to `address-pr-review` (which is for fixing feedback on your *own* PR). Do not conflate them.

## Hard rules (do not violate)

1. **Never post without explicit permission.** Draft locally, display, and wait for a clear "yes, post it" (or a selection of which comments to post). A vague "ok" to a different question is not permission to post to a public PR.
2. **Show the comments locally before posting.** The user must be able to read every comment verbatim as it will appear.
3. **Post only the selected comments.** If the user picks a subset (e.g. "just 1 and 3"), post exactly those and nothing else.
4. **Confirm the posting account first.** A PR review is public and attributed. Run `gh auth status` and state which account will post. If it's not the account the user expects, stop.
5. **Default event is `COMMENT`.** Never `APPROVE` or `REQUEST_CHANGES` unless the user explicitly asks for that verdict.

## 1. Gather the PR + diff (read-only)

```bash
gh pr view <PR> --json number,title,author,baseRefName,headRefName,headRefOid,state,url,isCrossRepository
gh pr diff <PR>
```

Capture `headRefOid` — the head commit SHA. Every inline comment and the review itself must be pinned to it (`commit_id`), so the anchors stay valid if the branch advances.

Read surrounding code as needed via `gh api repos/<owner>/<repo>/contents/<path>?ref=<sha>` (decode base64) rather than the local checkout, which may be on a different branch.

## 2. Draft the review LOCALLY

Analyze the diff and write the findings to a local draft file (e.g. `/tmp/pr<PR>-review.md`) so the user can read them. For each inline finding record: file path, line, and the comment body. Also draft the summary body.

**Critical constraint — inline comments only anchor to lines in the diff.** GitHub's review API resolves an inline comment's `path`+`line` only against lines that appear in the PR's unified diff (added/context lines on the RIGHT side by default). Anchoring to a line outside the diff returns **422 `Path could not be resolved`** and the *entire* review is rejected. Before drafting an inline comment, confirm its line is in `gh pr diff` output.
- For a concern on unchanged code (not in the diff): either (a) anchor to the nearest changed line and reference the real location, or (b) put it in the summary body with a permalink `.../blob/<sha>/<path>#L<n>`. Tell the user which you did and why.
- To comment on a deleted/left-side line, the comment needs `side: "LEFT"`.

## 3. SHOW the draft and ask what to post

Present the drafted comments to the user, numbered, each showing the file:line anchor and the exact body text. Then ask — using the selection tool when appropriate — **which** comments to post and confirm the event type. Do not proceed on assumption.

If some findings had to move to the summary (step 2), say so explicitly.

## 4. Build the review payload (only the selected comments)

Write the API payload to a file. `event` defaults to `COMMENT`.

```json
{
  "commit_id": "<headRefOid>",
  "event": "COMMENT",
  "body": "<summary — omit or empty string if none>",
  "comments": [
    { "path": "<file>", "line": <n>, "body": "<...>" }
  ]
}
```

- `line` is the line number in the file at `commit_id` (RIGHT side). Use `start_line`+`line` for a multi-line range. Add `"side": "LEFT"` for deleted lines.
- Include ONLY the comments the user selected in step 3.
- A single inline comment (no summary) is fine — omit `body` or pass `""`.

## 5. Post it (a single atomic review)

```bash
gh api repos/<owner>/<repo>/pulls/<PR>/reviews --method POST --input <payload.json> \
  -q '"Posted review " + (.id|tostring) + " (" + .state + "): " + .html_url'
```

This creates one review containing all selected inline comments plus the summary — cleaner than N standalone comments and it threads properly in the Files-changed tab.

**If it returns 422 `Path could not be resolved`:** one or more inline anchors are not in the diff. Do NOT retry blindly — the whole review was rejected and nothing posted. Re-check each `path`/`line` against `gh pr diff`, fix or move the offending comment (step 2), re-show if the change is material, and re-post.

## 6. Confirm and report

Report the review URL and which comments were posted. If any drafted comments were withheld (not selected, or moved to summary), state that too so the user knows what did and didn't land on the PR.

## Alternatives (when a full review isn't wanted)

- **One general PR comment** (not line-anchored): `gh pr comment <PR> --body-file <file>`.
- **A single inline comment** without opening a review: still use the reviews endpoint with one `comments` entry and `event: COMMENT` — there is no clean per-line comment endpoint that avoids the "pending review" state.
