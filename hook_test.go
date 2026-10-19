package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallUninstall(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude", "settings.json")
	if _, err := installHook(path); err != nil { // no file yet
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), hookCmd) {
		t.Fatalf("not installed: %s", b)
	}

	existing := `{"model": "opus", "big": 12345678901234567890, "hooks": {
  "Stop": [{"hooks": [{"type": "command", "command": "notify-send done"}]}],
  "SessionStart": [{"hooks": [{"type": "command", "command": "herdr-agent-state.sh session"}]}]}}`
	os.WriteFile(path, []byte(existing), 0o644)
	if changed, err := installHook(path); err != nil || !changed {
		t.Fatal(changed, err)
	}
	if changed, _ := installHook(path); changed {
		t.Fatal("second install changed the file")
	}
	b, _ := os.ReadFile(path)
	for _, want := range []string{hookCmd, "notify-send done", "herdr-agent-state.sh session", `"opus"`, "12345678901234567890"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %q in %s", want, b)
		}
	}
	if bak, _ := os.ReadFile(path + ".bak"); string(bak) != existing {
		t.Fatalf("backup: %s", bak)
	}

	if changed, err := uninstallHook(path); err != nil || !changed {
		t.Fatal(changed, err)
	}
	b, _ = os.ReadFile(path)
	if strings.Contains(string(b), hookCmd) || !strings.Contains(string(b), "notify-send done") || !strings.Contains(string(b), "SessionStart") {
		t.Fatalf("uninstall: %s", b)
	}
}

func TestTreeChanged(t *testing.T) {
	p := filepath.Join(t.TempDir(), "agentpr", "main.hook-tree")
	for i, c := range []struct {
		tree string
		want bool
	}{{"a", true}, {"a", false}, {"b", true}, {"b", false}, {"a", true}} {
		if got := treeChanged(p, c.tree); got != c.want {
			t.Fatalf("step %d (%s): got %v, want %v", i, c.tree, got, c.want)
		}
	}
}

// hookEnv makes the process look like it runs inside herdr, pane p1 of tab t1.
func hookEnv(t *testing.T) {
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "p1")
	t.Setenv("HERDR_WORKSPACE_ID", "w1")
	t.Setenv("HERDR_TAB_ID", "t1")
	t.Setenv("AGENTPR_AUTO_OPEN", "")
}

// dirtyRepo is testRepo with an untracked file, so the Changeset isn't empty.
func dirtyRepo(t *testing.T) *Repo {
	r := testRepo(t)
	os.WriteFile(filepath.Join(r.Root, "new.txt"), []byte("hi\n"), 0o644)
	return r
}

func herdrWith(panes string) string { return strings.Replace(herdrPanes, "%s", panes, 1) }

func TestOpenPaneOpens(t *testing.T) {
	hookEnv(t)
	r := dirtyRepo(t)
	log := fakeTool(t, "herdr", herdrWith(""))
	if err := openPane(r); err != nil {
		t.Fatal(err)
	}
	got := calls(log)
	if len(got) != 4 || got[0] != "pane list --workspace w1" ||
		got[1] != "pane split p1 --direction right --no-focus --cwd "+r.Root || got[2] != "pane rename p9 agentpr" ||
		!strings.HasPrefix(got[3], "pane run p9 exec '") {
		t.Fatalf("calls: %q", got)
	}

	// The tree is the one recorded last turn, so the next turn opens nothing.
	if err := openPane(r); err != nil {
		t.Fatal(err)
	}
	if got := calls(log); len(got) != 5 || got[4] != "pane list --workspace w1" {
		t.Fatalf("unchanged tree: %q", got)
	}
}

func TestOpenPaneEarlyOuts(t *testing.T) {
	hookEnv(t)
	for _, c := range []struct {
		name  string
		repo  func() *Repo
		panes string
	}{
		{"pane open", func() *Repo { return dirtyRepo(t) }, `{"tab_id":"t1","label":"agentpr","cwd":"%s"}`},
		{"empty Changeset", func() *Repo { return testRepo(t) }, ""},
	} {
		r := c.repo()
		log := fakeTool(t, "herdr", herdrWith(strings.Replace(c.panes, "%s", r.Root, 1)))
		if err := openPane(r); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if hasCall(log, "pane split") {
			t.Errorf("%s: split anyway: %q", c.name, calls(log))
		}
	}

	// A pane of another tab, other repo or other label doesn't count.
	r := dirtyRepo(t)
	log := fakeTool(t, "herdr", herdrWith(`{"tab_id":"t2","label":"agentpr","cwd":"`+r.Root+`"},{"tab_id":"t1","label":"zsh","cwd":"`+r.Root+`"},{"tab_id":"t1","label":"agentpr","cwd":"/elsewhere"}`))
	if err := openPane(r); err != nil || !hasCall(log, "pane run p9") {
		t.Fatalf("others: %v %q", err, calls(log))
	}
}

