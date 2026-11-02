package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/datapointchris/tmux-tree-editor/plan"
	"github.com/datapointchris/tmux-tree-editor/tree"
)

// Colors are palette slots rather than hex, so they follow the terminal theme.
var (
	accent     = lipgloss.Color("6")
	faint      = lipgloss.NewStyle().Faint(true)
	title      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
	header     = lipgloss.NewStyle().Bold(true).Faint(true)
	headerOn   = lipgloss.NewStyle().Bold(true).Foreground(accent).Underline(true)
	cursorOn   = lipgloss.NewStyle().Bold(true).Foreground(accent).Reverse(true)
	cursorOff  = lipgloss.NewStyle().Bold(true).Foreground(accent)
	program    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	keySt      = lipgloss.NewStyle().Bold(true).Foreground(accent)
	stagedSt   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3"))
	killedSt   = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Strikethrough(true)
	cutSt      = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	selectedSt = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
	newSt      = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	changeSt   = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	rule       = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	reset      = "\x1b[0m"
)

func fit(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	if n := ansi.StringWidth(s); n < w {
		s += strings.Repeat(" ", w-n)
	}
	return s
}

// block lays lines into a w by h box. Each line ends with a reset, so a color
// a captured pane leaves open stops at its column.
func block(lines []string, w, h int) string {
	out := make([]string, h)
	for i := range out {
		if i < len(lines) {
			out[i] = fit(lines[i], w) + reset
		} else {
			out[i] = strings.Repeat(" ", w)
		}
	}
	return strings.Join(out, "\n")
}

func (m *Model) widths() []int {
	if m.width >= 110 {
		usable := m.width - 3
		s, w, p := usable*14/100, usable*26/100, usable*24/100
		return []int{s, w, p, usable - s - w - p}
	}
	usable := m.width - 2
	s, w := usable*25/100, usable*40/100
	return []int{s, w, usable - s - w}
}

var columnNames = []string{"sessions", "windows", "panes", "preview"}

func (m *Model) columnHeader(col int) string {
	if col == m.col {
		return headerOn.Render(columnNames[col])
	}
	return header.Render(columnNames[col])
}

type mark struct {
	glyph string
	style lipgloss.Style
}

func (m *Model) markFor(id string, renamed, moved bool) mark {
	switch {
	case m.killed[id]:
		return mark{"✗", killedSt}
	case m.isCut(id):
		return mark{"✂", cutSt}
	case m.selected[id]:
		return mark{"●", selectedSt}
	case tree.IsNew(id):
		return mark{"+", newSt}
	case moved:
		return mark{"→", changeSt}
	case renamed:
		return mark{"✎", changeSt}
	}
	return mark{" ", lipgloss.NewStyle()}
}

// row draws one entry: its mark in the mark's color, then its text. The
// selected entry of each column is drawn in the accent color, as a bar in
// the column the cursor is in.
func (m *Model) row(mk mark, text, note string, col, index, w int) string {
	glyph := mk.style.Render(mk.glyph) + " "
	if note != "" {
		note = "  " + note
	}
	if index == m.cur[col] {
		if col == m.col {
			return glyph + cursorOn.Render(fit(ansi.Strip(text)+note, w-2))
		}
		return glyph + cursorOff.Render(ansi.Strip(text)) + faint.Render(note)
	}
	if mk.glyph != " " {
		text = mk.style.Render(ansi.Strip(text))
	}
	return glyph + text + faint.Render(note)
}

func (m *Model) sessionsView(w, h int) string {
	lines := []string{m.columnHeader(sessionsCol)}
	lb := m.base.Locate()
	moved := m.movedSessions()
	for i, s := range m.want.Sessions {
		renamed := !tree.IsNew(s.ID) && s.Name != m.base.Sessions[lb.Session[s.ID]].Name
		note := ""
		if len(s.Windows) == 0 {
			note = "closes"
		}
		lines = append(lines, m.row(m.markFor(s.ID, renamed, moved[s.ID]), s.Name, note, sessionsCol, i, w))
	}
	return block(lines, w, h)
}

// movedSessions names the loaded sessions whose place among the other loaded
// sessions differs from where they were.
func (m *Model) movedSessions() map[string]bool {
	inWant := map[string]bool{}
	for _, s := range m.want.Sessions {
		inWant[s.ID] = true
	}
	var was, now []string
	for _, s := range m.base.Sessions {
		if inWant[s.ID] {
			was = append(was, s.ID)
		}
	}
	for _, s := range m.want.Sessions {
		if !tree.IsNew(s.ID) {
			now = append(now, s.ID)
		}
	}
	moved := map[string]bool{}
	for i := range now {
		if now[i] != was[i] {
			moved[now[i]] = true
		}
	}
	return moved
}

func (m *Model) windowsView(w, h int) string {
	lines := []string{m.columnHeader(windowsCol)}
	s := m.session()
	if s == nil {
		return block(lines, w, h)
	}
	lb := m.base.Locate()
	for i, win := range s.Windows {
		renamed, moved := false, false
		if at, ok := lb.Window[win.ID]; ok {
			renamed = win.Name != m.base.Sessions[at[0]].Windows[at[1]].Name
			moved = m.base.Sessions[at[0]].ID != s.ID
		}
		name := win.Name
		if name == "" {
			name = "(names itself)"
		}
		note := ""
		switch n := len(win.Panes); {
		case n == 0:
			note = "closes"
		case n > 1:
			note = fmt.Sprintf("%d panes", n)
		}
		lines = append(lines, m.row(m.markFor(win.ID, renamed, moved), name, note, windowsCol, i, w))
	}
	return block(lines, w, h)
}

