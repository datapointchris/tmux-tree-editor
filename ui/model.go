// Package ui is the editor: three columns of sessions, windows and panes, a
// preview of the pane under the cursor, and a staged edit that nothing applies
// until w or q shows the plan and y confirms it.
//
// Edits change want, a copy of the tree the editor loaded. Killing marks an id
// rather than removing it, so a killed item stays on screen, struck through,
// until the plan runs. Cut marks an item and paste moves it, as in yazi.
package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/datapointchris/tmux-tree-editor/plan"
	"github.com/datapointchris/tmux-tree-editor/tree"
)

type Source interface {
	plan.Runner
	Read() (tree.Tree, error)
	Capture(paneID string, lines int) string
}

const (
	sessionsCol = iota
	windowsCol
	panesCol
)

type mode int

const (
	browsing mode = iota
	renaming
	creating
	reviewing
	quitting
	helping
)

type snapshot struct {
	want   tree.Tree
	killed map[string]bool
}

type Model struct {
	src    Source
	self   string
	base   tree.Tree
	want   tree.Tree
	killed map[string]bool
	undo   []snapshot
	col    int
	cur    [3]int
	cut    string
	mode   mode
	input  textinput.Model
	steps  []plan.Step
	// closing is set when the plan on screen came from q, so y applies and quits.
	closing bool
	staged  int
	status  string
	width   int
	height  int
	next    int
	preview string
	shown   *tree.Pane
}

// New loads the server. self is the pane the editor runs in, which it refuses
// to kill; it is empty when the editor drives another server.
func New(src Source, self string) (*Model, error) {
	t, err := src.Read()
	if err != nil {
		return nil, err
	}
	in := textinput.New()
	in.CharLimit = 200
	m := &Model{src: src, self: self, input: in}
	m.load(t)
	return m, nil
}

func (m *Model) load(t tree.Tree) {
	m.base = t
	m.want = t.Clone()
	m.killed = map[string]bool{}
	m.undo = nil
	m.cut = ""
	m.restage()
	m.clamp()
	m.refreshPreview()
}

func (m *Model) session() *tree.Session {
	if len(m.want.Sessions) == 0 {
		return nil
	}
	return &m.want.Sessions[m.cur[0]]
}

func (m *Model) window() *tree.Window {
	s := m.session()
	if s == nil || len(s.Windows) == 0 {
		return nil
	}
	return &s.Windows[m.cur[1]]
}

func (m *Model) pane() *tree.Pane {
	w := m.window()
	if w == nil || len(w.Panes) == 0 {
		return nil
	}
	return &w.Panes[m.cur[2]]
}

func clampTo(i, n int) int {
	switch {
	case n == 0 || i < 0:
		return 0
	case i >= n:
		return n - 1
	}
	return i
}

func (m *Model) clamp() {
	m.cur[0] = clampTo(m.cur[0], len(m.want.Sessions))
	n := 0
	if s := m.session(); s != nil {
		n = len(s.Windows)
	}
	m.cur[1] = clampTo(m.cur[1], n)
	n = 0
	if w := m.window(); w != nil {
		n = len(w.Panes)
	}
	m.cur[2] = clampTo(m.cur[2], n)
}

// edited is want without the killed items: the tree the plan aims for.
func (m *Model) edited() tree.Tree {
	t := m.want.Clone()
	var sessions []tree.Session
	for _, s := range t.Sessions {
		if m.killed[s.ID] {
			continue
		}
		var windows []tree.Window
		for _, w := range s.Windows {
			if m.killed[w.ID] {
				continue
			}
			var panes []tree.Pane
			for _, p := range w.Panes {
				if !m.killed[p.ID] {
					panes = append(panes, p)
				}
			}
			w.Panes = panes
			windows = append(windows, w)
		}
		s.Windows = windows
		sessions = append(sessions, s)
	}
	t.Sessions = sessions
	return t
}

func (m *Model) restage() {
	steps, err := plan.Diff(m.base, m.base, m.edited())
	m.staged = 0
	if err == nil {
		m.staged = plan.Shown(steps)
	}
}

func (m *Model) checkpoint() {
	killed := make(map[string]bool, len(m.killed))
	for id, v := range m.killed {
		killed[id] = v
	}
	m.undo = append(m.undo, snapshot{want: m.want.Clone(), killed: killed})
}

