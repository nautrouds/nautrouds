package compiler

import (
	"fmt"
	"strings"
)

func isEscapable(c byte) bool {
	return c == '[' || c == ']' || c == '|' || c == '\\'
}

func isEscapePairAt(s string, i int) bool {
	return s[i] == '\\' && i+1 < len(s) && isEscapable(s[i+1])
}

func unescapeLiteral(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}

	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if isEscapePairAt(s, i) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func expandField(field string) ([]string, error) {
	start := -1
	for i := 0; i < len(field); i++ {
		if isEscapePairAt(field, i) {
			i++
			continue
		}
		if field[i] == '[' {
			start = i
			break
		}
	}

	if start == -1 {
		return []string{unescapeLiteral(field)}, nil
	}

	end := -1
	count := 0
	for i := start; i < len(field); i++ {
		if isEscapePairAt(field, i) {
			i++
			continue
		}
		if field[i] == '[' {
			count++
		} else if field[i] == ']' {
			count--
			if count == 0 {
				end = i
				break
			}
		}
	}

	if end == -1 {
		return nil, fmt.Errorf("unclosed '[' in field: %q", field)
	}

	content := field[start+1 : end]
	parts := splitByPipe(content)

	prefix := unescapeLiteral(field[:start])
	suffix := field[end+1:]
	remainingExpanded, err := expandField(suffix)
	if err != nil {
		return nil, err
	}

	var results []string
	for _, part := range parts {
		innerExpanded, err := expandField(part)
		if err != nil {
			return nil, err
		}
		for _, inner := range innerExpanded {
			for _, rem := range remainingExpanded {
				results = append(results, prefix+inner+rem)
			}
		}
	}

	return results, nil
}

func splitByPipe(s string) []string {
	var parts []string
	var current strings.Builder
	depth := 0

	for i := 0; i < len(s); i++ {
		if isEscapePairAt(s, i) {
			current.WriteByte(s[i])
			current.WriteByte(s[i+1])
			i++
			continue
		}

		char := s[i]
		switch char {
		case '[':
			depth++
		case ']':
			depth--
		}

		if char == '|' && depth == 0 {
			parts = append(parts, current.String())
			current.Reset()
		} else {
			current.WriteByte(char)
		}
	}
	parts = append(parts, current.String())
	return parts
}
