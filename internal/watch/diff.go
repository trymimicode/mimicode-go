package watch

import "strings"

// extractNewContent returns the lines that appear in newText but not in
// oldText, using an LCS-based line diff — the same principle as git diff.
// Works correctly whether the user appended text, edited mid-file, or cleared
// the whole file and started fresh.
func extractNewContent(oldText, newText string) string {
	added := diffAdded(splitLines(oldText), splitLines(newText))
	return strings.Join(added, "\n")
}

// normalizeLF converts CRLF and lone CR to LF so that comparisons and diffs
// are not confused by Windows editors (e.g. VS Code) normalizing line endings
// to CRLF when saving, while mimi appends responses with bare LF.
func normalizeLF(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = normalizeLF(s)
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

// diffAdded returns lines present in b but absent from the LCS of (a, b).
func diffAdded(a, b []string) []string {
	common := lcs(a, b)
	j := 0
	var added []string
	for _, line := range b {
		if j < len(common) && line == common[j] {
			j++
		} else {
			added = append(added, line)
		}
	}
	return added
}

// lcs returns the longest common subsequence of a and b (line-level).
func lcs(a, b []string) []string {
	m, n := len(a), len(b)
	if m == 0 || n == 0 {
		return nil
	}
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}
	result := make([]string, 0, dp[m][n])
	i, j := m, n
	for i > 0 && j > 0 {
		if a[i-1] == b[j-1] {
			result = append(result, a[i-1])
			i--
			j--
		} else if dp[i-1][j] >= dp[i][j-1] {
			i--
		} else {
			j--
		}
	}
	for l, r := 0, len(result)-1; l < r; l, r = l+1, r-1 {
		result[l], result[r] = result[r], result[l]
	}
	return result
}