func (m *Model) newID() string {
	m.next++
	return fmt.Sprintf("+%d", m.next)
}

func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *Model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	switch m.mode {
	case renaming, creating:
		switch k {
		case "enter":
			m.commitInput()
			return m, nil
		case "esc":
			m.mode = browsing
			m.input.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	case reviewing:
		switch {
		case k == "y":
			if m.apply() && m.closing {
				return m, tea.Quit
			}
		case k == "d" && m.closing:
			return m, tea.Quit
		case k == "n" || k == "esc" || k == "q":
			m.mode = browsing
		}
		return m, nil
	case quitting:
		if k == "y" {
			return m, tea.Quit
		}
		m.mode = browsing
		return m, nil
	case helping:
		m.mode = browsing
		return m, nil
	}
	if k == "ctrl+c" {
		return m, tea.Quit
	}
	for _, a := range actions {
		for _, key := range a.keys {
			if key == k {
				m.status = ""
				cmd := a.run(m)
				m.clamp()
				m.refreshPreview()
				return m, cmd
			}
		}
	}
	return m, nil
}

type action struct {
	keys   []string
	footer string
	help   string
	run    func(*Model) tea.Cmd
}

var actions []action

func init() {
	actions = []action{
		{[]string{"j", "down"}, "j/k move", "down a row", func(m *Model) tea.Cmd { m.move(1); return nil }},
		{[]string{"k", "up"}, "", "up a row", func(m *Model) tea.Cmd { m.move(-1); return nil }},
		{[]string{"h", "left"}, "h/l column", "the column to the left", func(m *Model) tea.Cmd { m.col = max(m.col-1, sessionsCol); return nil }},
		{[]string{"l", "right"}, "", "the column to the right", func(m *Model) tea.Cmd { m.col = min(m.col+1, panesCol); return nil }},
		{[]string{"g"}, "", "the first row", func(m *Model) tea.Cmd { m.move(-1 << 20); return nil }},
		{[]string{"G"}, "", "the last row", func(m *Model) tea.Cmd { m.move(1 << 20); return nil }},
		{[]string{"x"}, "x cut", "cut the window or pane under the cursor", (*Model).markCut},
		{[]string{"esc"}, "", "clear the cut", func(m *Model) tea.Cmd { m.cut = ""; return nil }},
		{[]string{"p"}, "p paste", "paste after the cursor: a pane in the windows column becomes a window, a window in the panes column joins that window", func(m *Model) tea.Cmd { m.paste(); return nil }},
		{[]string{"r"}, "r rename", "rename a session or window, or retitle a pane", (*Model).startRename},
		{[]string{"a"}, "a add", "add a session, window or pane after the cursor", (*Model).startCreate},
		{[]string{"d"}, "d kill", "mark for killing, or press again to keep", (*Model).toggleKill},
		{[]string{"J"}, "J/K shift", "shift down within its column", func(m *Model) tea.Cmd { m.shift(1); return nil }},
		{[]string{"K"}, "", "shift up within its column", func(m *Model) tea.Cmd { m.shift(-1); return nil }},
		{[]string{"u"}, "u undo", "undo the last edit", (*Model).undoLast},
		{[]string{"w"}, "w apply", "review the plan, then apply it with y", func(m *Model) tea.Cmd { m.closing = false; return m.review() }},
		{[]string{"R"}, "", "reload from tmux, discarding the edit", (*Model).reload},
		{[]string{"?"}, "? keys", "show these keys", func(m *Model) tea.Cmd { m.mode = helping; return nil }},
		{[]string{"q"}, "q close", "close; with anything staged, show the plan to apply or discard", (*Model).quit},
	}
}

func (m *Model) move(delta int) {
	m.cur[m.col] += delta
	switch m.col {
	case sessionsCol:
		m.cur[1], m.cur[2] = 0, 0
	case windowsCol:
		m.cur[2] = 0
	}
}

