# Project Notes

This repository uses **jj** (Jujutsu) instead of git for version control. Use `jj` commands rather than `git` commands.

## Creating commits with jj

In jj, the working copy **is** the current change. All edits are automatically tracked in it. To create a new commit:

1. **`jj new`** — Run this *before* starting new work. It finalizes the current change and creates a fresh empty change on top. This is the equivalent of "committing" in git.
2. **`jj describe -m "..."`** — Sets the description on the current change.

**Do not** use `jj describe` to "commit" new work into an existing change — that only rewrites the message and will overwrite the previous description. If you need to separate files already in one change, use `jj squash --from <rev> --into <rev> <paths>` to move specific files between changes.

## Pushing with jj

The `main` bookmark must be moved forward before pushing:

1. **`jj bookmark set main -r <rev>`** — Move `main` to the revision you want to push.
2. **`jj git push --bookmark main`** — Push to remote.

**Do not** push to remote unless the user explicitly asks you to.
