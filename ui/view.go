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
	faint    = lipgloss.NewStyle().Faint(true)
	bold     = lipgloss.NewStyle().Bold(true)
	focused  = lipgloss.NewStyle().Reverse(true)
	current  = lipgloss.NewStyle().Bold(true).Underline(true)
	killedSt = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Strikethrough(true)
	cutSt    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	newSt    = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	changeSt = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	rule     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

func fit(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	if n := ansi.StringWidth(s); n < w {
		s += strings.Repeat(" ", w-n)
	}
	return s
}

func block(lines []string, w, h int) string {
	out := make([]string, h)
	for i := range out {
		if i < len(lines) {
			out[i] = fit(lines[i], w)
		} else {
			out[i] = strings.Repeat(" ", w)
		}
	}
	return strings.Join(out, "\n")
}

func (m *Model) widths() []int {
	if m.width >= 110 {
		usable := m.width - 3
		s, w, p := usable*18/100, usable*28/100, usable*26/100
		return []int{s, w, p, usable - s - w - p}
	}
	usable := m.width - 2
	s, w := usable*25/100, usable*35/100
	return []int{s, w, usable - s - w}
}

// row styles one entry: its state color, and the cursor when it is the
// current item of its column.
func (m *Model) row(text string, state lipgloss.Style, col, index int) string {
	if index != m.cur[col] {
		return state.Render(text)
	}
	if col == m.col {
		return focused.Inherit(state).Render(text)
	}
	return current.Inherit(state).Render(text)
}

type mark struct {
	glyph string
	style lipgloss.Style
}

func (m *Model) markFor(id string, renamed, moved bool) mark {
	switch {
	case m.killed[id]:
		return mark{"✗", killedSt}
	case id == m.cut:
		return mark{"✂", cutSt}
	case tree.IsNew(id):
		return mark{"+", newSt}
	case moved:
		return mark{"→", changeSt}
	case renamed:
		return mark{"✎", changeSt}
	}
	return mark{" ", lipgloss.NewStyle()}
}

func (m *Model) sessionsView(w, h int) string {
	lines := []string{bold.Render("sessions")}
	lb := m.base.Locate()
	for i, s := range m.want.Sessions {
		renamed := !tree.IsNew(s.ID) && s.Name != m.base.Sessions[lb.Session[s.ID]].Name
		mk := m.markFor(s.ID, renamed, false)
		text := mk.glyph + " " + s.Name
		if len(s.Windows) == 0 {
			text += "  (closes)"
		}
		lines = append(lines, m.row(text, mk.style, sessionsCol, i))
	}
	return block(lines, w, h)
}

func (m *Model) windowsView(w, h int) string {
	s := m.session()
	if s == nil {
		return block([]string{bold.Render("windows")}, w, h)
	}
	lines := []string{bold.Render("windows") + faint.Render(" · "+s.Name)}
	lb := m.base.Locate()
	for i, win := range s.Windows {
		renamed, moved := false, false
		if at, ok := lb.Window[win.ID]; ok {
			renamed = win.Name != m.base.Sessions[at[0]].Windows[at[1]].Name
			moved = m.base.Sessions[at[0]].ID != s.ID
		}
		mk := m.markFor(win.ID, renamed, moved)
		name := win.Name
		if name == "" {
			name = "(names itself)"
		}
		count := fmt.Sprintf("  %d", len(win.Panes))
		if len(win.Panes) == 0 {
			count = "  (closes)"
		}
		lines = append(lines, m.row(mk.glyph+" "+name+count, mk.style, windowsCol, i))
	}
	return block(lines, w, h)
}

func (m *Model) panesView(w, h int) string {
	win := m.window()
	if win == nil {
		return block([]string{bold.Render("panes")}, w, h)
	}
	lines := []string{bold.Render("panes") + faint.Render(" · "+win.Name)}
	lb := m.base.Locate()
	for i, p := range win.Panes {
		renamed, moved := false, false
		if at, ok := lb.Pane[p.ID]; ok {
			was := m.base.Sessions[at[0]].Windows[at[1]]
			renamed = p.Title != was.Panes[at[2]].Title
			moved = was.ID != win.ID
		}
		mk := m.markFor(p.ID, renamed, moved)
		command := p.Command
		if command == "" {
			command = "(new)"
		}
		text := fmt.Sprintf("%s %-8s %s", mk.glyph, command, p.Title)
		if p.Stacked && (moved || tree.IsNew(p.ID)) {
			text += "  (below)"
		}
		lines = append(lines, m.row(text, mk.style, panesCol, i))
	}
	return block(lines, w, h)
}

func (m *Model) previewView(w, h int) string {
	rows := strings.Split(m.preview, "\n")
	if len(rows) > h-1 {
		rows = rows[len(rows)-(h-1):]
	}
	lines := []string{bold.Render("preview")}
	for _, r := range rows {
		lines = append(lines, faint.Render(ansi.Strip(r)))
	}
	return block(lines, w, h)
}

func (m *Model) header() string {
	staged := faint.Render("nothing staged")
	if m.staged > 0 {
		staged = changeSt.Render(fmt.Sprintf("%d staged", m.staged))
	}
	return fit(bold.Render("tmux-tree-editor")+"  "+staged, m.width)
}

func (m *Model) footer() string {
	var top string
	switch m.mode {
	case renaming, creating:
		top = m.input.View()
	case quitting:
		top = fmt.Sprintf("discard %d staged changes and quit? y/n", m.staged)
	default:
		top = m.status
	}
	var keys []string
	for _, a := range actions {
		if a.footer != "" {
			keys = append(keys, a.footer)
		}
	}
	return fit(top, m.width) + "\n" + fit(faint.Render(strings.Join(keys, "  ")), m.width)
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

func (m *Model) reviewView() string {
	lines := []string{bold.Render(fmt.Sprintf("apply %d changes to tmux?", plan.Shown(m.steps))), ""}
	n, mechanics := 0, 0
	for _, s := range m.steps {
		switch {
		case s.Shown:
			n++
			lines = append(lines, fmt.Sprintf("  %2d  %s", n, s.Say))
		case s.Do == nil:
			mechanics++
		}
	}
	if mechanics > 0 {
		lines = append(lines, "", faint.Render(fmt.Sprintf("  plus %d placeholder and layout commands around them", mechanics)))
	}
	lines = append(lines, "", "y apply   n back to editing")
	return block(lines, m.width, m.height)
}

func (m *Model) helpView() string {
	lines := []string{bold.Render("keys"), ""}
	for _, a := range actions {
		lines = append(lines, fmt.Sprintf("  %-12s %s", strings.Join(a.keys, " "), a.help))
	}
	lines = append(lines, "", faint.Render("any key closes this"))
	return block(lines, m.width, m.height)
}
