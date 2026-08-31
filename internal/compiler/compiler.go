package compiler

import (
	"bufio"
	"fmt"
	"io"
	"nautrouds/internal/compiler/commentline"
	"nautrouds/internal/compiler/middlewareline"
	"nautrouds/internal/compiler/ruleline"
	"nautrouds/internal/compiler/tagline"
	"nautrouds/internal/rtree"
	"strings"
)

func Parse(r io.Reader) (*rtree.RouteTree, error) {

	scanner := bufio.NewScanner(r)
	lineCount := 0

	comments := commentline.New()
	rule := ruleline.New()
	tags := tagline.New()
	middlewares := middlewareline.New()

	var rawNodes []*rtree.RawNode

	parseRule := func(r *ruleline.RawRule) {
		r.Tags = tags.Flush()
		r.Middlewares = middlewares.Flush()
		for _, url := range r.URLs {
			node := r.RawNode
			node.URL = url
			rawNodes = append(rawNodes, &node)
		}
	}

	for scanner.Scan() {

		lineCount++
		line := scanner.Text()

		if comments.IsComment(line) {
			continue
		}

		if middlewares.Pending() {

			if ruleline.IsRule(line) || tagline.IsTag(line) {
				return nil, fmt.Errorf("line %d: unterminated middleware directive: unclosed parenthesis before next rule or tag", lineCount)
			}

			if _, err := middlewares.TryParse(line); err != nil {
				return nil, fmt.Errorf("line %d: %w", lineCount, err)
			}
			continue
		}

		if ruleline.IsRule(line) {

			if r := rule.Flush(); r != nil {
				parseRule(r)
			} else {
				tags.Flush()
				middlewares.Flush()
			}

			if err := rule.Parse(line); err != nil {
				return nil, fmt.Errorf("line %d: %w", lineCount, err)
			}

			continue
		}

		if !rule.Pending() {
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				fmt.Printf("warning: line %d: unexpected indent without a preceding rule, skipping: %q\n", lineCount, trimmed)
			}
			continue
		}

		if ok, err := tags.TryParse(line); ok {
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", lineCount, err)
			}
			continue
		}

		if ok, err := middlewares.TryParse(line); ok {
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", lineCount, err)
			}
			continue
		}

	}

	if r := rule.Flush(); r != nil {
		parseRule(r)
	}

	if middlewares.Pending() {
		return nil, fmt.Errorf("line %d: unterminated middleware directive: unclosed parenthesis at end of file", lineCount)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanner error at line %d: %w", lineCount, err)
	}

	return rtree.Build(rawNodes), nil
}

func ParseString(content string) (*rtree.RouteTree, error) {
	return Parse(strings.NewReader(content))
}
