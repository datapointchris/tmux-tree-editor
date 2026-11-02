// Package tmux runs the tmux binary and reads a server into a tree.
//
// Every call goes through Run, which puts the socket flag on every command.
// Given a socket, it also strips $TMUX and $TMUX_PANE from the child's
// environment: tmux reads $TMUX to find the server a client is inside, so a
// command run from inside a session with the variable intact can reach the
// user's live server whatever other flags it carries.
package tmux

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/datapointchris/tmux-tree-editor/tree"
)

type Client struct {
	// Socket is the flag pair naming the server: {"-L", name}, {"-S", path},
	// or nil for the server the caller is attached to.
	Socket []string
}

func (c Client) Run(args ...string) (string, error) {
	cmd := exec.Command("tmux", append(append([]string{}, c.Socket...), args...)...)
	if len(c.Socket) > 0 {
		cmd.Env = withoutClientVars(os.Environ())
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %s", strings.Join(args, " "), strings.TrimSpace(stderr.String()+" "+err.Error()))
	}
	return strings.TrimRight(string(out), "\n"), nil
}

func withoutClientVars(env []string) []string {
	out := env[:0:0]
	for _, kv := range env {
		if strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_PANE=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

const paneFormat = "#{session_id}\t#{session_name}\t#{window_id}\t#{window_name}\t#{pane_id}\t#{pane_title}\t#{pane_current_command}\t#{pane_current_path}"

// Read lists every pane on the server, in tmux's own order: sessions, then
// window index, then pane index.
func (c Client) Read() (tree.Tree, error) {
	out, err := c.Run("list-panes", "-a", "-F", paneFormat)
	if err != nil {
		return tree.Tree{}, err
	}
	var t tree.Tree
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		f := strings.SplitN(line, "\t", 8)
		if len(f) != 8 {
			return tree.Tree{}, fmt.Errorf("unreadable list-panes line: %q", line)
		}
		if n := len(t.Sessions); n == 0 || t.Sessions[n-1].ID != f[0] {
			t.Sessions = append(t.Sessions, tree.Session{ID: f[0], Name: f[1]})
		}
		s := &t.Sessions[len(t.Sessions)-1]
		if n := len(s.Windows); n == 0 || s.Windows[n-1].ID != f[2] {
			s.Windows = append(s.Windows, tree.Window{ID: f[2], Name: f[3]})
		}
		w := &s.Windows[len(s.Windows)-1]
		w.Panes = append(w.Panes, tree.Pane{ID: f[4], Title: f[5], Command: f[6], Path: f[7]})
	}
	return t, nil
}

// Capture returns the last lines a pane shows, for the preview column.
func (c Client) Capture(paneID string, lines int) string {
	out, err := c.Run("capture-pane", "-p", "-t", paneID)
	if err != nil {
		return ""
	}
	rows := strings.Split(strings.TrimRight(out, "\n "), "\n")
	if len(rows) > lines {
		rows = rows[len(rows)-lines:]
	}
	return strings.Join(rows, "\n")
}
