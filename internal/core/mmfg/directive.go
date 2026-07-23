package mmfg

import (
	"fmt"
	"nautrouds/internal/core/builtins"
	"strings"
)

const DirectivePrefix = "$mmfg("

func ValidateDirective(expr string) error {
	if !strings.HasPrefix(expr, DirectivePrefix) || !strings.HasSuffix(expr, ")") {
		return fmt.Errorf("invalid $mmfg syntax (expected $mmfg(nodeName)): %s", expr)
	}

	_, args, err := builtins.ParseDirective(expr)
	if err != nil || len(args) != 1 || args[0] == "" {
		return fmt.Errorf("invalid $mmfg syntax (expected $mmfg(nodeName)): %s", expr)
	}

	return nil
}

func ParseNode(expr string) (string, error) {
	_, args, err := builtins.ParseDirective(expr)
	if err != nil || len(args) != 1 {
		return "", fmt.Errorf("invalid $mmfg directive: %s", expr)
	}
	return args[0], nil
}
