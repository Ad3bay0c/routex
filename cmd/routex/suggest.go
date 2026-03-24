package main

// levenshtein computes the edit distance between two strings.
// It counts the minimum number of single-character insertions,
// deletions, and substitutions needed to turn s into t.
//
// We use the classic DP approach with two rows instead of a full
// matrix — O(n) space instead of O(m*n).
func levenshtein(s, t string) int {
	sr := []rune(s)
	tr := []rune(t)
	m := len(sr)
	n := len(tr)

	if m == 0 {
		return n
	}
	if n == 0 {
		return m
	}

	prev := make([]int, n+1)
	curr := make([]int, n+1)

	for j := 0; j <= n; j++ {
		prev[j] = j
	}

	for i := 1; i <= m; i++ {
		curr[0] = i
		for j := 1; j <= n; j++ {
			if sr[i-1] == tr[j-1] {
				curr[j] = prev[j-1]
			} else {
				del := prev[j] + 1   // delete from s
				ins := curr[j-1] + 1 // insert into s
				sub := prev[j-1] + 1 // substitute
				curr[j] = min3(del, ins, sub)
			}
		}
		prev, curr = curr, prev
	}

	return prev[n]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

// suggest finds the closest known command to the given input.
// Returns the best match and true if it is close enough to be useful.
// Returns "", false if the input is too far from everything known.
func suggest(input string, candidates []string) (string, bool) {
	if len(candidates) == 0 {
		return "", false
	}

	best := ""
	bestDist := maxSuggestionDistance(input) + 1 // start above threshold

	for _, c := range candidates {
		d := levenshtein(input, c)
		if d < bestDist {
			bestDist = d
			best = c
		}
	}

	if bestDist > maxSuggestionDistance(input) {
		return "", false
	}
	return best, true
}

// maxSuggestionDistance returns the maximum edit distance we consider
// "close enough" to suggest. Scales with input length so short typos
// ("rn" → "run") and long typos ("valldate" → "validate") both work.
//
//	length 1-3  → allow 1 edit   ("rn" matches "run")
//	length 4-6  → allow 2 edits  ("runn" matches "run")
//	length 7+   → allow 3 edits  ("valldate" matches "validate")
func maxSuggestionDistance(s string) int {
	switch {
	case len(s) <= 3:
		return 1
	case len(s) <= 6:
		return 2
	default:
		return 3
	}
}
