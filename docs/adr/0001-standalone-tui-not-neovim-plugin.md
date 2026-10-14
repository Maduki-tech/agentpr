# Standalone TUI, not a Neovim plugin

The Reviewer lives in Neovim, so a Neovim plugin would get real Vim motions for free. We chose a standalone TUI anyway: the Reviewer wants a dedicated review tool that runs on its own (e.g. in a herdr pane next to the Author), not something tied to their editor config. As a result, Vim-style navigation has to be reimplemented in the TUI.
