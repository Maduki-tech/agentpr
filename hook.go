package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	hookCmd   = "agentpr hook"
	paneLabel = "agentpr"
)

// runHook is the Claude Code Stop hook: open agentpr in a herdr split next to Claude
// when the turn changed files and left a Changeset. It never fails Claude: errors only go to hook.log.
func runHook(stdin io.Reader) {
	if os.Getenv("AGENTPR_AUTO_OPEN") == "0" || os.Getenv("HERDR_ENV") != "1" || os.Getenv("HERDR_PANE_ID") == "" {
		return
	}
	var in struct {
		Cwd string `json:"cwd"`
	}
	json.NewDecoder(stdin).Decode(&in)
	if in.Cwd == "" {
		in.Cwd, _ = os.Getwd()
	}
	repo, err := openRepo(in.Cwd)
	if err != nil {
		return // not a git repo
	}
	if err := openPane(repo); err != nil {
		if f, ferr := os.OpenFile(filepath.Join(repo.GitDir, "agentpr", "hook.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); ferr == nil {
			fmt.Fprintf(f, "%s %s: %v\n", time.Now().Format(time.RFC3339), repo.Root, err)
			f.Close()
		}
	}
}

// treeChanged records tree in path and reports whether it differs from the one recorded
// last time. With no record yet it says true, so the first turn behaves like the old hook.
// ponytail: edits the user makes by hand between turns count too; a UserPromptSubmit hook would pin the turn start.
func treeChanged(path, tree string) bool {
	prev, _ := os.ReadFile(path)
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte(tree), 0o644)
	return len(prev) == 0 || string(prev) != tree
}

func openPane(repo *Repo) error {
	tree, err := repo.Snapshot()
	if err != nil {
		return err
	}
	// Recorded on every run, even when a pane is open, so the next turn compares against this one.
	edited := treeChanged(strings.TrimSuffix(repo.StatePath(), ".json")+".hook-tree", tree)
	var list struct {
		Result struct {
			Panes []struct {
				Tab   string `json:"tab_id"`
				Label string `json:"label"`
				Cwd   string `json:"cwd"`
			} `json:"panes"`
		} `json:"result"`
	}
	if err := herdrJSON(&list, "pane", "list", "--workspace", os.Getenv("HERDR_WORKSPACE_ID")); err != nil {
		return err
	}
	tab := os.Getenv("HERDR_TAB_ID")
	for _, p := range list.Result.Panes {
		// The pane runs `exec agentpr`, so it closes when agentpr quits: a labelled pane is a live one.
		if p.Label == paneLabel && p.Cwd == repo.Root && (tab == "" || p.Tab == tab) {
			return nil
		}
	}
	base, _, err := repo.Base("")
	if err != nil {
		return err
	}
	if baseTree, _ := git(repo.Root, nil, "rev-parse", base+"^{tree}"); baseTree == tree {
		return nil // empty Changeset
	}
	if !edited {
		return nil // the turn didn't touch the working tree
	}
	var split struct {
		Result struct {
			Pane struct {
				ID string `json:"pane_id"`
			} `json:"pane"`
		} `json:"result"`
	}
	if err := herdrJSON(&split, "pane", "split", os.Getenv("HERDR_PANE_ID"), "--direction", "right", "--no-focus", "--cwd", repo.Root); err != nil {
		return err
	}
	id := split.Result.Pane.ID
	if id == "" {
		return fmt.Errorf("herdr pane split returned no pane id")
	}
	self, err := os.Executable()
	if err != nil {
		self = "agentpr"
	}
	if err := herdrJSON(nil, "pane", "rename", id, paneLabel); err != nil {
		return err
	}
	return herdrJSON(nil, "pane", "run", id, "exec '"+strings.ReplaceAll(self, "'", `'\''`)+"'")
}

func herdrJSON(v any, args ...string) error {
	out, err := exec.Command("herdr", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("herdr %s: %v: %s", strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	if v == nil {
		return nil
	}
	return json.Unmarshal(out, v)
}

func settingsPath() (string, error) {
	home, err := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json"), err
}

// editSettings rewrites Claude's settings.json through f, backing it up first.
// ponytail: re-marshalling sorts keys; content is preserved, key order is not.
func editSettings(path string, f func(stop []any) []any) (changed bool, err error) {
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	settings := map[string]any{}
	if len(bytes.TrimSpace(b)) > 0 {
		d := json.NewDecoder(bytes.NewReader(b))
		d.UseNumber()
		if err := d.Decode(&settings); err != nil {
			return false, fmt.Errorf("%s: %w", path, err)
		}
	}
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok && settings["hooks"] != nil {
		return false, fmt.Errorf("%s: \"hooks\" is not an object", path)
	}
	if hooks == nil {
		hooks = map[string]any{}
	}
	stop, ok := hooks["Stop"].([]any)
	if !ok && hooks["Stop"] != nil {
		return false, fmt.Errorf("%s: \"hooks.Stop\" is not an array", path)
	}
	stop = f(stop)
	if len(stop) > 0 {
		hooks["Stop"] = stop
	} else {
		delete(hooks, "Stop")
	}
	if len(hooks) > 0 {
		settings["hooks"] = hooks
	} else {
		delete(settings, "hooks")
	}
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return false, err
	}
	out = append(out, '\n')
	if b != nil && bytes.Equal(bytes.TrimSpace(out), bytes.TrimSpace(b)) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if b != nil {
		if err := os.WriteFile(path+".bak", b, 0o600); err != nil {
			return false, err
		}
	}
	// WriteFile, not rename: settings.json is often a symlink into a dotfiles repo.
	return true, os.WriteFile(path, out, 0o644)
}

func isOurHook(h any) bool {
	m, _ := h.(map[string]any)
	return m != nil && m["command"] == hookCmd
}

func installHook(path string) (bool, error) {
	return editSettings(path, func(stop []any) []any {
		for _, g := range stop {
			if m, _ := g.(map[string]any); m != nil {
				hs, _ := m["hooks"].([]any)
				for _, h := range hs {
					if isOurHook(h) {
						return stop
					}
				}
			}
		}
		return append(stop, map[string]any{"hooks": []any{map[string]any{"type": "command", "command": hookCmd}}})
	})
}

func uninstallHook(path string) (bool, error) {
	return editSettings(path, func(stop []any) []any {
		var keep []any
		for _, g := range stop {
			m, _ := g.(map[string]any)
			hs, ok := m["hooks"].([]any)
			if !ok {
				keep = append(keep, g)
				continue
			}
			var rest []any
			for _, h := range hs {
				if !isOurHook(h) {
					rest = append(rest, h)
				}
			}
			if len(rest) == len(hs) {
				keep = append(keep, g)
			} else if len(rest) > 0 {
				m["hooks"] = rest
				keep = append(keep, m)
			}
		}
		return keep
	})
}

// herdrWorkspace is the herdr workspace open on the worktree root, or "" outside herdr or when there's none.
func herdrWorkspace(root string) string {
	if os.Getenv("HERDR_ENV") != "1" {
		return ""
	}
	var list struct {
		Result struct {
			Worktrees []struct {
				Path string `json:"path"`
				WS   string `json:"open_workspace_id"`
			} `json:"worktrees"`
		} `json:"result"`
	}
	if herdrJSON(&list, "worktree", "list", "--cwd", root) != nil {
		return ""
	}
	for _, w := range list.Result.Worktrees {
		if w.Path == root {
			return w.WS
		}
	}
	return ""
}
