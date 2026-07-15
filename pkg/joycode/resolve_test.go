package joycode

import "testing"

func TestMatchModel(t *testing.T) {
	known := []string{"Claude-Opus-4.8-hq", "Claude-Opus-4.7-hq", "JoyAI-Code-1.5"}

	tests := []struct {
		name        string
		requested   string
		known       []string
		wantMatched string
		wantExact   bool
	}{
		{"exact match", "Claude-Opus-4.8-hq", known, "Claude-Opus-4.8-hq", true},
		{"unique prefix case-insensitive", "claude-opus-4.8", known, "Claude-Opus-4.8-hq", false},
		{"unique prefix other version", "claude-opus-4.7", known, "Claude-Opus-4.7-hq", false},
		{"ambiguous prefix", "claude-opus-4", known, "", false},
		{"ambiguous prefix duplicate suffix", "claude-opus-4.8", []string{"claude-opus-4.8-hq", "claude-opus-4.8-lq"}, "", false},
		{"no match", "gpt-4o", known, "", false},
		{"empty request", "", known, "", false},
		{"empty request does not match all", "", []string{"a", "b"}, "", false},
		{"exact preferred over prefix", "Claude-Opus-4.7-hq", []string{"Claude-Opus-4.7-hq", "Claude-Opus-4.7-hq-preview"}, "Claude-Opus-4.7-hq", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMatched, gotExact := MatchModel(tt.requested, tt.known)
			if gotMatched != tt.wantMatched || gotExact != tt.wantExact {
				t.Errorf("MatchModel(%q) = (%q, %v), want (%q, %v)",
					tt.requested, gotMatched, gotExact, tt.wantMatched, tt.wantExact)
			}
		})
	}
}

func TestDisplayModel(t *testing.T) {
	tests := []struct {
		name      string
		requested string
		resolved  string
		exact     bool
		want      string
	}{
		{"exact", "Claude-Opus-4.8-hq", "Claude-Opus-4.8-hq", true, "Claude-Opus-4.8-hq"},
		{"prefix", "claude-opus-4.8", "Claude-Opus-4.8-hq", false, "Claude-Opus-4.8-hq(claude-opus-4.8)"},
		{"empty requested", "", "JoyAI-Code-1.5", false, "JoyAI-Code-1.5"},
		{"same name non-exact", "JoyAI-Code-1.5", "JoyAI-Code-1.5", false, "JoyAI-Code-1.5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DisplayModel(tt.requested, tt.resolved, tt.exact); got != tt.want {
				t.Errorf("DisplayModel(%q, %q, %v) = %q, want %q",
					tt.requested, tt.resolved, tt.exact, got, tt.want)
			}
		})
	}
}