func (m *Model) markCut() tea.Cmd {
	switch m.col {
	case sessionsCol:
		m.status = "a session does not move; cut its windows"
	case windowsCol:
		if w := m.window(); w != nil {
			m.cut = w.ID
			m.status = fmt.Sprintf("cut window %q; go where it belongs and press p", w.Name)
		}
	case panesCol:
		if p := m.pane(); p != nil {
			m.cut = p.ID
			m.status = fmt.Sprintf("cut pane %q; p beside another pane moves it there, p in the windows column makes it a window", m.want.Label(*p))
		}
	}
	return nil
}

// stackedAfter says whether a pane placed after anchor goes below it. A pane
// splits along its longer side, counting a cell as twice as tall as it is
// wide, the way dwindle tiling in bspwm or Hyprland does.
func stackedAfter(anchor *tree.Pane) bool {
	if anchor == nil || anchor.Width == 0 {
		return false
	}
	return anchor.Width < 2*anchor.Height
}

func (m *Model) cursorPane() *tree.Pane {
	p := m.pane()
	if p == nil {
		return nil
	}
	c := *p
	return &c
}

func insertWindow(ws []tree.Window, at int, w tree.Window) []tree.Window {
	at = min(max(at, 0), len(ws))
	out := append([]tree.Window{}, ws[:at]...)
	out = append(out, w)
	return append(out, ws[at:]...)
}

func insertPane(ps []tree.Pane, at int, p tree.Pane) []tree.Pane {
	at = min(max(at, 0), len(ps))
	out := append([]tree.Pane{}, ps[:at]...)
	out = append(out, p)
	return append(out, ps[at:]...)
}

func (m *Model) paste() {
	if m.cut == "" {
		m.status = "nothing is cut; x marks a window or pane"
		return
	}
	loc := m.want.Locate()
	if from, ok := loc.Window[m.cut]; ok {
		if m.col == panesCol {
			if w := m.window(); w == nil || w.ID == m.cut {
				m.status = "a window's panes join another window; put the cursor in that window's panes"
				return
			}
			m.checkpoint()
			m.joinWindow(from)
		} else {
			m.checkpoint()
			m.pasteWindow(from)
		}
	} else if from, ok := loc.Pane[m.cut]; ok {
		m.checkpoint()
		m.pastePane(from)
	}
	m.cut = ""
	m.restage()
}

// joinWindow moves every pane of the cut window into the window under the
// cursor, after the cursor's pane. The cut window closes once they leave.
func (m *Model) joinWindow(from [2]int) {
	targetID := m.window().ID
	stacked := stackedAfter(m.cursorPane())
	src := &m.want.Sessions[from[0]]
	moving := src.Windows[from[1]]
	src.Windows = append(src.Windows[:from[1]:from[1]], src.Windows[from[1]+1:]...)
	at := m.want.Locate().Window[targetID]
	m.cur[0], m.cur[1] = at[0], at[1]
	dst := &m.want.Sessions[at[0]].Windows[at[1]]
	pos := 0
	if len(dst.Panes) > 0 {
		pos = m.cur[2] + 1
	}
	for i, p := range moving.Panes {
		p.Stacked = stacked
		dst.Panes = insertPane(dst.Panes, pos+i, p)
	}
	m.cur[2] = pos
}

func (m *Model) pasteWindow(from [2]int) {
	src := &m.want.Sessions[from[0]]
	w := src.Windows[from[1]]
	src.Windows = append(src.Windows[:from[1]:from[1]], src.Windows[from[1]+1:]...)
	to := m.cur[0]
	at := len(m.want.Sessions[to].Windows)
	if m.col == windowsCol {
		at = m.cur[1] + 1
		if to == from[0] && from[1] < at {
			at--
		}
	}
	dst := &m.want.Sessions[to]
	dst.Windows = insertWindow(dst.Windows, at, w)
	m.cur[1] = at
}

func (m *Model) pastePane(from [3]int) {
	srcWindow := &m.want.Sessions[from[0]].Windows[from[1]]
	p := srcWindow.Panes[from[2]]
	p.Stacked = m.col == panesCol && stackedAfter(m.cursorPane())
	srcWindow.Panes = append(srcWindow.Panes[:from[2]:from[2]], srcWindow.Panes[from[2]+1:]...)
	if w := m.window(); m.col == panesCol && w != nil {
		at := m.cur[2] + 1
		if w.ID == srcWindow.ID && from[2] < at {
			at--
		}
		w.Panes = insertPane(w.Panes, at, p)
		m.cur[2] = at
		return
	}
	s := m.session()
	at := len(s.Windows)
	if m.col == windowsCol {
		at = m.cur[1] + 1
	}
	s.Windows = insertWindow(s.Windows, at, tree.Window{ID: m.newID(), Panes: []tree.Pane{p}})
	m.cur[1], m.cur[2] = at, 0
}

