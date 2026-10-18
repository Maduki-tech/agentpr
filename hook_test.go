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
