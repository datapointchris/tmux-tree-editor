// Command tmux-tree-editor arranges a tmux server's sessions, windows and
// panes in columns, and applies the staged edit when it is confirmed.
package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/datapointchris/tmux-tree-editor/tmux"
	"github.com/datapointchris/tmux-tree-editor/ui"
)

func main() {
	name := flag.String("L", "", "edit the tmux server on this socket name, as tmux -L takes it")
	path := flag.String("S", "", "edit the tmux server on this socket path, as tmux -S takes it")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Usage = func() {
		_, _ = fmt.Fprintf(flag.CommandLine.Output(), `usage: tmux-tree-editor [-L name | -S path] [-version]

Shows the sessions, windows and panes of a tmux server as columns. Cut, paste,
rename, add and kill are staged; w lists the tmux commands the edit needs, and
y runs them. Without -L or -S it edits the server it runs inside.

examples:
  tmux-tree-editor              edit the server this pane belongs to
  tmux-tree-editor -L scratch   edit the server tmux -L scratch started

`)
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVersion {
		fmt.Println(currentVersion())
		return
	}

	client := tmux.Client{}
	self := os.Getenv("TMUX_PANE")
	switch {
	case *name != "" && *path != "":
		fmt.Fprintln(os.Stderr, "tmux-tree-editor: -L and -S name two servers; give one")
		os.Exit(2)
	case *name != "":
		client.Socket, self = []string{"-L", *name}, ""
	case *path != "":
		client.Socket, self = []string{"-S", *path}, ""
	}

	model, err := ui.New(client, self)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tmux-tree-editor: cannot read the tmux server: %v\n", err)
		os.Exit(1)
	}
	if _, err := tea.NewProgram(model, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "tmux-tree-editor: %v\n", err)
		os.Exit(1)
	}
}
