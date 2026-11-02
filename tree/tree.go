// Package tree is the editor's model of a tmux server: sessions holding
// windows holding panes, each identified by tmux's own id.
//
// tmux ids ($N, @N, %N) survive renames and reindexing, so they are the only
// identity the editor trusts. An item the editor will create has no tmux id
// yet and carries a "+N" placeholder instead, which the plan resolves to the
// real id once tmux prints it.
package tree

import "strings"

type Pane struct {
	ID      string
	Title   string
	Command string
	Path    string
	// Width and Height are the pane's size in cells when the editor read it.
	Width, Height int
	// Stacked asks for a pane joined or created below its neighbor rather
	// than beside it. It is read only for a pane that moves or is new.
	Stacked bool
}

type Window struct {
	ID    string
	Name  string
	Panes []Pane
}

type Session struct {
	ID      string
	Name    string
	Windows []Window
}

type Tree struct {
	Sessions []Session
	// Host is the server's short host name, which tmux and most shells put in
	// a pane's title when nothing better is there.
	Host string
	// Home is the server's $HOME, written as ~ in a path.
	Home string
}

// IsNew reports whether an id is a placeholder for an item tmux has not made.
func IsNew(id string) bool { return strings.HasPrefix(id, "+") }

func (t Tree) Clone() Tree {
	out := Tree{Sessions: make([]Session, len(t.Sessions)), Host: t.Host, Home: t.Home}
	for si, s := range t.Sessions {
		ns := Session{ID: s.ID, Name: s.Name, Windows: make([]Window, len(s.Windows))}
		for wi, w := range s.Windows {
			ns.Windows[wi] = Window{ID: w.ID, Name: w.Name, Panes: append([]Pane(nil), w.Panes...)}
		}
		out.Sessions[si] = ns
	}
	return out
}

// Short writes a path under the server's home as ~.
func (t Tree) Short(path string) string {
	if t.Home != "" && t.Home != "/" && (path == t.Home || strings.HasPrefix(path, t.Home+"/")) {
		return "~" + path[len(t.Home):]
	}
	return path
}

// Label names a pane for a person. It is the pane's title, unless the title
// is only the host name or the "host:directory" a shell sets, which says no
// more than the directory does.
func (t Tree) Label(p Pane) string {
	title := p.Title
	if title == "" || (t.Host != "" && (title == t.Host || strings.HasPrefix(title, t.Host+":"))) {
		return t.Short(p.Path)
	}
	return title
}

var shells = map[string]bool{"sh": true, "ash": true, "dash": true, "bash": true, "zsh": true, "fish": true, "ksh": true, "nu": true}

// Program is what the pane runs, or "" when that is only its shell.
func (p Pane) Program() string {
	if shells[p.Command] {
		return ""
	}
	return p.Command
}

// Locations maps every id in the tree to where it sits.
type Locations struct {
	Session map[string]int
	Window  map[string][2]int
	Pane    map[string][3]int
}

func (t Tree) Locate() Locations {
	loc := Locations{Session: map[string]int{}, Window: map[string][2]int{}, Pane: map[string][3]int{}}
	for si, s := range t.Sessions {
		loc.Session[s.ID] = si
		for wi, w := range s.Windows {
			loc.Window[w.ID] = [2]int{si, wi}
			for pi, p := range w.Panes {
				loc.Pane[p.ID] = [3]int{si, wi, pi}
			}
		}
	}
	return loc
}

// Prune drops windows with no panes and sessions with no windows, which tmux
// cannot hold: a window dies with its last pane and a session with its last
// window.
func (t *Tree) Prune() {
	sessions := t.Sessions[:0]
	for _, s := range t.Sessions {
		windows := s.Windows[:0]
		for _, w := range s.Windows {
			if len(w.Panes) > 0 {
				windows = append(windows, w)
			}
		}
		s.Windows = windows
		if len(s.Windows) > 0 {
			sessions = append(sessions, s)
		}
	}
	t.Sessions = sessions
}
