package memory

import (
	"sort"
	"strings"
	"unicode"
)

// SelectMemory returns a bounded slice of MEMORY.md for injection into the
// system prompt. The full file grows without limit, so loading it whole costs
// tokens on every turn and dilutes attention. Instead:
//
//   - If the file fits in budget, return it unchanged (no behavior change for
//     small projects).
//   - Otherwise split it into entries (## sections) and keep the ones most
//     relevant to query — falling back to the most recent entries when query is
//     empty or matches nothing — until the byte budget is spent.
//
// Selected entries are returned in their original (chronological) order so the
// narrative still reads top-to-bottom.
func SelectMemory(cwd, query string, budget int) string {
	md := LoadMemory(cwd)
	if md == "" {
		return ""
	}
	if budget <= 0 || len(md) <= budget {
		return md
	}

	entries := splitEntries(md)
	if len(entries) <= 1 {
		// Single blob we can't break apart; keep the most recent bytes.
		if len(md) > budget {
			return "…(memory truncated)\n" + md[len(md)-budget:]
		}
		return md
	}

	terms := lowerTokens(query)

	type scored struct{ idx, score int }
	ranked := make([]scored, len(entries))
	for i, e := range entries {
		ranked[i] = scored{idx: i, score: scoreEntry(e, terms)}
	}
	// Highest relevance first; ties broken toward the most recent entry.
	sort.SliceStable(ranked, func(a, b int) bool {
		if ranked[a].score != ranked[b].score {
			return ranked[a].score > ranked[b].score
		}
		return ranked[a].idx > ranked[b].idx
	})

	picked := make(map[int]bool, len(entries))
	total := 0
	for _, r := range ranked {
		size := len(entries[r.idx]) + 2 // +2 for the joining blank line
		if total+size > budget && len(picked) > 0 {
			continue
		}
		picked[r.idx] = true
		total += size
		if total >= budget {
			break
		}
	}

	var out []string
	truncated := false
	for i, e := range entries {
		if picked[i] {
			out = append(out, e)
		} else {
			truncated = true
		}
	}
	res := strings.Join(out, "\n\n")
	if truncated {
		res += "\n\n_(older/less-relevant memory omitted — use memory_search to recall it)_"
	}
	return res
}

// splitEntries breaks markdown into entries delimited by "## " headings. Any
// preamble before the first heading becomes its own entry.
func splitEntries(md string) []string {
	var entries []string
	var cur []string
	flush := func() {
		if len(cur) == 0 {
			return
		}
		joined := strings.TrimRight(strings.Join(cur, "\n"), "\n")
		if strings.TrimSpace(joined) != "" {
			entries = append(entries, joined)
		}
		cur = nil
	}
	for _, ln := range strings.Split(md, "\n") {
		if strings.HasPrefix(ln, "## ") {
			flush()
		}
		cur = append(cur, ln)
	}
	flush()
	return entries
}

// scoreEntry counts how many distinct query terms appear in the entry.
func scoreEntry(entry string, terms []string) int {
	if len(terms) == 0 {
		return 0
	}
	lc := strings.ToLower(entry)
	n := 0
	for _, t := range terms {
		if strings.Contains(lc, t) {
			n++
		}
	}
	return n
}

// lowerTokens lowercases query and splits it into distinct word tokens of 3+
// characters, dropping punctuation and very short/noise words.
func lowerTokens(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	seen := make(map[string]bool, len(fields))
	var out []string
	for _, f := range fields {
		if len(f) < 3 || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}
