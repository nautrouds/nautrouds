package compiler

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"nautrouds/internal/compiler/commentline"
	"nautrouds/internal/compiler/middlewareline"
	"nautrouds/internal/compiler/ruleline"
	"nautrouds/internal/compiler/tagline"
	"nautrouds/internal/compiler/tokenizer"
	"nautrouds/internal/rtree"
	"strings"
)

func wrapTokenizeErr(lineCount int, lines []string, err error) error {
	var perr *tokenizer.UnterminatedError
	if errors.As(err, &perr) {
		if idx := perr.Line - 1; idx >= 0 && idx < len(lines) {
			context := strings.TrimSpace(lines[idx])
			return fmt.Errorf("line %d: %w: %s", lineCount, err, context)
		}
	}
	return fmt.Errorf("line %d: %w", lineCount, err)
}

func Parse(r io.Reader) (*rtree.RouteTree, error) {

	scanner := bufio.NewScanner(r)
	lineCount := 0
	var lines []string

	comments := commentline.New()
	rule := ruleline.New()
	tags := tagline.New()
	middlewares := middlewareline.New()
	tk := tokenizer.New()

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
		lines = append(lines, line)

		if comments.IsComment(line) {
			continue
		}

		parts, err := tk.Tokenize(lineCount, line)
		if err != nil {
			return nil, wrapTokenizeErr(lineCount, lines, err)
		}

		for _, p := range parts {

			if ruleline.IsRule(p) {

				if r := rule.Flush(); r != nil {
					parseRule(r)
				} else {
					tags.Flush()
					middlewares.Flush()
				}

				if err := rule.Parse(p); err != nil {
					return nil, fmt.Errorf("line %d: %w", lineCount, err)
				}

				continue
			}

			if !rule.Pending() {
				fmt.Printf("warning: line %d: unexpected indent without a preceding rule, skipping: %q\n", lineCount, line)
				break
			}

			if ok, err := tags.TryParse(p); ok {
				if err != nil {
					return nil, fmt.Errorf("line %d: %w", lineCount, err)
				}
				continue
			}

			if err := middlewares.TryParse(p); err != nil {
				return nil, fmt.Errorf("line %d: %w", lineCount, err)
			}

		}

	}

	if r := rule.Flush(); r != nil {
		parseRule(r)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanner error at line %d: %w", lineCount, err)
	}

	if err := tk.Close(); err != nil {
		return nil, wrapTokenizeErr(lineCount, lines, err)
	}

	return rtree.Build(rawNodes), nil
}

func ParseString(content string) (*rtree.RouteTree, error) {
	return Parse(strings.NewReader(content))
}
