package compiler

import (
	"fmt"
	"nautrouds/internal/core/builtins"
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
