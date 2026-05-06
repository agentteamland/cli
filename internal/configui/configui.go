// Package configui implements the Bubbletea-driven Q&A flow that powers
// `atl config init` and `atl config edit`. Per the atl-config-system
// decision (workspace .claude/docs/atl-config-system.md § Welcome /
// install Q&A UX) this asks the 9 user-tunable keys one screen at a
// time, with a summary at the end.
//
// The flow has these phases:
//
//	welcome   — init only. Title + two-paragraph context + "Start" / "Cancel".
//	entry     — edit only. Brief "editing <path> — Continue / Cancel" notice.
//	question  — one screen per non-skipped question. Three templates:
//	             • boolean (Yes/No, recommended-marked default)
//	             • integer (in-place numeric input + min/max hint)
//	             • enum    (radio list with descriptions)
//	summary   — full diff vs starting values + "Save" / "Edit a value" / "Cancel".
//	editPick  — list of fields to jump back to (returned from summary's "Edit a value").
//	cancel    — y/N confirm before discarding edits.
//
// Skip-when-disabled: when a parent toggle is set to false, dependent
// parameters are omitted from the question stream (per the brainstorm).
// Schema defaults are nonetheless preserved in the saved config — the
// effective behavior is "user sees a tighter Q&A, but the JSON file is
// still fully populated."
//
// Esc behavior: Esc on the first question returns to welcome (init) or
// exits (edit). Ctrl+C raises the cancel-confirm prompt, except on
// the welcome / entry screen where it exits immediately.
//
// The package is testable: the model implements bubbletea's Model
// interface (Init/Update/View) and Update can be exercised directly
// with synthetic tea.Msgs without spinning up a full TTY.
package configui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/agentteamland/cli/internal/config"
)

// Mode controls whether the welcome screen renders (init) or the brief
// entry screen renders (edit).
type Mode int

const (
	// ModeInit shows the welcome screen + full context paragraph.
	ModeInit Mode = iota
	// ModeEdit shows a minimal "editing <path>" screen; otherwise the
	// flow is identical.
	ModeEdit
)

// Result reports the outcome of a Run call.
type Result struct {
	// Cfg is the AtlConfig the user assembled. Unchanged from the
	// initial value when Saved=false.
	Cfg config.AtlConfig
	// Saved is true iff the user hit "Save" on the summary screen.
	// false on cancel, ctrl+c-confirm, or any non-success exit.
	Saved bool
}

// Run starts the Q&A and blocks until the user finishes (Save or Cancel).
//
// initial is the starting point: schema defaults for init, current values
// for edit. editPath is the on-disk path being edited (used by the entry
// screen) — pass "" in init mode.
func Run(mode Mode, initial config.AtlConfig, editPath string) (Result, error) {
	m := newModel(mode, initial, editPath)
	p := tea.NewProgram(m)
	finalRaw, err := p.Run()
	if err != nil {
		return Result{Cfg: initial}, err
	}
	final := finalRaw.(*model)
	return Result{Cfg: final.cfg, Saved: final.saved}, nil
}

// --- model ---

type phase int

const (
	phaseWelcome phase = iota
	phaseEntry
	phaseQuestion
	phaseSummary
	phaseEditPick
	phaseCancel
)

// question is one Q&A entry. The template kind controls rendering and
// keyboard handling. visible may return false when a parent toggle is
// disabled (skip-when-disabled).
type question struct {
	id       string // dot path: "cli.locale", "autoUpdate.throttleMinutes", ...
	label    string
	help     string
	template templateKind

	// boolean: defaultYes is the schema's recommended choice.
	defaultYes bool

	// integer: minimum/maximum bounds for validation.
	minInt int
	maxInt int

	// enum: ordered list of (value, description) pairs.
	enumOptions []enumOption

	// visible reports whether the question should appear in the current
	// flow given cfg's current state. Used for skip-when-disabled.
	visible func(cfg config.AtlConfig) bool

	// get/set handle the value's read/write against the typed cfg.
	get func(cfg config.AtlConfig) any
	set func(cfg *config.AtlConfig, v any)
}

type enumOption struct {
	value string
	desc  string
}

type templateKind int

const (
	tplBool templateKind = iota
	tplInt
	tplEnum
)

