package commentline

import "strings"

type Detector struct {
	skippingBlock bool
}

func New() *Detector {
	return &Detector{}
}

func (d *Detector) IsComment(line string) bool {
	trimmed := strings.TrimSpace(line)

	if trimmed == "" {
		d.skippingBlock = false
		return false
	}

	if d.skippingBlock {
		return true
	}

	if strings.HasPrefix(trimmed, "#*") {
		d.skippingBlock = true
		return true
	}

	return strings.HasPrefix(trimmed, "#")
}
