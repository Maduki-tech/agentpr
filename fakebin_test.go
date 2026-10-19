package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTool puts a shell script named name first on PATH. It appends its arguments, one
// line per call, to the returned log file, then runs body (canned output, exit codes).
func fakeTool(t *testing.T, name, body string) (log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, name+".log")
	script := "#!/bin/sh\necho \"$*\" >> '" + log + "'\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// calls is the fake's log, one argument line per call.
func calls(log string) []string {
	b, _ := os.ReadFile(log)
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// hasCall reports whether some call starts with prefix.
func hasCall(log, prefix string) bool {
	for _, c := range calls(log) {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// noTools leaves nothing on PATH, so `gh` and `herdr` are not found.
func noTools(t *testing.T) { t.Setenv("PATH", t.TempDir()) }

const herdrPanes = `if [ "$1 $2" = "pane list" ]; then echo '{"result":{"panes":[%s]}}'; fi
if [ "$1 $2" = "pane split" ]; then echo '{"result":{"pane":{"pane_id":"p9"}}}'; fi`
