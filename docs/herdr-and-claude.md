# herdr and Claude Code

agentpr works on its own in any git repo. With [herdr](https://herdr.dev) and [Claude Code](https://claude.com/claude-code) it closes the loop: it opens next to Claude when a turn leaves changes, and sends your Review straight to Claude's pane.

## Opening next to Claude

```sh
agentpr install     # adds a Stop hook to ~/.claude/settings.json
agentpr uninstall   # removes it
```

`install` adds `agentpr hook` as a Claude Code [Stop hook](https://docs.claude.com/en/docs/claude-code/hooks), which runs at the end of every Claude turn. Both commands back up the old file to `settings.json.bak` before writing, leave every other setting alone, and print whether anything changed. The rewrite keeps the content but sorts the keys.

At the end of a turn, `agentpr hook`:

1. Does nothing unless it runs inside a herdr pane (`HERDR_ENV=1` and `HERDR_PANE_ID` set) and `AGENTPR_AUTO_OPEN` isn't `0`.
2. Reads the session's working directory from the hook input and does nothing if it's not a git repo.
3. Does nothing if an `agentpr` pane for that worktree is already open in the tab: that pane reloads on its own.
4. Does nothing if the Changeset is empty.
5. Does nothing if the turn changed no files: the working tree is snapshotted (as for the Changeset, so edits made through Bash count) and compared with the snapshot recorded by the previous hook run, kept in `.git/agentpr/<branch>.hook-tree`. The first run has nothing to compare with and counts as changed. Changes you make by hand between turns count as well.
6. Otherwise splits Claude's pane to the right without taking focus, labels the new pane `agentpr` and runs `exec agentpr` in it, so the pane closes when you quit.

The hook never blocks or fails Claude: it exits after at most 2 seconds, and errors go to `.git/agentpr/hook.log`.

To turn it off without uninstalling, set `AGENTPR_AUTO_OPEN=0` in the environment Claude runs in. agentpr sets it on its own `claude -p` runs (the commit message drafts), so those never open a split.

## Sending a Review

When you submit a Review with *Request changes* (`S`, then `r`), agentpr builds a prompt from the pending Threads: each one's location, snippet and comments, and an instruction to answer with `agentpr reply <thread-id> "..."`. It then delivers it, in this order:

1. To the Claude pane working in this repo, via `herdr agent prompt`. It looks for herdr agents named `claude` whose directory is the repo root or inside it. If there are several, you pick one by number; if Claude is busy, herdr queues the prompt for after the current turn.
2. To the clipboard (`wl-copy`, else `xclip`) when there is no such pane.
3. To `.git/agentpr/last-review.md` when there is no clipboard either.

The status line says which one happened. Either way the Comments stop being pending.

Claude answers with `agentpr reply`, which appends its Comment to the Thread's file in `.git/agentpr/`. The open agentpr checks for changes every 1.5 seconds and shows the answer.

`agentpr` must be on the `PATH` Claude uses for `agentpr reply` to work.
