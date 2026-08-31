package middlewareline

import (
	"fmt"
	"nautrouds/internal/core/builtins"
	"nautrouds/internal/core/builtins/builtinsmware"
	"nautrouds/internal/core/mmfg"
	"strings"
)

type Tracker struct {
	middlewares []string

	pending        string
	pendingDepth   int
	pendingInQuote bool
}

func New() *Tracker {
	return &Tracker{}
}

func (t *Tracker) TryParse(line string) (bool, error) {
	trimmed := strings.TrimSpace(line)

	if t.pending != "" {
		t.pending += trimmed
		scanParens(trimmed, &t.pendingInQuote, &t.pendingDepth)

		switch {
		case t.pendingDepth > 0:
			return true, nil
		case t.pendingDepth < 0:
			expr := t.pending
			t.pending = ""
			return true, fmt.Errorf("unbalanced parentheses: %s", expr)
		default:
			expr := t.pending
			t.pending = ""
			return true, t.finish(expr)
		}
	}

	if trimmed == "" || strings.HasPrefix(trimmed, "@") {
		return false, nil
	}

	depth := 0
	inQuote := false
	scanParens(trimmed, &inQuote, &depth)

	switch {
	case depth > 0:
		t.pending = trimmed
		t.pendingDepth = depth
		t.pendingInQuote = inQuote
		return true, nil
	case depth < 0:
		return true, fmt.Errorf("unbalanced parentheses: %s", trimmed)
	default:
		return true, t.finish(trimmed)
	}
}

func (t *Tracker) Pending() bool {
	return t.pending != ""
}

func (t *Tracker) finish(trimmed string) error {
	switch {
	case strings.HasPrefix(trimmed, "$mmfg"):
		if err := mmfg.ValidateDirective(trimmed); err != nil {
			return err
		}
	case strings.HasPrefix(trimmed, "$"):
		valid, name := builtinsmware.IsValid(trimmed)
		if !valid {
			if name == "" {
				return fmt.Errorf("invalid builtin middleware syntax: %s", trimmed)
			}
			return fmt.Errorf("unknown builtin middleware: %s", name)
		}
		if funcName, args, err := builtins.ParseDirective(trimmed); err == nil {
			if factory, ok := builtinsmware.Registry[funcName]; ok {
				if _, err := factory(args...); err != nil {
					return err
				}
			}
		}
	default:
		if err := validateExternalMiddleware(trimmed); err != nil {
			return err
		}
	}

	if err := validateMiddlewareOrder(t.middlewares, trimmed); err != nil {
		return err
	}

	t.middlewares = append(t.middlewares, trimmed)
	return nil
}

func scanParens(s string, inQuote *bool, depth *int) {
	for _, r := range s {
		switch r {
		case '"':
			*inQuote = !*inQuote
		case '(':
			if !*inQuote {
				*depth++
			}
		case ')':
			if !*inQuote {
				*depth--
			}
		}
	}
}

func (t *Tracker) Flush() []string {
	middlewares := t.middlewares
	t.middlewares = nil
	return middlewares
}

func validateExternalMiddleware(trimmed string) error {
	if strings.Contains(trimmed, "(") && !strings.HasSuffix(trimmed, ")") {
		return fmt.Errorf("invalid external middleware syntax (missing closing parenthesis): %s", trimmed)
	}

	if _, _, err := builtins.ParseDirective(trimmed); err != nil {
		return fmt.Errorf("invalid external middleware syntax: %s", trimmed)
	}

	return nil
}

func funcNamePrefix(expr string) string {
	name, _, _ := strings.Cut(expr, "(")
	return name
}

func validateMiddlewareOrder(existing []string, newMw string) error {
	if len(existing) == 0 {
		return nil
	}
	last := existing[len(existing)-1]
	lastFuncName := funcNamePrefix(last)
	if builtinsmware.RequiresRealBody[lastFuncName] {
		return fmt.Errorf("%s must be the last middleware in the chain, but %q follows it", lastFuncName, newMw)
	}
	return nil
}
