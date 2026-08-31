package tags

import "strings"

const (
	NoMetricsTag uint16 = 1 << iota
)

func Analyze(origin []string) uint16 {
	mask := uint16(0)

	for _, tag := range origin {
		mask |= parseTag(tag)
	}

	return mask
}

func IsValid(tag string) bool {
	return parseTag(tag) != 0
}

func parseTag(tag string) uint16 {
	switch strings.ReplaceAll(strings.TrimPrefix(tag, "@"), "!", "no-") {
	case "no-metrics":
		return NoMetricsTag
	default:
		return 0
	}
}
