---
name: create-commit
description: Create a git commit the way this user wants them — SSH-signed, with a Signed-off-by trailer, and NO Co-Authored-By / tool-attribution trailer. Use whenever the user asks to commit changes. Also covers verifying the signature and sign-off after committing.
---

# Creating a commit

The user's commit conventions, in order of importance:

1. **No `Co-Authored-By` trailer.** Do not add Claude/tool co-authorship or any "Generated with" attribution to commit messages.
2. **Sign off** with the committer identity — `git commit -s` adds the `Signed-off-by: <name> <email>` trailer.
3. **SSH-sign** the commit — `git commit -S`. Signing config is usually already set (`gpg.format=ssh`, `user.signingkey`, `commit.gpgsign=true`); `-S` makes it explicit and independent of config.

## Steps

1. Confirm identity and signing config (once per repo is enough):
   ```bash
   git config user.name && git config user.email
   git config gpg.format && git config user.signingkey && git config commit.gpgsign
   ```
   Want `gpg.format=ssh` and a `user.signingkey`. If they're missing, ask the user rather than guessing a key.

2. Stage intended files explicitly (`git add <paths>`). Do not blindly `git add -A` — leave untracked scratch/build inputs out unless the user wants them.

3. Commit, signed and signed-off, with a message that describes the change and its reasoning only:
   ```bash
   git commit -S -s -m "$(cat <<'EOF'
   <type>(<scope>): <summary>

   <body: what changed and why>
   EOF
   )"
   ```
   The message must NOT contain `Co-Authored-By` or tool-generation trailers.

4. Verify the result:
   ```bash
   git log --show-signature -1 --format="%H %G?%n%B" | head
   ```
   Want `G` (good signature) and a `Signed-off-by:` line, and no `Co-Authored-By:`.

## Amending

If a commit already exists with the wrong trailers, fix it in place:
```bash
git commit --amend -S -s -m "$(cat <<'EOF'
<corrected message without Co-Authored-By>
EOF
)"
```
`--amend -s` won't duplicate an existing `Signed-off-by`, and `-S` re-signs.
