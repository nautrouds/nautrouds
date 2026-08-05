package loadbalance

import (
	"path/filepath"
	"strconv"
	"strings"
)

const (
	nodeWeightSeparator = "@"
	defaultNodeWeight   = 1
	maxNodeWeight       = 100
)

func ParseNodeWeight(nodePath string) (weight int32, ok bool) {
	base := filepath.Base(nodePath)
	base = strings.TrimSuffix(base, filepath.Ext(base))

	idx := strings.LastIndex(base, nodeWeightSeparator)
	if idx == -1 {
		return defaultNodeWeight, true
	}

	parsed, err := strconv.ParseInt(base[idx+len(nodeWeightSeparator):], 10, 32)
	if err != nil || parsed <= 0 || parsed > maxNodeWeight {
		return defaultNodeWeight, false
	}

	return int32(parsed), true
}
