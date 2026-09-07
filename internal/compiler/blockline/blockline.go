package blockline

import (
	"strings"
)

type Tracker struct {
	depth int
}

func New() *Tracker {
	return &Tracker{}
}

func (t *Tracker) Strip(line string) bool {
	switch strings.TrimSpace(line) {
	case "{":
		t.depth++
		return true
	case "}":
		t.depth--
		return true
	}
	return false
}

func (t *Tracker) ResetDepth() {
	t.depth = 0
}

func (t *Tracker) Depth() int {
	return t.depth
}
