package tagline

import (
	"fmt"
	"nautrouds/internal/tags"
	"strings"
)

type Tracker struct {
	tags []string
}

func New() *Tracker {
	return &Tracker{}
}

func IsTag(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "@")
}

func (t *Tracker) TryParse(line string) (bool, error) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "@") {
		return false, nil
	}

	if !tags.IsValid(trimmed) {
		return true, fmt.Errorf("unknown tag: %s", trimmed)
	}

	t.tags = append(t.tags, trimmed)
	return true, nil
}

func (t *Tracker) Flush() []string {
	result := t.tags
	t.tags = nil
	return result
}