type model struct {
	mode      Mode
	editPath  string
	initial   config.AtlConfig
	cfg       config.AtlConfig
	questions []question

	phase  phase
	qIdx   int // which visible question is on screen (counted only across visible)
	saved  bool
	exited bool

	// Per-question input state.
	intBuffer string // numeric input
	selIdx    int    // bool yes/no or enum option index

	// Summary editPick state.
	editPickIdx int

	width  int
	height int

	// returnToSummary, when true, makes advanceQuestion jump back to
	// summary instead of next visible question — used by the editPick
	// flow so editing a single field returns to summary.
	returnToSummary bool

	// Validation feedback for the current question (cleared on submit).
	validationErr string
}

func newModel(mode Mode, initial config.AtlConfig, editPath string) *model {
	m := &model{
		mode:      mode,
		editPath:  editPath,
		initial:   initial,
		cfg:       initial,
		questions: questionList(),
	}
	switch mode {
	case ModeInit:
		m.phase = phaseWelcome
	case ModeEdit:
		m.phase = phaseEntry
	}
	return m
}

// Init implements tea.Model.
func (m *model) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

// updateKey routes key input by phase.
func (m *model) updateKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Phase-independent shortcuts.
	switch k.Type {
	case tea.KeyCtrlC:
		if m.phase == phaseWelcome || m.phase == phaseEntry {
			m.exited = true
			return m, tea.Quit
		}
		m.phase = phaseCancel
		return m, nil
	}

	switch m.phase {
	case phaseWelcome:
		return m.updateWelcome(k)
	case phaseEntry:
		return m.updateEntry(k)
	case phaseQuestion:
		return m.updateQuestion(k)
	case phaseSummary:
		return m.updateSummary(k)
	case phaseEditPick:
		return m.updateEditPick(k)
	case phaseCancel:
		return m.updateCancel(k)
	}
	return m, nil
}

func (m *model) updateWelcome(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyEnter:
		m.qIdx = 0
		m.advanceToVisibleQuestion(+1)
		m.phase = phaseQuestion
		m.resetQuestionInput()
		return m, nil
	case tea.KeyEsc:
		m.exited = true
		return m, tea.Quit
	}
	return m, nil
}

func (m *model) updateEntry(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyEnter:
		m.qIdx = 0
		m.advanceToVisibleQuestion(+1)
		m.phase = phaseQuestion
		m.resetQuestionInput()
		return m, nil
	case tea.KeyEsc:
		m.exited = true
		return m, tea.Quit
	}
	return m, nil
}

func (m *model) updateQuestion(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	q := m.questions[m.qIdx]

	switch k.Type {
	case tea.KeyEsc:
		// Back to previous visible question, or back to welcome/entry.
		prev := m.findPrevVisible(m.qIdx)
		if prev < 0 {
			switch m.mode {
			case ModeInit:
				m.phase = phaseWelcome
			case ModeEdit:
				m.phase = phaseEntry
			}
			return m, nil
		}
		m.qIdx = prev
		m.resetQuestionInput()
		m.validationErr = ""
		return m, nil
	}

	switch q.template {
	case tplBool:
		return m.updateQuestionBool(k, q)
	case tplInt:
		return m.updateQuestionInt(k, q)
	case tplEnum:
		return m.updateQuestionEnum(k, q)
	}
	return m, nil
}

func (m *model) updateQuestionBool(k tea.KeyMsg, q question) (tea.Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyUp, tea.KeyDown, tea.KeyLeft, tea.KeyRight, tea.KeyTab:
		// Toggle.
		m.selIdx = 1 - m.selIdx
		return m, nil
	case tea.KeyEnter:
		// Commit: selIdx 0 = Yes, 1 = No.
		val := m.selIdx == 0
		q.set(&m.cfg, val)
		m.advanceQuestion()
		return m, nil
	}
	// Letter shortcuts.
	switch strings.ToLower(k.String()) {
	case "y":
		q.set(&m.cfg, true)
		m.advanceQuestion()
	case "n":
		q.set(&m.cfg, false)
		m.advanceQuestion()
	}
	return m, nil
}

