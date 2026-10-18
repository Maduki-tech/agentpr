package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const usage = `agentpr: review the changes a coding agent made, like a pull request.

  agentpr [--base <ref>]          open the review TUI for the current branch
  agentpr reply <thread-id> <text> answer a Thread (used by the Author)
  agentpr install                  open agentpr next to Claude in herdr after each turn (Claude Code Stop hook)
  agentpr uninstall                remove that hook
  agentpr hook                     the hook itself, run by Claude Code
`

func main() {
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	base := flag.String("base", "", "compare against this ref instead of the detected Base")
	flag.Parse()
	if err := run(*base, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "agentpr:", err)
		os.Exit(1)
	}
}

func run(base string, args []string) error {
	if len(args) == 1 {
		switch args[0] {
		case "hook":
			runHook(os.Stdin)
			return nil
		case "install", "uninstall":
			path, err := settingsPath()
			if err != nil {
				return err
			}
			edit := installHook
			if args[0] == "uninstall" {
				edit = uninstallHook
			}
			changed, err := edit(path)
			if err == nil {
				fmt.Printf("%s: %s (changed: %v)\n", args[0], path, changed)
			}
			return err
		}
	}
	repo, err := openRepo(".")
	if err != nil {
		return err
	}
	if len(args) > 0 && args[0] == "reply" {
		if len(args) < 3 {
			return fmt.Errorf("usage: agentpr reply <thread-id> <text>")
		}
		_, err := updateState(repo.StatePath(), func(s *State) error {
			t := s.Thread(args[1])
			if t == nil {
				return fmt.Errorf("no open thread %q", args[1])
			}
			t.Comments = append(t.Comments, Comment{Author: Author, Body: strings.Join(args[2:], " "), At: time.Now()})
			return nil
		})
		if err == nil {
			fmt.Println("replied to", args[1])
		}
		return err
	}
	if len(args) > 0 {
		flag.Usage()
		os.Exit(2)
	}
	_, err = tea.NewProgram(newModel(repo, base), tea.WithAltScreen(), tea.WithMouseAllMotion()).Run()
	return err
}
