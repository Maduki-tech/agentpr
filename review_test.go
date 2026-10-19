package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildPrompt(t *testing.T) {
	p := buildPrompt([]*Thread{
		{ID: "t1", Path: "a.go", Line: 7, Lines: []string{"+foo()", "+bar()"}, Comments: []Comment{
			{Author: Reviewer, Body: "rename\nthis"},
			{Author: Author, Body: "done"},
		}},
		{ID: "t2", Comments: []Comment{{Author: Reviewer, Body: "overall ok"}}},
	})
	for _, want := range []string{
		"(2 threads)", "agentpr reply <thread-id>",
		"## t1 · a.go:7-8", "```diff\n+foo()\n+bar()\n```",
		"reviewer: rename\n  this", "you: done",
		"## t2 · (whole changeset)", "reviewer: overall ok",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q:\n%s", want, p)
		}
	}
	if strings.Count(p, "```diff") != 1 {
		t.Error("a Thread without a snippet must not get a diff block")
	}
}

// fakeBin puts executable shell scripts named after cmds in a fresh dir that becomes all of PATH, so scripts may use only shell builtins;
// @OUT@ in a script stands for the file "out" in that dir.
func fakeBin(t *testing.T, scripts map[string]string) string {
	dir := t.TempDir()
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+strings.ReplaceAll(body, "@OUT@", filepath.Join(dir, "out"))), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	return dir
}

func TestSendPrompt(t *testing.T) {
	dir := fakeBin(t, map[string]string{"herdr": `echo "$@" > @OUT@`})
	if err := sendPrompt("p1", "hello"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "out")); strings.TrimSpace(string(b)) != "agent prompt p1 hello" {
		t.Fatalf("args: %q", b)
	}
	fakeBin(t, map[string]string{"herdr": "echo no such pane; exit 1"})
	if err := sendPrompt("p1", "hello"); err == nil || !strings.Contains(err.Error(), "no such pane") {
		t.Fatalf("herdr failure must surface its output: %v", err)
	}
}

func TestCopyClipboard(t *testing.T) {
	dir := fakeBin(t, map[string]string{"wl-copy": `while read -r l || [ -n "$l" ]; do echo "$l"; done > @OUT@`})
	if err := copyClipboard("text"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "out")); strings.TrimSpace(string(b)) != "text" {
		t.Fatalf("wl-copy got %q", b)
	}
	// wl-copy fails: fall back to xclip.
	dir = fakeBin(t, map[string]string{
		"wl-copy": "exit 1",
		"xclip":   `while read -r l || [ -n "$l" ]; do echo "$l"; done > @OUT@`,
	})
	if err := copyClipboard("viaxclip"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "out")); strings.TrimSpace(string(b)) != "viaxclip" {
		t.Fatalf("xclip got %q", b)
	}
	fakeBin(t, nil)
	if copyClipboard("x") == nil {
		t.Fatal("no clipboard tool must error")
	}
}

func TestAgentsFiltersByCwdAndKind(t *testing.T) {
	fakeBin(t, map[string]string{"herdr": `echo '{"result":{"agents":[
 {"pane_id":"1","agent":"claude","cwd":"/r"},
 {"pane_id":"2","agent":"claude","cwd":"/r/sub"},
 {"pane_id":"3","agent":"claude","cwd":"/rx"},
 {"pane_id":"4","agent":"codex","cwd":"/r"}]}}'`})
	as, err := agents("/r")
	if err != nil || len(as) != 2 || as[0].Pane != "1" || as[1].Pane != "2" {
		t.Fatalf("got %+v, %v", as, err)
	}
	fakeBin(t, map[string]string{"herdr": "echo nope"})
	if _, err := agents("/r"); err == nil {
		t.Fatal("bad JSON must error")
	}
}
