package middlewareline

import (
	"fmt"
	"nautrouds/internal/compiler/tokenizer"
	"nautrouds/internal/core/builtins"
	"nautrouds/internal/core/builtins/builtinsmware"
	"nautrouds/internal/core/mmfg"
	"strings"
)

type Tracker struct {
	middlewares []string
}

func New() *Tracker {
	return &Tracker{}
}

func (t *Tracker) TryParse(part *tokenizer.Part) error {
	if part.Flag != tokenizer.Text && part.Flag != tokenizer.Call {
		return nil
	}

	v := part.String()
	switch {
	case part.Value == "$mmfg":
		if err := mmfg.ValidateDirective(v); err != nil {
			return err
		}
	case part.Value[0] == '$':
		valid, name := builtinsmware.IsValid(v)
		if !valid {
			if name == "" {
				return fmt.Errorf("invalid builtin middleware syntax: %s", v)
			}
			return fmt.Errorf("unknown builtin middleware: %s", name)
		}
		if funcName, args, err := builtins.ParseDirective(v); err == nil {
			if factory, ok := builtinsmware.Registry[funcName]; ok {
				if _, err := factory(args...); err != nil {
					return err
				}
			}
		}
	default:
		if err := validateExternalMiddleware(v); err != nil {
			return err
		}
	}

	if err := validateMiddlewareOrder(t.middlewares, v); err != nil {
		return err
	}

	t.middlewares = append(t.middlewares, v)
	return nil

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
