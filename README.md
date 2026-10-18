# agentpr

Review the changes a coding agent (currently Claude Code) made on your branch, like a pull request, in the terminal. Comment on lines, submit, and Claude gets your Review, fixes things, and answers each Thread.

## Install

```sh
go build -o ~/.local/bin/agentpr .
```

`agentpr` must be on your `PATH`: Claude calls `agentpr reply` to answer Threads.

## Usage

Run it inside a git repo, ideally in a [herdr](https://herdr.dev) pane next to Claude:

```sh
agentpr                # review the current branch
agentpr --base <ref>   # compare against another ref
```

What gets reviewed (the Changeset) is everything that differs from the Base: committed, uncommitted, and new untracked files. The Base is the merge-base with `main`/`master` on a feature branch, or `HEAD` on `main` itself. After your first submitted Review, you only see what changed since then (`i` toggles the full Changeset).

### The loop

1. Move to a line (or select lines with `V`), press `Enter`, type your comment, press `Enter`. Comments stay *pending* until you submit.
2. Press `S`, then `r` (request changes). The Review is sent to the Claude pane working in this repo via herdr. If there are several, you pick one; if there are none, it's copied to the clipboard.
3. Claude fixes the code and answers each Thread with `agentpr reply <thread-id> "..."`. Answers show up live.
4. Resolve Threads with `R`, reply with `Enter`, repeat. When you're happy, `S` then `a` (approve) archives the Threads.

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
| `i` | Toggle full Changeset / changes since last Review |
| `/`, `n` / `N` | Search |
| `q` | Quit (pending comments are kept) |
| Mouse | Wheel scrolls, click a file in the list to show only that file (click again or `Esc` for all), click selects a row, drag selects lines, click the selected row/lines to comment |

In the comment box: `Enter` saves, `Ctrl-J` adds a newline, `Esc` cancels.

The mouse is captured, so hold `Shift` to select text in your terminal.

Threads are stored per branch in `.git/agentpr/`, so they never end up in a commit. A Thread whose code is gone is shown as *outdated*, with the snippet it was written on.

See [GLOSSARY.md](GLOSSARY.md) for the terms and [docs/adr](docs/adr) for the design decisions.
