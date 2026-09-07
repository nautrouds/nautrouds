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
	Args
	Call
)

type Part struct {
	Value string
	Flag  Flag
}

type Tokenizer struct {
	inRaw        bool
	raw          strings.Builder
	parenDepth   int
	args         strings.Builder
	paddingParts []Part
}

func New() *Tokenizer {
	return &Tokenizer{}
}

func (t *Tokenizer) Tokenize(line string) ([]Part, error) {
	parts := []Part{}
	var buf strings.Builder
	inQuote := false
	escaped := false

	flush := func(flag Flag) {
		if buf.Len() == 0 {
			return
		}
		parts = append(parts, Part{Value: buf.String(), Flag: flag})
		buf.Reset()
	}
	flushText := func() { flush(Text) }

	flushQuoted := func() {
		value := buf.String()
		parts = append(parts, Part{Value: value[1 : len(value)-1], Flag: Quoted})
		buf.Reset()
	}

	if t.inRaw && t.raw.Len() > 0 {
		t.raw.WriteRune('\n')
	}

	for _, r := range line {
		if t.inRaw {
			t.raw.WriteRune(r)
			if r == '`' {
				raw := t.raw.String()
				parts = append(parts, Part{Value: raw[1 : len(raw)-1], Flag: Quoted})
				t.raw.Reset()
				t.inRaw = false
			}
			continue
		}

		inArgs := t.parenDepth > 0
		dst := &buf
		if inArgs {
			dst = &t.args
		}

		if inQuote && escaped {
			dst.WriteRune(r)
			escaped = false
			continue
		}

		switch {
		case r == '\\':
			if inQuote {
				escaped = true
			}
			dst.WriteRune(r)
		case r == '"':
			if inArgs {
				inQuote = !inQuote
				dst.WriteRune(r)
				continue
			}
			if inQuote {
				buf.WriteRune(r)
				inQuote = false
				flushQuoted()
				continue
			}
			flushText()
			buf.WriteRune(r)
			inQuote = true
		case r == '`':
			if inQuote || inArgs {
				dst.WriteRune(r)
				continue
			}
			flushText()
			t.raw.WriteRune(r)
			t.inRaw = true
		case r == '(':
			if inQuote {
				dst.WriteRune(r)
				continue
			}
			if inArgs {
				t.args.WriteRune(r)
			} else {
				if buf.Len() > 0 {
					t.paddingParts = append(t.paddingParts, Part{Value: buf.String(), Flag: Call})
					buf.Reset()
				}
			}
			t.parenDepth++
		case r == ')':
			if inQuote {
				dst.WriteRune(r)
				continue
			}
			if t.parenDepth == 0 {
				buf.WriteRune(r)
				continue
			}
			t.parenDepth--
			if t.parenDepth == 0 {
				if len(t.paddingParts) > 0 {
					parts = append(parts, t.paddingParts...)
					t.paddingParts = nil
				}
				parts = append(parts, Part{Value: t.args.String(), Flag: Args})
				t.args.Reset()
			} else {
				t.args.WriteRune(r)
			}
		case isBracket(r):
			if inQuote {
				dst.WriteRune(r)
				continue
			}
			if inArgs {
				return nil, fmt.Errorf("brace not allowed inside parentheses: %s", line)
			}
			flushText()
			parts = append(parts, Part{Value: string(r), Flag: Bracket})
		case r == ';':
			if inQuote || inArgs {
				dst.WriteRune(r)
				continue
			}
			flushText()
		default:
			dst.WriteRune(r)
		}
	}

	if inQuote {
		return nil, fmt.Errorf("unterminated quote: %s", line)
	}

	flushText()
	return parts, nil
}

func isBracket(r rune) bool {
	return r == '{' || r == '}'
}
