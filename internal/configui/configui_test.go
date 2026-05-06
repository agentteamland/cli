package configui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/agentteamland/cli/internal/config"
)

// keyMsg is a small constructor for synthetic key events used by
// Update — the bubbletea KeyMsg API takes a Type for special keys and
// a Runes slice for letter / digit input.
func keyMsg(t tea.KeyType, runes ...rune) tea.KeyMsg {
	if len(runes) > 0 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: runes}
	}
	return tea.KeyMsg{Type: t}
}

// drive feeds a sequence of KeyMsg into the model and returns the final
// state. Caller asserts on phase / cfg / saved.
func drive(start *model, msgs ...tea.Msg) *model {
	m := tea.Model(start)
	for _, msg := range msgs {
		m, _ = m.Update(msg)
	}
	return m.(*model)
}

func TestNewModel_Init_StartsAtWelcome(t *testing.T) {
	m := newModel(ModeInit, config.DefaultAtlConfig(), "")
	if m.phase != phaseWelcome {
		t.Errorf("init mode: phase = %d, want phaseWelcome", m.phase)
	}
}

func TestNewModel_Edit_StartsAtEntry(t *testing.T) {
	m := newModel(ModeEdit, config.DefaultAtlConfig(), "/tmp/x.json")
	if m.phase != phaseEntry {
		t.Errorf("edit mode: phase = %d, want phaseEntry", m.phase)
	}
}

func TestWelcome_EnterAdvancesToFirstQuestion(t *testing.T) {
	m := newModel(ModeInit, config.DefaultAtlConfig(), "")
	m = drive(m, keyMsg(tea.KeyEnter))
	if m.phase != phaseQuestion {
		t.Errorf("phase = %d, want phaseQuestion", m.phase)
	}
	if m.qIdx != 0 {
		t.Errorf("qIdx = %d, want 0 (first question)", m.qIdx)
	}
}

func TestWelcome_EscExits(t *testing.T) {
	m := newModel(ModeInit, config.DefaultAtlConfig(), "")
	m = drive(m, keyMsg(tea.KeyEsc))
	if !m.exited {
		t.Error("expected exited=true after Esc on welcome")
	}
	if m.saved {
		t.Error("expected saved=false after Esc on welcome")
	}
}

func TestQuestion_EnumNavigatesAndCommits(t *testing.T) {
	m := newModel(ModeInit, config.DefaultAtlConfig(), "")
	// Welcome → first question (cli.locale, an enum).
	m = drive(m, keyMsg(tea.KeyEnter))
	if m.questions[m.qIdx].id != "cli.locale" {
		t.Fatalf("expected first question = cli.locale, got %q", m.questions[m.qIdx].id)
	}

	// Default selection: en (selIdx=0). Move down → tr, then Enter.
	m = drive(m, keyMsg(tea.KeyDown), keyMsg(tea.KeyEnter))
	if m.cfg.CLI.Locale != "tr" {
		t.Errorf("locale = %q, want %q after down + enter", m.cfg.CLI.Locale, "tr")
	}
}

func TestQuestion_BoolNavigatesAndCommits(t *testing.T) {
	m := newModel(ModeInit, config.DefaultAtlConfig(), "")
	// Skip locale: enter on default (en) → next question = sessionStartEnabled.
	m = drive(m, keyMsg(tea.KeyEnter), keyMsg(tea.KeyEnter))
	if m.questions[m.qIdx].id != "autoUpdate.sessionStartEnabled" {
		t.Fatalf("expected sessionStartEnabled, got %q", m.questions[m.qIdx].id)
	}
	// Default selIdx=0 (Yes). Press 'n' → commit false → advance.
	m = drive(m, keyMsg(tea.KeyRunes, 'n'))
	if m.cfg.AutoUpdate.SessionStartEnabled {
		t.Error("sessionStartEnabled = true, want false (after pressing n)")
	}
}

