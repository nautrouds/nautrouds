package ruleline

import (
	"fmt"
	"nautrouds/internal/core/builtins"
	"nautrouds/internal/core/builtins/virtualservices"
	"nautrouds/internal/rtree"
	"strings"

	"github.com/google/shlex"
)

// RawRule 擴充 rtree.RawNode,額外保存 Parse 當下就展開好的多筆 URLs(嵌入的 RawNode.URL 不使用),
// 供呼叫端逐一取出建立 rtree.RawNode 而不用再另外呼叫 expandField/normalizeURL。
type RawRule struct {
	rtree.RawNode
	URLs []string
}

type Tracker struct {
	current *RawRule
}

func New() *Tracker {
	return &Tracker{}
}

func IsRule(line string) bool {
	if strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t") {
		return false
	}
	return strings.TrimSpace(line) != ""
}

func (t *Tracker) Parse(line string) error {
	trimmed := strings.TrimSpace(line)

	fields, err := shlex.Split(trimmed)
	if err != nil {
		return fmt.Errorf("invalid rule syntax: %s", trimmed)
	}

	var methods, rawURL, service string

	switch len(fields) {
	case 0:
		return nil
	case 1:
		methods, rawURL, service = "*", "**", fields[0]
	case 2:
		methods, rawURL, service = "*", fields[0], fields[1]
	case 3:
		methods, rawURL, service = fields[0], fields[1], fields[2]
	default:
		return fmt.Errorf("invalid rule fields (expected 1-3, got %d): %s", len(fields), trimmed)
	}

	if ok, bad := rtree.ValidateMethods(methods); !ok {
		return fmt.Errorf("unknown HTTP method: %s", bad)
	}

	if strings.HasPrefix(service, "$") {
		valid, name := virtualservices.IsValid(service)
		if !valid {
			if name == "" {
				return fmt.Errorf("invalid virtual service syntax: %s", service)
			}
			return fmt.Errorf("unknown virtual service: %s", name)
		}
		if funcName, args, err := builtins.ParseDirective(service); err == nil {
			if factory, ok := virtualservices.Registry[funcName]; ok && factory != nil {
				if _, err := factory(args...); err != nil {
					return err
				}
			}
		}
	}

	urls, err := expandField(rawURL)
	if err != nil {
		return fmt.Errorf("invalid rule syntax: %s", err)
	}
	for i, url := range urls {
		urls[i] = normalizeURL(url)
	}

	t.current = &RawRule{
		RawNode: rtree.RawNode{
			Service: service,
			Methods: methods,
		},
		URLs: urls,
	}
	return nil
}

func (t *Tracker) Pending() bool {
	return t.current != nil
}

func (t *Tracker) Flush() *RawRule {
	rule := t.current
	t.current = nil
	return rule
}
