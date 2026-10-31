package keyboards

import (
	"strings"
	"testing"
)

func TestMainMenuKeyboard(t *testing.T) {
	kb := MainMenuKeyboard()

	var allButtonTexts []string
	for _, row := range kb.Keyboard {
		for _, btn := range row {
			allButtonTexts = append(allButtonTexts, btn.Text)
		}
	}

	// 1. Must contain exactly 4 buttons
	if len(allButtonTexts) != 4 {
		t.Fatalf("expected exactly 4 buttons in MainMenuKeyboard, got %d: %v", len(allButtonTexts), allButtonTexts)
	}

	// 2. Check for presence of the 4 required buttons
	hasGenerate := false
	hasAbout := false
	hasHelp := false
	hasAbort := false
	hasCancel := false

	for _, text := range allButtonTexts {
		lower := strings.ToLower(text)
		if strings.Contains(lower, "generate") {
			hasGenerate = true
		}
		if strings.Contains(lower, "about") {
			hasAbout = true
		}
		if strings.Contains(lower, "help") {
			hasHelp = true
		}
		if strings.Contains(lower, "abort") {
			hasAbort = true
		}
		if strings.Contains(lower, "cancel") {
			hasCancel = true
		}
	}

	if !hasGenerate {
		t.Errorf("missing Generate Session button in main menu")
	}
	if !hasAbout {
		t.Errorf("missing About button in main menu")
	}
	if !hasHelp {
		t.Errorf("missing Help button in main menu")
	}
	if !hasAbort {
		t.Errorf("missing Abort button in main menu")
	}
	if hasCancel {
		t.Errorf("persistent Cancel button must NOT be present in main menu")
	}
}

func TestAbortOnlyKeyboard(t *testing.T) {
	kb := AbortOnlyKeyboard()

	if len(kb.Keyboard) != 1 || len(kb.Keyboard[0]) != 1 {
		t.Fatalf("expected 1x1 keyboard for AbortOnlyKeyboard")
	}

	text := kb.Keyboard[0][0].Text
	if !strings.Contains(strings.ToLower(text), "abort") {
		t.Errorf("expected Abort button in AbortOnlyKeyboard, got %s", text)
	}
}

func TestProviderSelectionKeyboardAbort(t *testing.T) {
	kb := ProviderSelectionKeyboard()

	hasAbort := false
	hasCancel := false
	for _, row := range kb.Keyboard {
		for _, btn := range row {
			lower := strings.ToLower(btn.Text)
			if strings.Contains(lower, "abort") {
				hasAbort = true
			}
			if strings.Contains(lower, "cancel") {
				hasCancel = true
			}
		}
	}

	if !hasAbort {
		t.Errorf("ProviderSelectionKeyboard should contain Abort button")
	}
	if hasCancel {
		t.Errorf("ProviderSelectionKeyboard should NOT contain obsolete Cancel button")
	}
}