func TestOpenPaneErrors(t *testing.T) {
	hookEnv(t)
	r := dirtyRepo(t)
	fakeTool(t, "herdr", "echo boom >&2; exit 1")
	if err := openPane(r); err == nil || !strings.Contains(err.Error(), "herdr pane list --workspace w1") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("list fails: %v", err)
	}

	fakeTool(t, "herdr", `if [ "$1 $2" = "pane list" ]; then echo '{"result":{"panes":[]}}'; else echo '{}'; fi`)
	os.Remove(strings.TrimSuffix(r.StatePath(), ".json") + ".hook-tree")
	if err := openPane(r); err == nil || !strings.Contains(err.Error(), "no pane id") {
		t.Fatalf("no id: %v", err)
	}

	fakeTool(t, "herdr", herdrWith("")+"\nif [ \"$1 $2\" = \"pane rename\" ]; then exit 1; fi")
	os.Remove(strings.TrimSuffix(r.StatePath(), ".json") + ".hook-tree")
	if err := openPane(r); err == nil || !strings.Contains(err.Error(), "pane rename") {
		t.Fatalf("rename fails: %v", err)
	}

	fakeTool(t, "herdr", "echo not json")
	if err := openPane(r); err == nil {
		t.Fatal("bad json accepted")
	}

	// A repo with no valid base can't be compared.
	bad := &Repo{Root: t.TempDir(), GitDir: t.TempDir(), Branch: "main"}
	if err := openPane(bad); err == nil {
		t.Fatal("non-repo accepted")
	}
}

// TestRunHook calls runHook several times in one test: each call arms a 2s os.Exit timer,
// and the whole package must finish before the first one fires.
func TestRunHook(t *testing.T) {
	hookEnv(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(t.TempDir()))
	r := dirtyRepo(t)
	in := func(dir string) *strings.Reader { return strings.NewReader(`{"cwd":"` + dir + `"}`) }
	log := fakeTool(t, "herdr", herdrWith(""))

	t.Setenv("AGENTPR_AUTO_OPEN", "0")
	runHook(in(r.Root))
	t.Setenv("AGENTPR_AUTO_OPEN", "")
	t.Setenv("HERDR_ENV", "")
	runHook(in(r.Root))
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "")
	runHook(in(r.Root))
	t.Setenv("HERDR_PANE_ID", "p1")
	runHook(in(t.TempDir())) // not a git repo
	if _, err := os.Stat(log); err == nil {
		t.Fatalf("herdr called by an early-out: %q", calls(log))
	}

	t.Chdir(r.Root) // no cwd in the payload: the working directory
	runHook(strings.NewReader(`{}`))
	if !hasCall(log, "pane run p9") {
		t.Fatalf("not opened: %q", calls(log))
	}

	// Errors only reach hook.log.
	fakeTool(t, "herdr", "echo boom >&2; exit 1")
	runHook(in(r.Root))
	if b, _ := os.ReadFile(filepath.Join(r.GitDir, "agentpr", "hook.log")); !strings.Contains(string(b), "boom") {
		t.Fatalf("hook.log: %q", b)
	}
}

func TestHerdrWorkspace(t *testing.T) {
	t.Setenv("HERDR_ENV", "")
	if ws := herdrWorkspace("/wt"); ws != "" {
		t.Fatalf("outside herdr: %q", ws)
	}
	t.Setenv("HERDR_ENV", "1")
	log := fakeTool(t, "herdr", `echo '{"result":{"worktrees":[{"path":"/other","open_workspace_id":"w0"},{"path":"/wt","open_workspace_id":"w7"}]}}'`)
	if ws := herdrWorkspace("/wt"); ws != "w7" {
		t.Fatalf("got %q", ws)
	}
	if got := calls(log); got[0] != "worktree list --cwd /wt" {
		t.Fatalf("calls: %q", got)
	}
	if ws := herdrWorkspace("/none"); ws != "" {
		t.Fatalf("no workspace: %q", ws)
	}
	fakeTool(t, "herdr", "exit 1")
	if ws := herdrWorkspace("/wt"); ws != "" {
		t.Fatalf("herdr fails: %q", ws)
	}
}

func TestSettingsPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if p, err := settingsPath(); err != nil || p != filepath.Join(home, ".claude", "settings.json") {
		t.Fatalf("got %q %v", p, err)
	}
}

func TestEditSettingsErrors(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"bad json":       `{`,
		"hooks not obj":  `{"hooks": 1}`,
		"stop not array": `{"hooks": {"Stop": 1}}`,
	} {
		p := filepath.Join(dir, "s.json")
		os.WriteFile(p, []byte(content), 0o644)
		if _, err := installHook(p); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
