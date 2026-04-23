// Package rulebook provides loading and keyword-based searching of the COC rulebook.
package rulebook

import (
	"bufio"
	"os"
	"strings"
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
// wherever a top-level heading (lines starting with "# ") appears.
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
		if strings.HasPrefix(line, "# ") {
			// Save previous section.
			if current != nil {
				sections = append(sections, *current)
			}
			current = &Section{Title: strings.TrimPrefix(line, "# ")}
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

// Search returns up to maxResults sections whose title or content contains
// any of the space-separated keywords in query (case-insensitive).
// Results are ordered by the number of keyword matches (most matches first).
func Search(idx Index, query string, maxResults int) []Section {
	if maxResults <= 0 {
		maxResults = 3
	}
	keywords := splitKeywords(query)
	if len(keywords) == 0 {
		return nil
	}

	type scored struct {
		sec   Section
		score int
	}
	var hits []scored

	for _, sec := range idx {
		lower := strings.ToLower(sec.Title + " " + sec.Content)
		score := 0
		for _, kw := range keywords {
			if strings.Contains(lower, kw) {
				score++
			}
		}
		if score > 0 {
			hits = append(hits, scored{sec, score})
		}
	}

	// Sort by score descending (simple selection-style since lists are small).
	for i := 0; i < len(hits); i++ {
		for j := i + 1; j < len(hits); j++ {
			if hits[j].score > hits[i].score {
				hits[i], hits[j] = hits[j], hits[i]
			}
		}
	}

	if len(hits) > maxResults {
		hits = hits[:maxResults]
	}

	result := make([]Section, 0, len(hits))
	for _, h := range hits {
		result = append(result, h.sec)
	}
	return result
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

// splitKeywords lowercases the query and splits on whitespace / Chinese punctuation.
func splitKeywords(query string) []string {
	// Replace common Chinese punctuation with spaces, then split on whitespace.
	replacer := strings.NewReplacer(
		"，", " ", "。", " ", "、", " ", "：", " ",
		"；", " ", "（", " ", "）", " ", "【", " ", "】", " ",
	)
	cleaned := replacer.Replace(strings.ToLower(query))
	parts := strings.Fields(cleaned)
	// Also add the whole query as one keyword to catch exact phrases.
	if len(parts) > 1 {
		parts = append(parts, strings.ToLower(strings.TrimSpace(query)))
	}
	return parts
}
