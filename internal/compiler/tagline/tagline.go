package tagline

import (
	"fmt"
	"nautrouds/internal/compiler/tokenizer"
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

func (t *Tracker) TryParse(part *tokenizer.Part) (bool, error) {
	if part.Flag != tokenizer.Text {
		return false, nil
	}

	v := part.String()
	if !strings.HasPrefix(v, "@") {
		return false, nil
	}

	if !tags.IsValid(v) {
		return true, fmt.Errorf("unknown tag: %s", v)
	}

	t.tags = append(t.tags, v)
	return true, nil
}

func (t *Tracker) Flush() []string {
	result := t.tags
	t.tags = nil
	return result
}
