package compiler

import "strings"

func normalizeURL(url string) string {
	if strings.HasPrefix(url, "/") {
		url = "*" + url
	}
	if !strings.Contains(url, "/") && strings.Trim(url, "*") != "" {
		url += "/**"
	}
	if strings.HasSuffix(url, "*") && !strings.HasSuffix(url, "**") {
		url += "*"
	}
	return url
}
