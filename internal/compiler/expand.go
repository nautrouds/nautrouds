package compiler

import "strings"

func expandField(field string) []string {
	start := strings.Index(field, "[")
	if start == -1 {
		return []string{field}
	}

	end := -1
	count := 0
	for i := start; i < len(field); i++ {
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
		return []string{field}
	}

	content := field[start+1 : end]
	parts := splitByPipe(content)

	var results []string
	prefix := field[:start]
	suffix := field[end+1:]
	remainingExpanded := expandField(suffix)

	for _, part := range parts {
		innerExpanded := expandField(part)
		for _, inner := range innerExpanded {
			for _, rem := range remainingExpanded {
				results = append(results, prefix+inner+rem)
			}
		}
	}

	return results
}

func splitByPipe(s string) []string {
	var parts []string
	var current strings.Builder
	depth := 0

	for i := 0; i < len(s); i++ {
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
