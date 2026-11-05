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
	"slices"
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
	moving
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
	// grabbedUndo and grabbedCur are where M grabbed its row, for esc to put it back.
	grabbedUndo int
	grabbedCur  [3]int
	col         int
	cur         [3]int
	// cut holds the ids x cut, in tree order, all of the kind cutKind names.
	cut     []string
	cutKind int
	// selected holds the ids space selected, all of the kind selKind names.
	selected map[string]bool
	selKind  int
	mode     mode
	input    textinput.Model
	steps    []plan.Step
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
	m.clearMarks()
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
		case k == "D" && m.closing:
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
	if m.mode == moving {
		m.moveKey(k)
		m.clamp()
		m.refreshPreview()
		return m, nil
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
		{[]string{"]"}, "[/] jump", "the next session's windows, or the next window's panes", func(m *Model) tea.Cmd { m.jump(1); return nil }},
		{[]string{"["}, "", "the previous session's windows, or the previous window's panes", func(m *Model) tea.Cmd { m.jump(-1); return nil }},
		{[]string{"g", "home"}, "", "the first row", func(m *Model) tea.Cmd { m.move(-1 << 20); return nil }},
		{[]string{"G", "end"}, "", "the last row", func(m *Model) tea.Cmd { m.move(1 << 20); return nil }},
		{[]string{"ctrl+d", "pgdown"}, "", "half a screen down", func(m *Model) tea.Cmd { m.move(m.halfScreen()); return nil }},
		{[]string{"ctrl+u", "pgup"}, "", "half a screen up", func(m *Model) tea.Cmd { m.move(-m.halfScreen()); return nil }},
		{[]string{" ", "space"}, "space select", "select or unselect the window or pane under the cursor and move down; x and d then act on every selected one", (*Model).toggleSelect},
		{[]string{"x"}, "x cut", "cut the window or pane under the cursor, or everything selected", (*Model).markCut},
		{[]string{"esc"}, "", "clear the cut and the selection", (*Model).clearMarks},
		{[]string{"p"}, "p paste", "paste after the cursor: a pane in the windows column becomes a window, a window in the panes column joins that window", func(m *Model) tea.Cmd { m.paste(); return nil }},
		{[]string{"r"}, "r rename", "rename a session or window, or retitle a pane", (*Model).startRename},
		{[]string{"a"}, "a add", "add a session, window or pane after the cursor", (*Model).startCreate},
		{[]string{"D"}, "D kill", "mark for killing, or press again to keep; with a selection, every selected one", (*Model).toggleKill},
		{[]string{"M"}, "M move", "move it within its column: j and k walk it, enter drops it, esc puts it back", (*Model).grab},
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

// jump moves along the column to the left: the next session's windows, or the next
// window's panes. The sessions column has no parent to jump along.
func (m *Model) jump(delta int) {
	switch m.col {
	case windowsCol:
		m.cur[0] += delta
		m.cur[1], m.cur[2] = 0, 0
	case panesCol:
		m.cur[1] += delta
		m.cur[2] = 0
	}
}

func (m *Model) halfScreen() int { return max(1, (m.height-3)/2) }

var kinds = [3]string{"session", "window", "pane"}

// toggleSelect selects or unselects the window or pane under the cursor and
// moves down, as space does in yazi. A selection holds one kind, so selecting
// a pane drops a selection of windows and the other way round.
func (m *Model) toggleSelect() tea.Cmd {
	if m.col == sessionsCol {
		m.status = "sessions do not move; select their windows"
		return nil
	}
	id := m.currentID()
	if id == "" {
		return nil
	}
	if m.selKind != m.col {
		m.selected, m.selKind = map[string]bool{}, m.col
	}
	if m.selected[id] {
		delete(m.selected, id)
	} else {
		m.selected[id] = true
	}
	m.move(1)
	return nil
}

func (m *Model) clearMarks() tea.Cmd {
	m.cut, m.selected = nil, map[string]bool{}
	return nil
}

// marked lists the selection in the order the tree holds it, or the item
// under the cursor when nothing is selected.
func (m *Model) marked() (ids []string, kind int) {
	if len(m.selected) == 0 {
		if m.col == sessionsCol {
			return nil, m.col
		}
		if id := m.currentID(); id != "" {
			return []string{id}, m.col
		}
		return nil, m.col
	}
	for _, s := range m.want.Sessions {
		for _, w := range s.Windows {
			if m.selKind == windowsCol && m.selected[w.ID] {
				ids = append(ids, w.ID)
			}
			for _, p := range w.Panes {
				if m.selKind == panesCol && m.selected[p.ID] {
					ids = append(ids, p.ID)
				}
			}
		}
	}
	return ids, m.selKind
}

func (m *Model) markCut() tea.Cmd {
	if m.col == sessionsCol && len(m.selected) == 0 {
		m.status = "a session does not move; cut its windows"
		return nil
	}
	ids, kind := m.marked()
	if len(ids) == 0 {
		return nil
	}
	m.cut, m.cutKind, m.selected = ids, kind, map[string]bool{}
	what := fmt.Sprintf("%d %ss", len(ids), kinds[kind])
	if len(ids) == 1 {
		what = kinds[kind]
	}
	if kind == windowsCol {
		m.status = fmt.Sprintf("cut %s; p in a windows column moves them there, p in a panes column moves their panes into that window", what)
	} else {
		m.status = fmt.Sprintf("cut %s; p in a panes column moves them there, p in a windows column makes each a window", what)
	}
	return nil
}

func (m *Model) isCut(id string) bool { return slices.Contains(m.cut, id) }

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

// paste puts everything cut after the cursor, in the order it was cut. The
// target is remembered by id, the cut items are taken out, and the target is
// found again, so taking them out never shifts where they land.
func (m *Model) paste() {
	if len(m.cut) == 0 {
		m.status = "nothing is cut; x cuts the window or pane under the cursor, or everything space selected"
		return
	}
	if m.session() == nil {
		return
	}
	if (m.col == windowsCol && m.cutKind == windowsCol && m.isCut(m.currentID())) ||
		(m.col == panesCol && m.isCut(m.currentID())) {
		m.status = "paste beside something that is not cut"
		return
	}
	if m.col == panesCol && m.cutKind == windowsCol {
		if w := m.window(); w == nil || m.isCut(w.ID) {
			m.status = "a window's panes join another window; put the cursor in that window's panes"
			return
		}
	}
	m.checkpoint()
	switch {
	case m.cutKind == windowsCol && m.col == panesCol:
		m.joinWindows()
	case m.cutKind == windowsCol:
		m.pasteWindows()
	case m.col == panesCol && m.window() != nil:
		m.pastePanes()
	default:
		m.panesToWindows()
	}
	m.cut = nil
	m.restage()
}

// takeWindows removes the cut windows from the tree and returns them.
func (m *Model) takeWindows() []tree.Window {
	var out []tree.Window
	for _, id := range m.cut {
		at, ok := m.want.Locate().Window[id]
		if !ok {
			continue
		}
		s := &m.want.Sessions[at[0]]
		out = append(out, s.Windows[at[1]])
		s.Windows = slices.Delete(s.Windows, at[1], at[1]+1)
	}
	return out
}

// takePanes removes the cut panes from the tree and returns them. A window
// they leave empty stays, marked as closing, until the plan prunes it.
func (m *Model) takePanes() []tree.Pane {
	var out []tree.Pane
	for _, id := range m.cut {
		at, ok := m.want.Locate().Pane[id]
		if !ok {
			continue
		}
		w := &m.want.Sessions[at[0]].Windows[at[1]]
		out = append(out, w.Panes[at[2]])
		w.Panes = slices.Delete(w.Panes, at[2], at[2]+1)
	}
	return out
}

// untouched reports whether a window is still the default one a new session
// is made with: new, unnamed, holding one new untitled pane.
func untouched(w tree.Window) bool {
	return tree.IsNew(w.ID) && w.Name == "" && len(w.Panes) == 1 && tree.IsNew(w.Panes[0].ID) && w.Panes[0].Title == ""
}

// pasteWindows moves the cut windows into the session under the cursor:
// after the cursor's window in the windows column, or at the end from the
// sessions column. A new session's untouched default window gives way to
// the first windows pasted into it.
func (m *Model) pasteWindows() {
	sid, after := m.session().ID, ""
	if m.col == windowsCol {
		if w := m.window(); w != nil {
			after = w.ID
		}
	}
	moving := m.takeWindows()
	si := m.want.Locate().Session[sid]
	s := &m.want.Sessions[si]
	if tree.IsNew(s.ID) && len(s.Windows) == 1 && untouched(s.Windows[0]) {
		s.Windows, after = nil, ""
	}
	at := len(s.Windows)
	if after != "" {
		at = m.want.Locate().Window[after][1] + 1
	}
	s.Windows = slices.Insert(s.Windows, at, moving...)
	m.cur = [3]int{si, at, 0}
}

// joinWindows moves every pane of the cut windows into the window under the
// cursor, after the cursor's pane. Each cut window closes once its panes leave.
func (m *Model) joinWindows() {
	wid, after := m.window().ID, ""
	if p := m.pane(); p != nil {
		after = p.ID
	}
	stacked := stackedAfter(m.cursorPane())
	var moving []tree.Pane
	for _, w := range m.takeWindows() {
		moving = append(moving, w.Panes...)
	}
	m.insertPanes(wid, after, moving, stacked)
}

// pastePanes moves the cut panes into the window under the cursor, after the
// cursor's pane.
func (m *Model) pastePanes() {
	wid, after := m.window().ID, ""
	if p := m.pane(); p != nil {
		after = p.ID
	}
	stacked := stackedAfter(m.cursorPane())
	m.insertPanes(wid, after, m.takePanes(), stacked)
}

func (m *Model) insertPanes(wid, after string, panes []tree.Pane, stacked bool) {
	loc := m.want.Locate()
	at := loc.Window[wid]
	w := &m.want.Sessions[at[0]].Windows[at[1]]
	pos := 0
	if after != "" {
		pos = loc.Pane[after][2] + 1
	}
	for i := range panes {
		panes[i].Stacked = stacked
	}
	w.Panes = slices.Insert(w.Panes, pos, panes...)
	m.cur = [3]int{at[0], at[1], pos}
}

// panesToWindows makes each cut pane a window of its own, after the cursor's
// window, or at the end of the session from the sessions column.
func (m *Model) panesToWindows() {
	sid, after := m.session().ID, ""
	if m.col == windowsCol {
		if w := m.window(); w != nil {
			after = w.ID
		}
	}
	panes := m.takePanes()
	si := m.want.Locate().Session[sid]
	s := &m.want.Sessions[si]
	at := len(s.Windows)
	if after != "" {
		at = m.want.Locate().Window[after][1] + 1
	}
	windows := make([]tree.Window, len(panes))
	for i, p := range panes {
		p.Stacked = false
		windows[i] = tree.Window{ID: m.newID(), Panes: []tree.Pane{p}}
	}
	s.Windows = slices.Insert(s.Windows, at, windows...)
	m.cur = [3]int{si, at, 0}
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

// toggleKill marks the item under the cursor, or every selected one, for
// killing. A selection all marked already is kept instead.
func (m *Model) toggleKill() tea.Cmd {
	ids := []string{m.currentID()}
	if len(m.selected) > 0 {
		ids, _ = m.marked()
	}
	if ids[0] == "" {
		return nil
	}
	keep := true
	for _, id := range ids {
		if m.holdsSelf(id) {
			m.status = "that holds the pane this editor runs in"
			return nil
		}
		keep = keep && m.killed[id]
	}
	m.checkpoint()
	for _, id := range ids {
		if keep {
			delete(m.killed, id)
		} else {
			m.killed[id] = true
		}
	}
	m.selected = map[string]bool{}
	m.restage()
	return nil
}

func (m *Model) shift(delta int) {
	switch m.col {
	case sessionsCol:
		i, j := m.cur[0], m.cur[0]+delta
		if j < 0 || j >= len(m.want.Sessions) {
			return
		}
		m.checkpoint()
		m.want.Sessions[i], m.want.Sessions[j] = m.want.Sessions[j], m.want.Sessions[i]
		m.cur[0] = j
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

// grab starts moving the row under the cursor within its column.
func (m *Model) grab() tea.Cmd {
	if m.currentID() == "" {
		return nil
	}
	m.mode, m.grabbedUndo, m.grabbedCur = moving, len(m.undo), m.cur
	return nil
}

// moveKey walks the grabbed row with j and k. Enter drops it, and esc puts it back.
func (m *Model) moveKey(k string) {
	switch k {
	case "j", "down":
		m.shift(1)
	case "k", "up":
		m.shift(-1)
	case "enter":
		m.mode = browsing
	case "esc":
		for len(m.undo) > m.grabbedUndo {
			m.undoLast()
		}
		m.cur = m.grabbedCur
		m.mode = browsing
	}
}

func (m *Model) undoLast() tea.Cmd {
	if len(m.undo) == 0 {
		m.status = "nothing to undo"
		return nil
	}
	last := m.undo[len(m.undo)-1]
	m.undo = m.undo[:len(m.undo)-1]
	m.want, m.killed = last.want, last.killed
	m.clearMarks()
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
