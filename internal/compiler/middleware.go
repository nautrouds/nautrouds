package compiler

import (
	"fmt"
	"nautrouds/internal/core/builtins"
	"nautrouds/internal/core/builtins/builtinsmware"
	"strings"
)

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