func (m *model) updateQuestionInt(k tea.KeyMsg, q question) (tea.Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyEnter:
		s := strings.TrimSpace(m.intBuffer)
		if s == "" {
			// Treat empty as "keep default" — we already preloaded
			// intBuffer with the current value in resetQuestionInput.
			s = strconv.Itoa(q.get(m.cfg).(int))
		}
		v, err := strconv.Atoi(s)
		if err != nil {
			m.validationErr = fmt.Sprintf("not a number: %q", s)
			return m, nil
		}
		if v < q.minInt || v > q.maxInt {
			m.validationErr = fmt.Sprintf("must be between %d and %d, got %d", q.minInt, q.maxInt, v)
			return m, nil
		}
		q.set(&m.cfg, v)
		m.advanceQuestion()
		return m, nil
	case tea.KeyBackspace:
		if len(m.intBuffer) > 0 {
			m.intBuffer = m.intBuffer[:len(m.intBuffer)-1]
		}
		return m, nil
	}
	// Append digits / minus.
	r := k.String()
	if len(r) == 1 && (r == "-" || (r >= "0" && r <= "9")) {
		m.intBuffer += r
		m.validationErr = ""
	}
	return m, nil
}

func (m *model) updateQuestionEnum(k tea.KeyMsg, q question) (tea.Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyUp, tea.KeyLeft:
		if m.selIdx > 0 {
			m.selIdx--
		}
		return m, nil
	case tea.KeyDown, tea.KeyRight, tea.KeyTab:
		if m.selIdx < len(q.enumOptions)-1 {
			m.selIdx++
		}
		return m, nil
	case tea.KeyEnter:
		q.set(&m.cfg, q.enumOptions[m.selIdx].value)
		m.advanceQuestion()
		return m, nil
	}
	return m, nil
}

// advanceQuestion moves to the next visible question, or to the summary
// when the visible queue is exhausted, or when returnToSummary is set
// (single-field edit jump).
func (m *model) advanceQuestion() {
	if m.returnToSummary {
		m.returnToSummary = false
		m.phase = phaseSummary
		m.selIdx = 0
		return
	}
	next := m.findNextVisible(m.qIdx)
	if next < 0 {
		m.phase = phaseSummary
		m.selIdx = 0
		return
	}
	m.qIdx = next
	m.resetQuestionInput()
	m.validationErr = ""
}

// advanceToVisibleQuestion ensures m.qIdx points at a visible question.
// dir: +1 to scan forward, -1 to scan backward. If none, leaves qIdx unchanged.
func (m *model) advanceToVisibleQuestion(dir int) {
	q := m.questions[m.qIdx]
	if q.visible == nil || q.visible(m.cfg) {
		return
	}
	if dir > 0 {
		next := m.findNextVisible(m.qIdx - 1)
		if next >= 0 {
			m.qIdx = next
		}
	} else {
		prev := m.findPrevVisible(m.qIdx + 1)
		if prev >= 0 {
			m.qIdx = prev
		}
	}
}

func (m *model) findNextVisible(after int) int {
	for i := after + 1; i < len(m.questions); i++ {
		if m.questions[i].visible == nil || m.questions[i].visible(m.cfg) {
			return i
		}
	}
	return -1
}

func (m *model) findPrevVisible(before int) int {
	for i := before - 1; i >= 0; i-- {
		if m.questions[i].visible == nil || m.questions[i].visible(m.cfg) {
			return i
		}
	}
	return -1
}

func (m *model) resetQuestionInput() {
	q := m.questions[m.qIdx]
	m.intBuffer = ""
	m.selIdx = 0
	m.validationErr = ""
	switch q.template {
	case tplBool:
		v := q.get(m.cfg).(bool)
		if v {
			m.selIdx = 0 // Yes
		} else {
			m.selIdx = 1 // No
		}
	case tplInt:
		m.intBuffer = strconv.Itoa(q.get(m.cfg).(int))
	case tplEnum:
		current := q.get(m.cfg).(string)
		for i, opt := range q.enumOptions {
			if opt.value == current {
				m.selIdx = i
				break
			}
		}
	}
}

