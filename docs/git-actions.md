# Git actions

The button at the right of the toolbar ships the Changeset. Press `c` or click it to run its action; press `C` or click `▾` for a menu with each step on its own.

## The button's label

The label follows the branch's state. The first row that matches wins:

| State | Label | Does |
| --- | --- | --- |
| Detached HEAD | Commit | nothing: check out a branch first |
| Uncommitted changes, no remote | Commit | commit |
| Uncommitted changes, an open PR (or PRs unavailable) | Commit & push | commit, push |
| Uncommitted changes | Commit, push & create PR | commit, push, open a PR |
| No remote, nothing to commit | Commit | nothing |
| Behind its upstream | Pull | nothing: pull first |
| Unpushed commits, and an open PR, or on `main`/`master`, or PRs unavailable | Push | push |
| Unpushed commits | Push & create PR | push, open a PR |
| An open PR | Merge PR | merge it |
| The PR is merged | Merged | nothing |
| Commits since the Base, not on `main`/`master` | Create PR | open a PR (off when PRs are unavailable) |
| Anything else | Commit | nothing: nothing to commit |

A greyed-out button can't do anything right now; pressing `c` shows why in the status line.

The menu offers Commit, Push, Create PR, Merge PR and View PR (opens the PR in the browser), each enabled only when it can work.

## Committing

A commit stages everything the Changeset shows, tracked and untracked (`git add -A`), and commits it. On `main` or `master` agentpr first asks for a new branch name and switches to it, so a PR is possible; open Threads move to the new branch.

The commit message is drafted by Claude: agentpr stages the changes and pipes `git diff --cached` (the stat, then the diff, cut at 100KB) to `claude -p --model haiku`, then prefills the message box with the answer. Edit it, or press `Enter` to commit; `Ctrl-R` asks again. If `claude` is missing, fails or takes more than 60 seconds, the box stays empty and the status line says why. `Esc` cancels, but leaves the changes staged.

## Pull requests

PRs use the [GitHub CLI](https://cli.github.com) (`gh`). If it's not installed, not logged in, or `origin` isn't on GitHub, the PR actions are off and the status line says why. agentpr asks GitHub for the branch's PR about every 30 seconds and after each git action.

- **Create PR** asks for the base branch (preselected: the Base's branch), then for the title (first line) and body. With a new commit they're prefilled with the commit message; otherwise Claude drafts them from the branch's diff against the Base. It then pushes the branch and opens the PR.
- **Merge PR** asks for the target branch (preselected: the PR's base; picking another retargets the PR first) and the method: merge, squash (preselected) or rebase.

## Cleaning up after a merge

Merging from a linked worktree (made with `git worktree add`, or a herdr worktree workspace) then removes it and quits:

1. It deletes the branch on `origin` (fine if GitHub already did), then locally.
2. It removes the worktree, through `herdr worktree remove` when the worktree is open as a herdr workspace, which also closes the workspace.

If the worktree has uncommitted changes, untracked files, or commits on no remote, agentpr asks first: `y` removes it anyway, `n` keeps everything. If a step fails, agentpr stays open and shows the error.

Merging from the main checkout removes nothing.

In menus: `j`/`k` or the mouse to move, `Enter` or click to pick, `Esc` to close.
