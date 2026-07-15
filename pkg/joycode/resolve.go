package joycode

import "strings"

// MatchModel resolves the requested model name against knownModels.
// It first looks for an exact (case-sensitive) match; if none is found it
// falls back to a case-insensitive prefix match where the requested name is a
// prefix of a candidate (e.g. "claude-opus-4.8" -> "Claude-Opus-4.8-hq").
//
// The prefix match only succeeds when exactly one candidate matches; zero or
// ambiguous (>=2) matches are treated as no match.
//
// Returns the matched candidate's real name and whether the match was exact.
// A matched value of "" means no match — callers should apply their fallback
// chain (account default -> system default -> DefaultModel).
func MatchModel(requested string, knownModels []string) (matched string, exact bool) {
	if requested == "" {
		return "", false
	}
	for _, m := range knownModels {
		if m == requested {
			return requested, true
		}
	}
	lower := strings.ToLower(requested)
	prefixMatch := ""
	count := 0
	for _, m := range knownModels {
		if strings.HasPrefix(strings.ToLower(m), lower) {
			prefixMatch = m
			count++
		}
	}
	if count == 1 {
		return prefixMatch, false
	}
	return "", false
}

// DisplayModel returns the model string to surface in logs. For an exact match
// (or when there was no prefix resolution) it is simply resolved. For a
// non-exact (prefix) match it is "resolved(requested)", e.g.
// "Claude-Opus-4.8-hq(claude-opus-4.8)".
func DisplayModel(requested, resolved string, exact bool) string {
	if exact || requested == "" || requested == resolved {
		return resolved
	}
	return resolved + "(" + requested + ")"
}