func (m *model) updateSummary(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	const (
		idxSave   = 0
		idxEdit   = 1
		idxCancel = 2
	)
	switch k.Type {
	case tea.KeyUp, tea.KeyLeft:
		if m.selIdx > 0 {
			m.selIdx--
		}
	case tea.KeyDown, tea.KeyRight, tea.KeyTab:
		if m.selIdx < 2 {
			m.selIdx++
		}
	case tea.KeyEnter:
		switch m.selIdx {
		case idxSave:
			m.saved = true
			m.exited = true
			return m, tea.Quit
		case idxEdit:
			m.phase = phaseEditPick
			m.editPickIdx = 0
			return m, nil
		case idxCancel:
			m.phase = phaseCancel
			return m, nil
		}
	case tea.KeyEsc:
		// Esc on summary: go back to last visible question.
		last := m.findPrevVisible(len(m.questions))
		if last >= 0 {
			m.qIdx = last
			m.phase = phaseQuestion
			m.resetQuestionInput()
		}
	}
	return m, nil
}

func (m *model) updateEditPick(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	visible := m.visibleQuestionIndices()

	switch k.Type {
	case tea.KeyUp, tea.KeyLeft:
		if m.editPickIdx > 0 {
			m.editPickIdx--
		}
	case tea.KeyDown, tea.KeyRight, tea.KeyTab:
		if m.editPickIdx < len(visible)-1 {
			m.editPickIdx++
		}
	case tea.KeyEnter:
		if len(visible) == 0 {
			// Defensive: no fields visible, return to summary.
			m.phase = phaseSummary
			m.selIdx = 0
			return m, nil
		}
		m.qIdx = visible[m.editPickIdx]
		m.phase = phaseQuestion
		m.resetQuestionInput()
		// Mark that we'll return to summary after this single question.
		m.returnToSummary = true
	case tea.KeyEsc:
		m.phase = phaseSummary
		m.selIdx = 0
	}
	return m, nil
}

func (m *model) updateCancel(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch strings.ToLower(k.String()) {
	case "y":
		m.exited = true
		return m, tea.Quit
	case "n", "esc":
		m.phase = phaseSummary
		m.selIdx = 0
		return m, nil
	}
	switch k.Type {
	case tea.KeyEsc:
		m.phase = phaseSummary
		m.selIdx = 0
	}
	return m, nil
}

func (m *model) visibleQuestionIndices() []int {
	out := make([]int, 0, len(m.questions))
	for i, q := range m.questions {
		if q.visible == nil || q.visible(m.cfg) {
			out = append(out, i)
		}
	}
	return out
}

// returnToSummary, when true, makes advanceQuestion jump back to summary
// instead of going to the next question — used by the editPick flow so
// editing a single field returns to the summary screen.
//
// (Implemented as a model field because the model is the natural carrier
// of single-shot UI hints; cleared on each summary transition.)

// View implements tea.Model.
func (m *model) View() string {
	switch m.phase {
	case phaseWelcome:
		return m.viewWelcome()
	case phaseEntry:
		return m.viewEntry()
	case phaseQuestion:
		return m.viewQuestion()
	case phaseSummary:
		return m.viewSummary()
	case phaseEditPick:
		return m.viewEditPick()
	case phaseCancel:
		return m.viewCancel()
	}
	return ""
}

// --- views ---

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	helpStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	selStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	footerStyle = lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("245"))
)

func (m *model) viewWelcome() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Welcome to atl"))
	b.WriteString("\n\n")
	b.WriteString("This is the first-time setup for the atl CLI.\n")
	b.WriteString("We'll walk through 9 short questions to populate your\n")
	b.WriteString("global configuration at ~/.atl/config.json.\n\n")
	b.WriteString("Defaults are sensible — you can tweak any of them later\n")
	b.WriteString("with `atl config edit` or by re-running this welcome.\n\n")
	b.WriteString(footerStyle.Render("[Enter] start  ·  [Esc] cancel"))
	b.WriteString("\n")
	return b.String()
}

func (m *model) viewEntry() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Editing config"))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("Current file: %s\n\n", m.editPath))
	b.WriteString("Press Enter on each question to keep the current value, or\n")
	b.WriteString("change it as needed. Cancel leaves the file unchanged.\n\n")
	b.WriteString(footerStyle.Render("[Enter] continue  ·  [Esc] cancel"))
	b.WriteString("\n")
	return b.String()
}

