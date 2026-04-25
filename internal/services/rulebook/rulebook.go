// Package rulebook provides loading and keyword-based searching of the COC rulebook.
package rulebook

import (
	"bufio"
	"os"
	"sort"
	"strings"
	"unicode"
)

// Section represents a single top-level chapter/section of the rulebook.
type Section struct {
	Title   string
	Content string
}

// Index is a slice of rulebook sections loaded from a Markdown file.
type Index []Section

// GlobalIndex holds the loaded rulebook sections, populated at startup.
var GlobalIndex Index

// Load reads a Markdown file at the given path and splits it into sections
// at any Markdown heading level (lines starting with one or more '#').
func Load(path string) (Index, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var sections Index
	var current *Section

	scanner := bufio.NewScanner(f)
	// Increase scanner buffer for very long lines in the rulebook.
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if title, ok := parseHeading(trimmed); ok {
			// Save previous section.
			if current != nil {
				sections = append(sections, *current)
			}
			current = &Section{Title: title}
		} else if current != nil {
			current.Content += line + "\n"
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	// Flush last section.
	if current != nil {
		sections = append(sections, *current)
	}

	return sections, nil
}

func parseHeading(line string) (string, bool) {
	if line == "" || line[0] != '#' {
		return "", false
	}
	i := 0
	for i < len(line) && line[i] == '#' {
		i++
	}
	if i >= len(line) || line[i] != ' ' {
		return "", false
	}
	title := strings.TrimSpace(line[i:])
	if title == "" {
		return "", false
	}
	return title, true
}

// Search returns up to maxResults sections ranked by multi-strategy matching:
// exact phrase, keyword hits, title boosts, and Chinese fragment fuzzy overlap.
func Search(idx Index, query string, maxResults int) []Section {
	if maxResults <= 0 {
		maxResults = 3
	}
	qRaw := strings.TrimSpace(strings.ToLower(query))
	if qRaw == "" {
		return nil
	}
	qNorm := normalizeForMatch(qRaw)
	keywords := splitKeywords(qRaw)
	if len(keywords) == 0 {
		return nil
	}

	type scored struct {
		sec   Section
		score int
	}
	var hits []scored

	for _, sec := range idx {
		titleLower := strings.ToLower(sec.Title)
		contentLower := strings.ToLower(sec.Content)
		wholeLower := titleLower + "\n" + contentLower
		titleNorm := normalizeForMatch(titleLower)
		wholeNorm := normalizeForMatch(wholeLower)

		score := scoreSection(titleLower, contentLower, wholeLower, titleNorm, wholeNorm, qRaw, qNorm, keywords)
		if score > 0 {
			hits = append(hits, scored{sec, score})
		}
	}

	sort.SliceStable(hits, func(i, j int) bool {
		return hits[i].score > hits[j].score
	})

	if len(hits) > maxResults {
		hits = hits[:maxResults]
	}

	result := make([]Section, 0, len(hits))
	for _, h := range hits {
		result = append(result, h.sec)
	}
	return result
}

func scoreSection(titleLower, contentLower, wholeLower, titleNorm, wholeNorm, qRaw, qNorm string, keywords []string) int {
	score := 0

	// Exact phrase has highest weight.
	if strings.Contains(wholeLower, qRaw) {
		score += 120
	}
	if qNorm != "" && strings.Contains(wholeNorm, qNorm) {
		score += 120
	}
	if strings.Contains(titleLower, qRaw) {
		score += 180
	}
	if qNorm != "" && strings.Contains(titleNorm, qNorm) {
		score += 180
	}

	for _, kw := range keywords {
		kwNorm := normalizeForMatch(kw)
		if strings.Contains(titleLower, kw) || (kwNorm != "" && strings.Contains(titleNorm, kwNorm)) {
			score += 40
		}
		if strings.Contains(contentLower, kw) || (kwNorm != "" && strings.Contains(wholeNorm, kwNorm)) {
			score += 18
		}

		// Fuzzy fallback for long Chinese phrases: overlap on 2-rune fragments.
		if kwNorm != "" && len([]rune(kwNorm)) >= 4 {
			overlap := bigramOverlap(wholeNorm, kwNorm)
			if overlap >= 0.5 {
				score += int(overlap * 20)
			}
		}
	}

	return score
}

func normalizeForMatch(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func bigramOverlap(haystack, needle string) float64 {
	hRunes := []rune(haystack)
	nRunes := []rune(needle)
	if len(nRunes) < 2 || len(hRunes) < 2 {
		return 0
	}

	total := len(nRunes) - 1
	if total <= 0 {
		return 0
	}

	hSet := make(map[string]struct{}, len(hRunes)-1)
	for i := 0; i < len(hRunes)-1; i++ {
		hSet[string(hRunes[i:i+2])] = struct{}{}
	}

	matched := 0
	for i := 0; i < len(nRunes)-1; i++ {
		if _, ok := hSet[string(nRunes[i:i+2])]; ok {
			matched++
		}
	}

	return float64(matched) / float64(total)
}

// Format converts sections to plain text suitable for an LLM prompt, with a
// total character cap to avoid blowing up the context window.
func Format(sections []Section, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 3000
	}
	var sb strings.Builder
	for _, sec := range sections {
		block := "【" + sec.Title + "】\n" + sec.Content + "\n"
		if sb.Len()+len(block) > maxChars {
			// Truncate last block to fit.
			remaining := maxChars - sb.Len()
			if remaining > 0 {
				sb.WriteString(block[:remaining])
				sb.WriteString("…")
			}
			break
		}
		sb.WriteString(block)
	}
	return strings.TrimSpace(sb.String())
}

// splitKeywords lowercases query and splits on whitespace / punctuation,
// then expands long tokens into smaller Chinese fragments for better recall.
func splitKeywords(query string) []string {
	// Replace common Chinese punctuation with spaces, then split on whitespace.
	replacer := strings.NewReplacer(
		"，", " ", "。", " ", "、", " ", "：", " ",
		"；", " ", "（", " ", "）", " ", "【", " ", "】", " ",
		"？", " ", "！", " ", ",", " ", ".", " ", ";", " ",
		":", " ", "(", " ", ")", " ", "[", " ", "]", " ",
	)
	cleaned := replacer.Replace(strings.ToLower(query))
	rawParts := strings.Fields(cleaned)
	out := make([]string, 0, len(rawParts)+8)

	seen := map[string]struct{}{}
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		if _, ok := seen[v]; ok {
			return
		}
		out = append(out, v)
		seen[v] = struct{}{}
	}

	base := append([]string(nil), rawParts...)
	for _, p := range base {
		add(p)
		r := []rune(normalizeForMatch(p))
		if len(r) >= 4 {
			// Add 2~4 rune fragments from long phrases to improve recall for Chinese.
			for i := 0; i < len(r); i++ {
				for w := 2; w <= 4; w++ {
					if i+w <= len(r) {
						add(string(r[i : i+w]))
					}
				}
			}
		}
	}

	// Also add the whole query as one keyword to catch exact phrases.
	if len(base) > 1 {
		add(strings.ToLower(strings.TrimSpace(query)))
		qNorm := normalizeForMatch(query)
		if qNorm != "" {
			add(qNorm)
		}
	}
	return out
}
