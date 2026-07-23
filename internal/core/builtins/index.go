package builtins

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strings"
)

type Factory func(args ...string) (http.HandlerFunc, error)

func CheckArgCount(args []string, min, max int) (int, error) {
	n := len(args)
	if n < min || n > max {
		if min == max {
			return n, fmt.Errorf("expected %d argument(s), got %d", min, n)
		}
		return n, fmt.Errorf("expected %d-%d argument(s), got %d", min, max, n)
	}
	return n, nil
}

func ParseArguments(input string) ([]string, error) {
	r := csv.NewReader(strings.NewReader(input))

	r.LazyQuotes = true

	r.TrimLeadingSpace = true

	fields, err := r.Read()
	if err != nil {
		return nil, err
	}
	return fields, nil
}

func ParseDirective(s string) (string, []string, error) {
	var args []string
	start := strings.Index(s, "(")
	end := strings.LastIndex(s, ")")
	if start != -1 && end != -1 && end > start {
		fields, err := ParseArguments(s[start+1 : end])
		if err != nil {
			return "", nil, err
		}
		s = s[:start]
		args = fields
	}
	return s, args, nil
}
