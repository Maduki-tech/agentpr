# agentpr

Review the changes a coding agent (currently Claude Code) made on your branch, like a pull request, in the terminal. Comment on lines, submit, and Claude gets your Review, fixes things, and answers each Thread.

## Install

```sh
go build -o ~/.local/bin/agentpr .
```

`agentpr` must be on your `PATH`: Claude calls `agentpr reply` to answer Threads.

### Open automatically next to Claude

```sh
agentpr install     # adds a Stop hook to ~/.claude/settings.json (backup in settings.json.bak)
agentpr uninstall   # removes it
```

At the end of every Claude Code turn, `agentpr hook` runs. If Claude is in a herdr pane, its directory is a git repo, and the Changeset is not empty, agentpr opens in a split to the right of Claude, without taking focus. If an agentpr pane for that worktree is already open in the tab, nothing happens: it reloads live. Outside herdr, or with no changes, the hook does nothing. It never blocks Claude; errors go to `.git/agentpr/hook.log`.

Set `AGENTPR_AUTO_OPEN=0` to turn it off without uninstalling.

## Usage

Run it inside a git repo, ideally in a [herdr](https://herdr.dev) pane next to Claude:

```sh
agentpr                # review the current branch
agentpr --base <ref>   # compare against another branch, e.g. origin/develop
agentpr --theme catppuccin-latte
```

Code is syntax-highlighted by file type, and the whole UI follows the theme. Themes: `catppuccin-mocha` (default), `catppuccin-macchiato`, `catppuccin-frappe`, `catppuccin-latte`, or any [chroma style](https://xyproto.github.io/splash/docs/) name. Set a default with `AGENTPR_THEME`; `T` cycles the Catppuccin flavors while running. Use a truecolor terminal for the intended colors.

What gets reviewed (the Changeset) is everything that differs from the Base, like `git diff` against it: committed, uncommitted, and new untracked files. The Base is the merge-base with `main`/`master` on a feature branch (with `origin/main` when it is ahead of your local `main`). On `main` itself it's the upstream (`origin/main`), so unpushed commits show too; without an upstream, `HEAD`. To compare against another branch, local or remote, click `Base: … ▾` in the toolbar or press `b`; `--base` sets it at startup. The status line shows the active Base.

### The loop

1. Move to a line (or select lines with `V`), press `Enter`, type your comment, press `Enter`. Comments stay *pending* until you submit.
2. Press `S`, then `r` (request changes). The Review is sent to the Claude pane working in this repo via herdr. If there are several, you pick one; if there are none, it's copied to the clipboard.
3. Claude fixes the code and answers each Thread with `agentpr reply <thread-id> "..."`. Answers show up live.
4. Resolve Threads with `R`, reply with `Enter`, repeat. When you're happy, `S` then `a` (approve) archives the Threads.

### Toolbar

The top row has a button for each action, so you can click instead of remembering keys: `‹ File` / `File ›`, `💬 Comment`, `✓ Resolve`, `Request changes`, `Approve`, `Base: … ▾` (compare against another branch), `/ Search`, `◐` theme, `? Keys` (the key list), the git action button with its `▾` menu, and `✕ Quit`. A click does exactly what the key does; greyed-out buttons can't do anything right now. On narrow terminals labels shrink to icons, then the least used buttons drop out.

### Keys

| Key | Action |
| --- | --- |
| `j` / `k`, `Ctrl-d` / `Ctrl-u`, `gg` / `G` | Move |
| `]c` / `[c` | Next / previous hunk |
| `]f` / `[f` | Next / previous file |
| `]t` / `[t` | Next / previous Thread |
| `v` / `V` | Select lines |
| `Enter` | Comment on line, selection, file header, or top row (whole Changeset); reply when on a Thread |
| `R` | Resolve / unresolve Thread |
| `S` | Submit Review: `r` request changes, `a` approve |
| `c` | Run the action button (see below) |
| `C` | Git menu: Commit, Push, Create PR, Merge PR, View PR |
| `b` | Compare against another branch (local or remote) |
| `T` | Next theme |
| `?` | Show / hide the key list |
| `/`, `n` / `N` | Search |
| `q` | Quit (pending comments are kept) |
| Mouse | Click toolbar buttons, wheel scrolls, click a file in the list to show only that file (click again or `Esc` for all), click selects a row, drag selects lines, click the selected row/lines to comment |

### Shipping

When the Changeset looks good, ship it from the action button at the right of the toolbar (`c`, or click it; `▾` or `C` opens the menu with each step). Its label follows the branch: `Commit` (no remote), `Commit, push & create PR`, `Commit & push` once a PR exists, `Push & create PR` / `Create PR` for committed work, then `Merge PR`.

- A commit stages everything the Changeset shows (tracked and untracked). On `main`/`master` you're asked for a new branch first, so a PR is possible; open Threads move with you.
- The commit message is drafted by Claude: agentpr stages the changes (`git add -A`) and pipes `git diff --cached` (stat and diff, cut at 100KB) to `claude -p --model haiku`, then prefills the message box. Edit it or press `Enter`; `Ctrl-R` asks again. If `claude` is missing, fails, or takes over 60s, the box stays empty and the status line says why. Esc leaves the changes staged.
- Creating a PR pushes the branch, then asks for the base branch (preselected: the Base's branch) and the title (first line) and body, prefilled with the commit message, or drafted by Claude from the branch's diff when there's no new commit.
- Merging asks for the target branch (preselected: the PR's base; another one retargets the PR) and the method: merge, squash (preselected) or rebase.

PRs use the [GitHub CLI](https://cli.github.com) (`gh`); if it's missing, not logged in, or no remote is on GitHub, the PR actions are off and the status line says why. In menus: `j`/`k` or the mouse to move, `Enter` or click to pick, `Esc` to close.

In the comment box: `Enter` saves, `Ctrl-J` adds a newline, `Esc` cancels.

The mouse is captured, so hold `Shift` to select text in your terminal.

Threads are stored per branch in `.git/agentpr/`, so they never end up in a commit. A Thread whose code is gone is shown as *outdated*, with the snippet it was written on.

See [GLOSSARY.md](GLOSSARY.md) for the terms and [docs/adr](docs/adr) for the design decisions.
