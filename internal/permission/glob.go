package permission

// freeMatch reports whether pattern matches text, with "*" standing for
// any run of characters and "?" for exactly one. Unlike a path glob,
// neither wildcard stops at a separator: a command or a URL has no
// path structure worth respecting.
//
// The scan is linear with backtracking only on the last star, so a
// pattern full of wildcards cannot make matching blow up.
func freeMatch(pattern, text string) bool {
	var (
		p, t            int
		starP, starT    int
		haveStar        bool
		patLen, textLen = len(pattern), len(text)
	)

	for t < textLen {
		switch {
		case p < patLen && (pattern[p] == '?' || pattern[p] == text[t]):
			p++
			t++
		case p < patLen && pattern[p] == '*':
			haveStar = true
			starP, starT = p, t
			p++
		case haveStar:
			// Give the star one more character and retry.
			starT++
			p, t = starP+1, starT
		default:
			return false
		}
	}

	for p < patLen && pattern[p] == '*' {
		p++
	}
	return p == patLen
}