func TestQuestion_IntValidatesRange(t *testing.T) {
	// Walk to throttleMinutes (4th visible question if all enabled).
	m := newModel(ModeInit, config.DefaultAtlConfig(), "")
	m = drive(m,
		keyMsg(tea.KeyEnter), // welcome
		keyMsg(tea.KeyEnter), // locale (en, default)
		keyMsg(tea.KeyEnter), // sessionStartEnabled (Yes, default)
		keyMsg(tea.KeyEnter), // promptSubmitEnabled (Yes, default)
	)
	if m.questions[m.qIdx].id != "autoUpdate.throttleMinutes" {
		t.Fatalf("expected throttleMinutes, got %q", m.questions[m.qIdx].id)
	}

	// Type 0 (out of range), Enter → validation error fires.
	m = drive(m,
		keyMsg(tea.KeyBackspace), keyMsg(tea.KeyBackspace), // erase pre-loaded "30"
		keyMsg(tea.KeyRunes, '0'),
		keyMsg(tea.KeyEnter),
	)
	if m.validationErr == "" {
		t.Error("expected validationErr after entering 0 for throttleMinutes")
	}
	if m.questions[m.qIdx].id != "autoUpdate.throttleMinutes" {
		t.Error("expected to remain on throttleMinutes after validation failure")
	}

	// Now enter a valid value.
	m = drive(m, keyMsg(tea.KeyBackspace), keyMsg(tea.KeyRunes, '6'), keyMsg(tea.KeyRunes, '0'), keyMsg(tea.KeyEnter))
	if m.cfg.AutoUpdate.ThrottleMinutes != 60 {
		t.Errorf("throttleMinutes = %d, want 60", m.cfg.AutoUpdate.ThrottleMinutes)
	}
}

func TestSkipWhenDisabled_ThrottleSkippedWhenPromptSubmitOff(t *testing.T) {
	m := newModel(ModeInit, config.DefaultAtlConfig(), "")
	m = drive(m,
		keyMsg(tea.KeyEnter),                 // welcome
		keyMsg(tea.KeyEnter),                 // locale en
		keyMsg(tea.KeyEnter),                 // sessionStartEnabled yes
		keyMsg(tea.KeyRunes, 'n'),            // promptSubmitEnabled NO
	)
	// Next visible question must be selfCheckEnabled (NOT throttleMinutes).
	if m.questions[m.qIdx].id != "autoUpdate.selfCheckEnabled" {
		t.Errorf("after disabling promptSubmit, next question = %q, want selfCheckEnabled (throttle skipped)",
			m.questions[m.qIdx].id)
	}
}

func TestEsc_BackNavigatesAndReturnsToWelcome(t *testing.T) {
	m := newModel(ModeInit, config.DefaultAtlConfig(), "")
	m = drive(m, keyMsg(tea.KeyEnter)) // welcome -> first question
	if m.phase != phaseQuestion {
		t.Fatal("setup: not on question phase")
	}
	m = drive(m, keyMsg(tea.KeyEsc)) // esc on first question -> back to welcome
	if m.phase != phaseWelcome {
		t.Errorf("phase = %d, want phaseWelcome after Esc on first question", m.phase)
	}
}

func TestSummary_Save_SetsSaved(t *testing.T) {
	m := newModel(ModeInit, config.DefaultAtlConfig(), "")
	// Walk past welcome + every question keeping defaults.
	msgs := []tea.Msg{keyMsg(tea.KeyEnter)} // welcome
	// 9 questions, but 2 may be skipped depending on toggles.
	// Default config has all toggles on, so all 9 questions show.
	for range m.questions {
		msgs = append(msgs, keyMsg(tea.KeyEnter))
	}
	m = drive(m, msgs...)

	if m.phase != phaseSummary {
		t.Fatalf("after walking all questions, phase = %d, want phaseSummary", m.phase)
	}
	// On summary: selIdx=0 (Save). Enter → save + quit.
	m = drive(m, keyMsg(tea.KeyEnter))
	if !m.saved {
		t.Error("expected saved=true after pressing Enter on Save")
	}
	if !m.exited {
		t.Error("expected exited=true after Save")
	}
}