func (m *model) viewQuestion() string {
	q := m.questions[m.qIdx]
	visible := m.visibleQuestionIndices()
	pos := indexOf(visible, m.qIdx) + 1

	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("[%d / %d] %s", pos, len(visible), q.label)))
	b.WriteString("\n\n")
	if q.help != "" {
		b.WriteString(helpStyle.Render(q.help))
		b.WriteString("\n\n")
	}

	switch q.template {
	case tplBool:
		yes := "  Yes"
		no := "  No"
		recommended := ""
		if q.defaultYes {
			recommended = " (recommended)"
		} else {
			recommended = ""
		}
		if m.selIdx == 0 {
			yes = selStyle.Render("> Yes" + map[bool]string{true: recommended}[q.defaultYes])
		} else {
			yes = "  Yes" + map[bool]string{true: recommended}[q.defaultYes]
		}
		if m.selIdx == 1 {
			no = selStyle.Render("> No" + map[bool]string{false: recommended}[!q.defaultYes])
		} else {
			no = "  No" + map[bool]string{false: recommended}[!q.defaultYes]
		}
		b.WriteString(yes + "\n" + no + "\n\n")
		b.WriteString(footerStyle.Render("[Y/N or arrows + Enter] choose  ·  [Esc] back  ·  [Ctrl+C] cancel"))
	case tplInt:
		b.WriteString(fmt.Sprintf("Value: %s\n", m.intBuffer))
		b.WriteString(helpStyle.Render(fmt.Sprintf("Range: %d–%d", q.minInt, q.maxInt)))
		b.WriteString("\n\n")
		if m.validationErr != "" {
			b.WriteString(errStyle.Render(m.validationErr))
			b.WriteString("\n\n")
		}
		b.WriteString(footerStyle.Render("[digits + Enter] confirm  ·  [Backspace] erase  ·  [Esc] back"))
	case tplEnum:
		for i, opt := range q.enumOptions {
			line := fmt.Sprintf("  %s — %s", opt.value, opt.desc)
			if i == m.selIdx {
				line = selStyle.Render("> " + opt.value + " — " + opt.desc)
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
		b.WriteString(footerStyle.Render("[arrows + Enter] choose  ·  [Esc] back"))
	}
	b.WriteString("\n")
	return b.String()
}

func (m *model) viewSummary() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Summary"))
	b.WriteString("\n\n")
	b.WriteString(formatSummary(m.cfg))
	b.WriteString("\n")

	options := []string{"Save", "Edit a value", "Cancel"}
	for i, opt := range options {
		line := "  " + opt
		if i == m.selIdx {
			line = selStyle.Render("> " + opt)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
	b.WriteString(footerStyle.Render("[arrows + Enter] choose  ·  [Esc] back to last question"))
	b.WriteString("\n")
	return b.String()
}

func (m *model) viewEditPick() string {
	visible := m.visibleQuestionIndices()
	var b strings.Builder
	b.WriteString(titleStyle.Render("Edit a value"))
	b.WriteString("\n\n")
	for i, qi := range visible {
		q := m.questions[qi]
		val := formatValue(q.get(m.cfg))
		line := fmt.Sprintf("  %s = %s", q.label, val)
		if i == m.editPickIdx {
			line = selStyle.Render("> " + q.label + " = " + val)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
	b.WriteString(footerStyle.Render("[arrows + Enter] choose  ·  [Esc] back to summary"))
	b.WriteString("\n")
	return b.String()
}

func (m *model) viewCancel() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Cancel?"))
	b.WriteString("\n\n")
	b.WriteString("Discard all changes and exit without saving?\n\n")
	b.WriteString(footerStyle.Render("[Y] yes, discard  ·  [N or Esc] no, keep editing"))
	b.WriteString("\n")
	return b.String()
}

// --- helpers ---

func indexOf(haystack []int, needle int) int {
	for i, v := range haystack {
		if v == needle {
			return i
		}
	}
	return -1
}

func formatValue(v any) string {
	switch x := v.(type) {
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(x)
	case string:
		return x
	}
	return fmt.Sprintf("%v", v)
}

func formatSummary(cfg config.AtlConfig) string {
	var b strings.Builder
	for _, q := range questionList() {
		if q.visible != nil && !q.visible(cfg) {
			b.WriteString(fmt.Sprintf("  %s = (skipped)\n", q.label))
			continue
		}
		b.WriteString(fmt.Sprintf("  %s = %s\n", q.label, formatValue(q.get(cfg))))
	}
	return b.String()
}
