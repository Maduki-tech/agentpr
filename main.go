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
	repo, err := openRepo()
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