func TestSummary_CancelOption_OpensConfirm(t *testing.T) {
	m := newModel(ModeInit, config.DefaultAtlConfig(), "")
	msgs := []tea.Msg{keyMsg(tea.KeyEnter)}
	for range m.questions {
		msgs = append(msgs, keyMsg(tea.KeyEnter))
	}
	m = drive(m, msgs...)
	if m.phase != phaseSummary {
		t.Fatal("setup: not at summary")
	}
	// Move selection to Cancel (index 2): Down, Down.
	m = drive(m, keyMsg(tea.KeyDown), keyMsg(tea.KeyDown), keyMsg(tea.KeyEnter))
	if m.phase != phaseCancel {
		t.Errorf("phase = %d, want phaseCancel after summary->Cancel", m.phase)
	}

	// Press 'n' → return to summary.
	m = drive(m, keyMsg(tea.KeyRunes, 'n'))
	if m.phase != phaseSummary {
		t.Errorf("phase = %d, want phaseSummary after cancel-confirm 'n'", m.phase)
	}

	// Re-enter cancel + 'y' → exit without save.
	m = drive(m, keyMsg(tea.KeyDown), keyMsg(tea.KeyDown), keyMsg(tea.KeyEnter), keyMsg(tea.KeyRunes, 'y'))
	if !m.exited {
		t.Error("expected exited=true after cancel-confirm 'y'")
	}
	if m.saved {
		t.Error("expected saved=false after cancel-confirm 'y'")
	}
}

func TestSummary_EditAValue_RoundTrips(t *testing.T) {
	m := newModel(ModeInit, config.DefaultAtlConfig(), "")
	msgs := []tea.Msg{keyMsg(tea.KeyEnter)}
	for range m.questions {
		msgs = append(msgs, keyMsg(tea.KeyEnter))
	}
	m = drive(m, msgs...)
	if m.phase != phaseSummary {
		t.Fatal("setup: not at summary")
	}

	// Move to "Edit a value" (index 1) → enter → editPick.
	m = drive(m, keyMsg(tea.KeyDown), keyMsg(tea.KeyEnter))
	if m.phase != phaseEditPick {
		t.Fatalf("phase = %d, want phaseEditPick", m.phase)
	}

	// First field on editPick = cli.locale. Enter → returns to question.
	m = drive(m, keyMsg(tea.KeyEnter))
	if m.phase != phaseQuestion {
		t.Fatalf("phase = %d, want phaseQuestion after editPick->Enter", m.phase)
	}
	if m.questions[m.qIdx].id != "cli.locale" {
		t.Errorf("editPick jumped to wrong question: %q", m.questions[m.qIdx].id)
	}

	// Submit → returnToSummary should fire.
	m = drive(m, keyMsg(tea.KeyEnter))
	if m.phase != phaseSummary {
		t.Errorf("phase = %d, want phaseSummary after single-field edit submit", m.phase)
	}
}

func TestCtrlC_OnQuestion_OpensCancelConfirm(t *testing.T) {
	m := newModel(ModeInit, config.DefaultAtlConfig(), "")
	m = drive(m, keyMsg(tea.KeyEnter), keyMsg(tea.KeyCtrlC))
	if m.phase != phaseCancel {
		t.Errorf("phase = %d, want phaseCancel after Ctrl+C on question", m.phase)
	}
}

func TestCtrlC_OnWelcome_ExitsImmediately(t *testing.T) {
	m := newModel(ModeInit, config.DefaultAtlConfig(), "")
	m = drive(m, keyMsg(tea.KeyCtrlC))
	if !m.exited {
		t.Error("Ctrl+C on welcome should exit immediately")
	}
}

func TestView_RendersSomethingNonEmpty(t *testing.T) {
	// Smoke test: each phase's View returns non-empty output.
	m := newModel(ModeInit, config.DefaultAtlConfig(), "/tmp/x.json")
	for _, phase := range []phase{
		phaseWelcome, phaseQuestion, phaseSummary, phaseEditPick, phaseCancel,
	} {
		m.phase = phase
		out := m.View()
		if strings.TrimSpace(out) == "" {
			t.Errorf("phase %d: View returned empty output", phase)
		}
	}

	// Entry phase (edit mode).
	m = newModel(ModeEdit, config.DefaultAtlConfig(), "/tmp/y.json")
	if strings.TrimSpace(m.View()) == "" {
		t.Error("entry phase: View returned empty output")
	}
}

func TestView_EditEntryShowsPath(t *testing.T) {
	m := newModel(ModeEdit, config.DefaultAtlConfig(), "/path/to/.atl/config.json")
	out := m.View()
	if !strings.Contains(out, "/path/to/.atl/config.json") {
		t.Errorf("entry view should mention the path, got: %q", out)
	}
}