func (m *Model) paneText(p tree.Pane) string {
	label := m.want.Label(p)
	if tree.IsNew(p.ID) && label == "" {
		label = "(new pane)"
	}
	if prog := p.Program(); prog != "" {
		return program.Render(prog) + "  " + label
	}
	return label
}

func (m *Model) panesView(w, h int) string {
	lines := []string{m.columnHeader(panesCol)}
	win := m.window()
	if win == nil {
		return block(lines, w, h)
	}
	lb := m.base.Locate()
	for i, p := range win.Panes {
		renamed, moved := false, false
		if at, ok := lb.Pane[p.ID]; ok {
			was := m.base.Sessions[at[0]].Windows[at[1]]
			renamed = p.Title != was.Panes[at[2]].Title
			moved = was.ID != win.ID
		}
		note := ""
		if i > 0 && (moved || tree.IsNew(p.ID)) {
			note = "beside"
			if p.Stacked {
				note = "below"
			}
		}
		lines = append(lines, m.row(m.markFor(p.ID, renamed, moved), m.paneText(p), note, panesCol, i, w))
	}
	return block(lines, w, h)
}

func (m *Model) previewView(w, h int) string {
	lines := []string{header.Render("preview")}
	if p := m.shown; p != nil {
		details := []string{}
		if prog := p.Program(); prog != "" {
			details = append(details, prog)
		} else if p.Command != "" {
			details = append(details, p.Command)
		}
		if p.Path != "" {
			details = append(details, m.want.Short(p.Path))
		}
		if p.Width > 0 {
			details = append(details, fmt.Sprintf("%d×%d", p.Width, p.Height))
		}
		lines = append(lines, faint.Render(strings.Join(details, " · ")), rule.Render(strings.Repeat("─", w)))
	}
	rows := strings.Split(m.preview, "\n")
	if room := h - len(lines); len(rows) > room {
		rows = rows[len(rows)-room:]
	}
	return block(append(lines, rows...), w, h)
}

func (m *Model) header() string {
	staged := faint.Render("nothing staged")
	if m.staged > 0 {
		staged = stagedSt.Render(fmt.Sprintf("%d staged", m.staged)) + faint.Render("  w or q shows the plan")
	}
	if n := len(m.selected); n > 0 {
		staged += "  " + selectedSt.Render(fmt.Sprintf("%d %ss selected", n, kinds[m.selKind]))
	}
	return fit(title.Render("tmux-tree-editor")+"  "+staged, m.width)
}

// keyHint colors the key of a footer hint such as "x cut".
func keyHint(hint string) string {
	key, rest, _ := strings.Cut(hint, " ")
	return keySt.Render(key) + " " + faint.Render(rest)
}

func (m *Model) footer() string {
	var top string
	switch m.mode {
	case renaming, creating:
		top = m.input.View()
	case quitting:
		top = fmt.Sprintf("discard %d staged changes and quit? y/n", m.staged)
		if m.status != "" {
			top = m.status + "; " + top
		}
	default:
		top = m.status
	}
	var keys []string
	for _, a := range actions {
		if a.footer != "" {
			keys = append(keys, keyHint(a.footer))
		}
	}
	return fit(top, m.width) + "\n" + fit(strings.Join(keys, "  "), m.width)
}

func (m *Model) View() string {
	if m.width == 0 || m.height < 5 {
		return ""
	}
	switch m.mode {
	case reviewing:
		return m.reviewView()
	case helping:
		return m.helpView()
	}
	h := m.height - 3
	ws := m.widths()
	cols := []string{m.sessionsView(ws[0], h), m.windowsView(ws[1], h), m.panesView(ws[2], h)}
	if len(ws) == 4 {
		cols = append(cols, m.previewView(ws[3], h))
	}
	sep := rule.Render(strings.TrimSuffix(strings.Repeat("│\n", h), "\n"))
	parts := make([]string, 0, 2*len(cols))
	for i, c := range cols {
		if i > 0 {
			parts = append(parts, sep)
		}
		parts = append(parts, c)
	}
	return m.header() + "\n" + lipgloss.JoinHorizontal(lipgloss.Top, parts...) + "\n" + m.footer()
}

// verbStyle colors a plan step by its first word.
func verbStyle(say string) lipgloss.Style {
	verb, _, _ := strings.Cut(say, " ")
	switch verb {
	case "kill":
		return killedSt.Strikethrough(false)
	case "create":
		return newSt
	case "move":
		return lipgloss.NewStyle().Foreground(accent)
	case "rename", "retitle", "let":
		return changeSt
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
}

func (m *Model) reviewView() string {
	lines := []string{title.Render(fmt.Sprintf("apply %d changes to tmux?", plan.Shown(m.steps))), ""}
	n, mechanics := 0, 0
	for _, s := range m.steps {
		switch {
		case s.Shown:
			n++
			lines = append(lines, faint.Render(fmt.Sprintf("  %2d  ", n))+verbStyle(s.Say).Render(s.Say))
		case s.Do == nil:
			mechanics++
		}
	}
	if mechanics > 0 {
		lines = append(lines, "", faint.Render(fmt.Sprintf("  plus %d placeholder, title and layout commands around them", mechanics)))
	}
	choices := keyHint("y apply") + "   " + keyHint("esc back to editing")
	if m.closing {
		choices = keyHint("y apply and close") + "   " + keyHint("d close without applying") + "   " + keyHint("esc back to editing")
	}
	lines = append(lines, "", choices)
	return block(lines, m.width, m.height)
}

func (m *Model) helpView() string {
	lines := []string{title.Render("keys"), ""}
	for _, a := range actions {
		lines = append(lines, "  "+keySt.Render(fmt.Sprintf("%-12s", strings.Join(a.keys, " ")))+" "+a.help)
	}
	lines = append(lines, "", faint.Render("any key closes this"))
	return block(lines, m.width, m.height)
}
