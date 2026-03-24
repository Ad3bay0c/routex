package main

import "testing"

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		s, t string
		want int
	}{
		// identical
		{"run", "run", 0},
		{"", "", 0},

		// one edit
		{"run", "rn", 1},  // deletion
		{"rn", "run", 1},  // insertion
		{"run", "ran", 1}, // substitution

		// two edits
		{"vadate", "validate", 2},
		{"tooldd", "tools", 2},

		// completely different
		{"abc", "xyz", 3},

		// empty string
		{"", "run", 3},
		{"run", "", 3},

		// real typo cases
		{"runn", "run", 1},
		{"vaildate", "validate", 2},
		{"initl", "init", 1},
		{"vesrion", "version", 2},
	}

	for _, tt := range tests {
		t.Run(tt.s+"→"+tt.t, func(t *testing.T) {
			got := levenshtein(tt.s, tt.t)
			if got != tt.want {
				t.Errorf("levenshtein(%q, %q) = %d, want %d", tt.s, tt.t, got, tt.want)
			}
		})
	}
}

func TestSuggest(t *testing.T) {
	commands := []string{"run", "validate", "tools", "init", "version"}

	tests := []struct {
		input   string
		wantSug string
		wantOK  bool
	}{
		// Clear typos — should suggest
		{"runn", "run", true},
		{"rn", "run", true},
		{"vaildate", "validate", true},
		{"valdate", "validate", true},
		{"toold", "tools", true},
		{"initl", "init", true},
		{"vesrion", "version", true},
		{"versoin", "version", true},

		// Too far off — should not suggest
		{"xyz", "", false},
		{"abcdefghijkl", "", false},
		{"deploy", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, ok := suggest(tt.input, commands)
			if ok != tt.wantOK {
				t.Errorf("suggest(%q) ok = %v, want %v", tt.input, ok, tt.wantOK)
				return
			}
			if ok && got != tt.wantSug {
				t.Errorf("suggest(%q) = %q, want %q", tt.input, got, tt.wantSug)
			}
		})
	}
}

func TestSuggest_EmptyCandidates(t *testing.T) {
	_, ok := suggest("run", nil)
	if ok {
		t.Error("suggest with nil candidates should return false")
	}
}

func TestMaxSuggestionDistance(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"rn", 1},       // len 2
		{"run", 1},      // len 3
		{"runn", 2},     // len 4
		{"valdat", 2},   // len 6
		{"validate", 3}, // len 8
	}

	for _, tt := range tests {
		got := maxSuggestionDistance(tt.input)
		if got != tt.want {
			t.Errorf("maxSuggestionDistance(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}