func (m *Model) currentName() (string, bool) {
	switch m.col {
	case sessionsCol:
		if s := m.session(); s != nil {
			return s.Name, true
		}
	case windowsCol:
		if w := m.window(); w != nil {
			return w.Name, true
		}
	case panesCol:
		if p := m.pane(); p != nil {
			return p.Title, true
		}
	}
	return "", false
}

func (m *Model) currentID() string {
	switch m.col {
	case sessionsCol:
		if s := m.session(); s != nil {
			return s.ID
		}
	case windowsCol:
		if w := m.window(); w != nil {
			return w.ID
		}
	case panesCol:
		if p := m.pane(); p != nil {
			return p.ID
		}
	}
	return ""
}

var prompts = [3]string{"session name: ", "window name (empty names itself): ", "pane title: "}

func (m *Model) startRename() tea.Cmd {
	name, ok := m.currentName()
	if !ok {
		return nil
	}
	m.mode = renaming
	m.input.Prompt = "rename to " + prompts[m.col]
	m.input.SetValue(name)
	m.input.CursorEnd()
	return m.input.Focus()
}

func (m *Model) startCreate() tea.Cmd {
	if m.col != sessionsCol && m.session() == nil {
		m.status = "add a session first"
		return nil
	}
	if m.col == panesCol && m.window() == nil {
		m.status = "add a window first"
		return nil
	}
	m.mode = creating
	m.input.Prompt = "new " + prompts[m.col]
	m.input.SetValue("")
	return m.input.Focus()
}

func (m *Model) commitInput() {
	value := strings.TrimSpace(m.input.Value())
	m.input.Blur()
	creating := m.mode == creating
	m.mode = browsing
	if m.col == sessionsCol && (value == "" || strings.ContainsAny(value, ":.")) {
		m.status = "a session name needs text, and tmux refuses : and . in one"
		return
	}
	m.checkpoint()
	kind := [3]string{"session", "window", "pane"}[m.col]
	switch {
	case creating:
		m.create(value)
		m.status = fmt.Sprintf("staged a new %s %q; w or q shows the plan", kind, value)
	case m.col == sessionsCol:
		m.session().Name = value
	case m.col == windowsCol:
		m.window().Name = value
	case m.col == panesCol:
		m.pane().Title = value
	}
	if !creating {
		m.status = fmt.Sprintf("staged: %s renamed to %q; w or q shows the plan", kind, value)
	}
	m.restage()
}

func pathOf(s *tree.Session, w *tree.Window) string {
	if w != nil && len(w.Panes) > 0 {
		return w.Panes[0].Path
	}
	if s != nil {
		for _, w := range s.Windows {
			if len(w.Panes) > 0 {
				return w.Panes[0].Path
			}
		}
	}
	return ""
}

func (m *Model) create(name string) {
	switch m.col {
	case sessionsCol:
		path := pathOf(m.session(), m.window())
		s := tree.Session{ID: m.newID(), Name: name}
		s.Windows = []tree.Window{{ID: m.newID(), Panes: []tree.Pane{{ID: m.newID(), Path: path}}}}
		at := 0
		if len(m.want.Sessions) > 0 {
			at = m.cur[0] + 1
		}
		m.want.Sessions = append(m.want.Sessions[:at:at], append([]tree.Session{s}, m.want.Sessions[at:]...)...)
		m.cur = [3]int{at, 0, 0}
	case windowsCol:
		s := m.session()
		w := tree.Window{ID: m.newID(), Name: name, Panes: []tree.Pane{{ID: m.newID(), Path: pathOf(s, m.window())}}}
		at := 0
		if len(s.Windows) > 0 {
			at = m.cur[1] + 1
		}
		s.Windows = insertWindow(s.Windows, at, w)
		m.cur[1], m.cur[2] = at, 0
	case panesCol:
		w := m.window()
		path := ""
		anchor := m.cursorPane()
		if anchor != nil {
			path = anchor.Path
		}
		at := 0
		if len(w.Panes) > 0 {
			at = m.cur[2] + 1
		}
		w.Panes = insertPane(w.Panes, at, tree.Pane{ID: m.newID(), Title: name, Path: path, Stacked: stackedAfter(anchor)})
		m.cur[2] = at
	}
}

