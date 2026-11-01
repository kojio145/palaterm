//go:build windows

package main

import "testing"

// Which browser shortcut keys the GUI window lets through (owner's choice
// 2026-11-01: F5 and Ctrl+F back, the page-copying ones stay swallowed).
func TestGUIShortcutBlocklist(t *testing.T) {
	type k struct {
		vk               uint
		ctrl, alt, shift bool
	}
	pass := map[string]k{
		"F5":             {0x74, false, false, false},
		"Ctrl+F5":        {0x74, true, false, false},
		"F3":             {0x72, false, false, false},
		"Ctrl+R":         {'R', true, false, false},
		"Ctrl+F":         {'F', true, false, false},
		"Ctrl+C":         {'C', true, false, false},
		"Ctrl+V":         {'V', true, false, false},
		"Ctrl+A":         {'A', true, false, false},
		"Ctrl+Z":         {'Z', true, false, false},
		"Ctrl+Shift+Z":   {'Z', true, false, true},
		"Ctrl+plus":      {0xBB, true, false, false},
		"Ctrl+0":         {0x30, true, false, false},
		"Ctrl+Left":      {0x25, true, false, false},
		"Ctrl+Backspace": {0x08, true, false, false},
		"Alt+Left":       {0x25, false, true, false},
		"Alt+Right":      {0x27, false, true, false},
		"Tab":            {0x09, false, false, false},
		"Shift+Tab":      {0x09, false, false, true},
		"Enter":          {0x0D, false, false, false},
		"Esc":            {0x1B, false, false, false},
		"Delete":         {0x2E, false, false, false},
		"Down":           {0x28, false, false, false},
	}
	block := map[string]k{
		"Ctrl+S":            {'S', true, false, false},
		"Ctrl+P":            {'P', true, false, false},
		"Ctrl+U":            {'U', true, false, false},
		"Ctrl+O":            {'O', true, false, false},
		"Ctrl+J":            {'J', true, false, false},
		"Ctrl+H":            {'H', true, false, false},
		"Ctrl+N":            {'N', true, false, false},
		"Ctrl+T":            {'T', true, false, false},
		"Ctrl+Shift+I":      {'I', true, false, true},
		"Ctrl+Shift+J":      {'J', true, false, true},
		"Ctrl+Shift+C":      {'C', true, false, true},
		"Ctrl+Shift+S":      {'S', true, false, true},
		"Ctrl+Shift+P":      {'P', true, false, true},
		"Ctrl+Shift+Delete": {0x2E, true, false, true},
		"F12":               {0x7B, false, false, false},
		"F11":               {0x7A, false, false, false},
		"F1":                {0x70, false, false, false},
		"F7":                {0x76, false, false, false},
		"Alt+F5":            {0x74, false, true, false},
		"Alt+Home":          {0x24, false, true, false},
		"Alt+D":             {'D', false, true, false},
	}
	for name, x := range pass {
		if guiShortcutBlocked(x.vk, x.ctrl, x.alt, x.shift) {
			t.Errorf("%s should reach the page", name)
		}
	}
	for name, x := range block {
		if !guiShortcutBlocked(x.vk, x.ctrl, x.alt, x.shift) {
			t.Errorf("%s should be swallowed", name)
		}
	}
}
