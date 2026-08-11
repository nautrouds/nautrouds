package main

import (
	"fmt"
	"regexp"
)

type socketNamer struct {
	prefix  string
	pattern *regexp.Regexp
}

func newSocketNamer(instanceID string) *socketNamer {
	infix := ""
	if instanceID != "" {
		infix = "-" + instanceID
	}
	prefix := fmt.Sprintf("nautrouds%s-", infix)
	pattern := regexp.MustCompile(fmt.Sprintf(`^%s\d+\.sock$`, regexp.QuoteMeta(prefix)))
	return &socketNamer{prefix: prefix, pattern: pattern}
}

func (n *socketNamer) Format(index int) string {
	return fmt.Sprintf("%s%d.sock", n.prefix, index)
}

func (n *socketNamer) Owns(filename string) bool {
	return n.pattern.MatchString(filename)
}