// holdsSelf reports whether killing id would kill the pane this editor runs in.
func (m *Model) holdsSelf(id string) bool {
	if m.self == "" {
		return false
	}
	at, ok := m.want.Locate().Pane[m.self]
	if !ok {
		return false
	}
	s := m.want.Sessions[at[0]]
	return id == m.self || id == s.ID || id == s.Windows[at[1]].ID
}

func (m *Model) toggleKill() tea.Cmd {
	id := m.currentID()
	if id == "" {
		return nil
	}
	if m.holdsSelf(id) {
		m.status = "that holds the pane this editor runs in"
		return nil
	}
	m.checkpoint()
	if m.killed[id] {
		delete(m.killed, id)
	} else {
		m.killed[id] = true
	}
	m.restage()
	return nil
}

func (m *Model) shift(delta int) {
	switch m.col {
	case sessionsCol:
		m.status = "tmux orders sessions itself"
	case windowsCol:
		s := m.session()
		i, j := m.cur[1], m.cur[1]+delta
		if s == nil || j < 0 || j >= len(s.Windows) {
			return
		}
		m.checkpoint()
		s.Windows[i], s.Windows[j] = s.Windows[j], s.Windows[i]
		m.cur[1] = j
	case panesCol:
		w := m.window()
		i, j := m.cur[2], m.cur[2]+delta
		if w == nil || j < 0 || j >= len(w.Panes) {
			return
		}
		m.checkpoint()
		w.Panes[i], w.Panes[j] = w.Panes[j], w.Panes[i]
		m.cur[2] = j
	}
	m.restage()
}

func (m *Model) undoLast() tea.Cmd {
	if len(m.undo) == 0 {
		m.status = "nothing to undo"
		return nil
	}
	last := m.undo[len(m.undo)-1]
	m.undo = m.undo[:len(m.undo)-1]
	m.want, m.killed = last.want, last.killed
	m.restage()
	return nil
}

func (m *Model) review() tea.Cmd {
	live, err := m.src.Read()
	if err != nil {
		m.status = err.Error()
		return nil
	}
	steps, err := plan.Diff(m.base, live, m.edited())
	if err != nil {
		m.status = err.Error()
		return nil
	}
	if plan.Shown(steps) == 0 {
		m.status = "nothing is staged"
		return nil
	}
	m.steps = steps
	m.mode = reviewing
	return nil
}

// apply runs the plan on screen and reports whether every step ran.
func (m *Model) apply() bool {
	ran, err := plan.Apply(m.src, m.steps)
	if t, rerr := m.src.Read(); rerr == nil {
		m.load(t)
	}
	m.mode = browsing
	if err != nil {
		m.status = fmt.Sprintf("stopped at step %d of %d: %v", ran+1, len(m.steps), err)
		return false
	}
	m.status = fmt.Sprintf("applied %d changes", plan.Shown(m.steps))
	return true
}

func (m *Model) reload() tea.Cmd {
	t, err := m.src.Read()
	if err != nil {
		m.status = err.Error()
		return nil
	}
	m.load(t)
	m.status = "reloaded from tmux"
	return nil
}

// quit closes the editor. With an edit staged it shows the plan instead, so
// closing applies the edit with y or drops it with d; a plan that cannot be
// built leaves only the choice to discard.
func (m *Model) quit() tea.Cmd {
	if m.staged == 0 {
		return tea.Quit
	}
	m.review()
	if m.mode == reviewing {
		m.closing = true
		return nil
	}
	m.mode = quitting
	return nil
}

func (m *Model) refreshPreview() {
	var p *tree.Pane
	if m.col == panesCol {
		p = m.cursorPane()
	} else if w := m.window(); w != nil && len(w.Panes) > 0 {
		c := w.Panes[0]
		p = &c
	}
	m.preview, m.shown = "", p
	if p != nil && !tree.IsNew(p.ID) {
		m.preview = m.src.Capture(p.ID, 200)
	}
}
