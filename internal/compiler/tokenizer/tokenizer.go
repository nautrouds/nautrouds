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

type Position struct {
	Line     int
	Index    int
	Indented bool
}

func newPosition(lineNum, index int, indented bool) Position {
	return Position{Line: lineNum, Index: index, Indented: indented}
}

func (p Position) String() string {
	return fmt.Sprintf("%d:%d", p.Line, p.Index)
}

type Part struct {
	Value string
	Flag  Flag
	Args  string
	Depth int
	Position
}

func (p Part) String() string {
	switch p.Flag {
	case Call:
		return fmt.Sprintf("%s(%s)", p.Value, p.Args)
	case Args:
		return fmt.Sprintf("(%s)", p.Args)
	}

	return p.Value
}

type Tokenizer struct {
	rawPart      *Part
	raw          strings.Builder
	parenStack   []Position
	braceStack   []Position
	args         strings.Builder
	paddingParts []*Part
}

func New() *Tokenizer {
	return &Tokenizer{}
}

type UnterminatedError struct {
	Position
	Reason string
}

func (e *UnterminatedError) Error() string {
	return fmt.Sprintf("%s starting at %s", e.Reason, e.Position)
}

func unterminatedCallError(pos Position) error {
	return &UnterminatedError{Position: pos, Reason: "unterminated parentheses"}
}

func unterminatedRawError(part *Part) error {
	return &UnterminatedError{Position: part.Position, Reason: "unterminated raw string"}
}

func isValidContinuation(line string) bool {
	trimmed := strings.TrimSpace(line)
	return trimmed == "" || trimmed == ")" || strings.HasPrefix(trimmed, "\"") || strings.HasPrefix(trimmed, "`")
}

func needsAutoComma(existing string, line string) bool {
	trimmedLine := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmedLine, "\"") && !strings.HasPrefix(trimmedLine, "`") {
		return false
	}
	trimmedExisting := strings.TrimRight(existing, " \t")
	return trimmedExisting != "" && !strings.HasSuffix(trimmedExisting, ",")
}

func (t *Tokenizer) Tokenize(lineNum int, line string) ([]*Part, error) {
	if n := len(t.parenStack); n > 0 && !isValidContinuation(line) {
		return nil, unterminatedCallError(t.parenStack[n-1])
	}

	indented := hasSpacePrefix(line)

	if len(t.parenStack) > 0 {
		if needsAutoComma(t.args.String(), line) {
			t.args.WriteByte(',')
		}
		line = strings.TrimLeft(line, " \t")
	}

	parts := []*Part{}
	var buf strings.Builder
	inQuote := false
	escaped := false
	bufStart := 0

	flush := func(flag Flag) {
		if buf.Len() == 0 {
			return
		}
		value := strings.TrimSpace(buf.String())
		buf.Reset()
		if value == "" {
			return
		}
		p := &Part{
			Value:    value,
			Flag:     flag,
			Depth:    len(t.braceStack),
			Position: newPosition(lineNum, bufStart, indented),
		}
		parts = append(parts, p)
	}
	flushText := func() { flush(Text) }

	flushQuoted := func() {
		value := buf.String()
		p := &Part{
			Value:    value[1 : len(value)-1],
			Flag:     Quoted,
			Depth:    len(t.braceStack),
			Position: newPosition(lineNum, bufStart, indented),
		}
		parts = append(parts, p)
		buf.Reset()
	}

	if t.rawPart != nil && t.raw.Len() > 0 {
		t.raw.WriteRune('\n')
	}

	for i, r := range line {
		if t.rawPart != nil {
			t.raw.WriteRune(r)
			if r == '`' {
				raw := t.raw.String()
				t.rawPart.Value = raw[1 : len(raw)-1]
				parts = append(parts, t.rawPart)
				t.raw.Reset()
				t.rawPart = nil
			}
			continue
		}

		inArgs := len(t.parenStack) > 0
		dst := &buf
		if inArgs {
			dst = &t.args
		} else if strings.TrimSpace(buf.String()) == "" {
			bufStart = i
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
			t.rawPart = &Part{
				Flag:     Quoted,
				Depth:    len(t.braceStack),
				Position: newPosition(lineNum, i, indented),
			}
		case r == '(':
			if inQuote {
				dst.WriteRune(r)
				continue
			}
			if inArgs {
				t.args.WriteRune(r)
			} else {
				if v := strings.TrimSpace(buf.String()); v != "" {
					p := &Part{
						Value:    v,
						Flag:     Call,
						Depth:    len(t.braceStack),
						Position: newPosition(lineNum, bufStart, indented),
					}
					t.paddingParts = append(t.paddingParts, p)
				}
				buf.Reset()
			}
			t.parenStack = append(t.parenStack, newPosition(lineNum, i, indented))
		case r == ')':
			if inQuote {
				dst.WriteRune(r)
				continue
			}
			if len(t.parenStack) == 0 {
				return nil, fmt.Errorf("unexpected ')': %s", line)
			}
			n := len(t.parenStack) - 1
			pos := t.parenStack[n]
			t.parenStack = t.parenStack[:n]
			if len(t.parenStack) == 0 {
				argsValue := t.args.String()
				t.args.Reset()

				if n := len(t.paddingParts); n > 0 {
					t.paddingParts[n-1].Args = argsValue
					parts = append(parts, t.paddingParts...)
					t.paddingParts = nil
				} else {
					p := &Part{
						Flag:     Args,
						Args:     argsValue,
						Depth:    len(t.braceStack),
						Position: pos,
					}
					parts = append(parts, p)
				}
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
			pos := newPosition(lineNum, i, indented)
			var depth int
			if r == '{' {
				t.braceStack = append(t.braceStack, pos)
				depth = len(t.braceStack)
			} else {
				if len(t.braceStack) == 0 {
					return nil, fmt.Errorf("unexpected '}': %s", line)
				}
				depth = len(t.braceStack)
				t.braceStack = t.braceStack[:len(t.braceStack)-1]
			}
			p := &Part{
				Value:    string(r),
				Flag:     Bracket,
				Depth:    depth,
				Position: pos,
			}
			parts = append(parts, p)
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

func (t *Tokenizer) Close() error {
	if t.rawPart != nil {
		return unterminatedRawError(t.rawPart)
	}
	if n := len(t.parenStack); n > 0 {
		return unterminatedCallError(t.parenStack[n-1])
	}
	if n := len(t.braceStack); n > 0 {
		return &UnterminatedError{Position: t.braceStack[n-1], Reason: "unclosed block, missing '}'"}
	}
	return nil
}

func isBracket(r rune) bool {
	return r == '{' || r == '}'
}

func hasSpacePrefix(line string) bool {
	return strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t")
}
