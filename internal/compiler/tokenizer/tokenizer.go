package tokenizer

import (
	"fmt"
	"strings"
)

type Flag int

const (
	Text Flag = iota
	Bracket
	Quoted
)

type Part struct {
	Value string
	Flag  Flag
}

type Tokenizer struct {
	inRaw bool
	raw   strings.Builder
}

func New() *Tokenizer {
	return &Tokenizer{}
}

func (t *Tokenizer) Tokenize(line string) ([]Part, error) {
	isBracket := func(r rune) bool {
		return r == '{' || r == '}'
	}

	parts := []Part{}
	var buf strings.Builder
	inQuote := false
	escaped := false

	flush := func(flag Flag) {
		if buf.Len() > 0 {
			parts = append(parts, Part{Value: buf.String(), Flag: flag})
			buf.Reset()
		}
	}

	if t.inRaw && t.raw.Len() > 0 {
		t.raw.WriteRune('\n')
	}

	for _, r := range line {
		if t.inRaw {
			t.raw.WriteRune(r)
			if r == '`' {
				parts = append(parts, Part{Value: t.raw.String(), Flag: Quoted})
				t.raw.Reset()
				t.inRaw = false
			}
			continue
		}

		if inQuote && escaped {
			buf.WriteRune(r)
			escaped = false
			continue
		}

		switch {
		case r == '\\':
			if inQuote {
				escaped = true
			}
			buf.WriteRune(r)
		case r == '"':
			if inQuote {
				buf.WriteRune(r)
				inQuote = false
				flush(Quoted)
				continue
			}
			flush(Text)
			buf.WriteRune(r)
			inQuote = true
		case r == '`':
			if inQuote {
				buf.WriteRune(r)
				continue
			}
			flush(Text)
			t.raw.WriteRune(r)
			t.inRaw = true
		case isBracket(r):
			if inQuote {
				buf.WriteRune(r)
				continue
			}
			flush(Text)
			parts = append(parts, Part{Value: string(r), Flag: Bracket})
		default:
			buf.WriteRune(r)
		}
	}

	if inQuote {
		return nil, fmt.Errorf("unterminated quote: %s", line)
	}

	flush(Text)
	return parts, nil
}
