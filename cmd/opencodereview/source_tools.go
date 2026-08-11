// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/alibaba/open-code-review/internal/reviewsource"
	"github.com/alibaba/open-code-review/internal/tool"
)

func buildSourceToolRegistry(collector *tool.CommentCollector, view reviewsource.SourceView) *tool.Registry {
	reg := tool.NewRegistry()
	reg.Register(tool.NewBuiltin(tool.FileRead, sourceFileRead(view)))
	reg.Register(tool.NewBuiltin(tool.FileFind, sourceFileFind(view)))
	reg.Register(tool.NewFileReadDiff(tool.DiffMap{}))
	reg.Register(tool.NewBuiltin(tool.CodeSearch, sourceCodeSearch(view)))
	reg.Register(&tool.CodeCommentProvider{Collector: collector})
	return reg
}

func sourceFileRead(view reviewsource.SourceView) func(context.Context, map[string]any) (string, error) {
	return func(ctx context.Context, args map[string]any) (string, error) {
		filePath, _ := args["file_path"].(string)
		if strings.TrimSpace(filePath) == "" {
			return "Error: file_path is required", nil
		}
		start := intArg(args["start_line"], 1)
		end := intArg(args["end_line"], 0)
		if start <= 0 {
			start = 1
		}
		maxLines := 500
		if end > 0 {
			if end < start {
				return "", fmt.Errorf("invalid line range: start_line %d is greater than end_line %d", start, end)
			}
			if requested := end - start + 1; requested < maxLines {
				maxLines = requested
			}
		}
		result, err := view.ReadLines(ctx, filePath, start, maxLines)
		if err != nil {
			return "", fmt.Errorf("source file %q not found: %w", filePath, err)
		}
		var out strings.Builder
		out.WriteString(fmt.Sprintf("File: %s (Total lines: %d)\n", filePath, result.TotalLines))
		out.WriteString(fmt.Sprintf("IS_TRUNCATED: %t\n", result.EndLine < result.TotalLines && (end == 0 || result.EndLine < end)))
		out.WriteString(fmt.Sprintf("LINE_RANGE: %d-%d\n", result.StartLine, result.EndLine))
		if result.Content != "" {
			for i, line := range strings.Split(strings.TrimSuffix(result.Content, "\n"), "\n") {
				out.WriteString(fmt.Sprintf("%d|%s\n", result.StartLine+i, line))
			}
		}
		return out.String(), nil
	}
}

func sourceFileFind(view reviewsource.SourceView) func(context.Context, map[string]any) (string, error) {
	return func(ctx context.Context, args map[string]any) (string, error) {
		query, _ := args["query_name"].(string)
		result, err := view.Find(ctx, reviewsource.FindRequest{QueryName: query, CaseSensitive: boolArg(args["case_sensitive"])})
		if err != nil {
			return "", err
		}
		if result.Status == "missing_context" {
			return "missing_context: " + result.Reason, nil
		}
		return fmt.Sprintf("TRUNCATED: %t\n%s", result.Truncated, strings.Join(result.Paths, "\n")), nil
	}
}

func sourceCodeSearch(view reviewsource.SourceView) func(context.Context, map[string]any) (string, error) {
	return func(ctx context.Context, args map[string]any) (string, error) {
		searchText, _ := args["search_text"].(string)
		request := reviewsource.SearchRequest{
			SearchText:    searchText,
			FilePatterns:  stringSliceArg(args["file_patterns"]),
			CaseSensitive: boolArg(args["case_sensitive"]),
			UsePerlRegexp: boolArg(args["use_perl_regexp"]),
		}
		result, err := view.Search(ctx, request)
		if err != nil {
			return "", fmt.Errorf("source code_search failed: %w", err)
		}
		if result.Status == "missing_context" {
			return "missing_context: " + result.Reason, nil
		}
		var lines []string
		for _, match := range result.Matches {
			lines = append(lines, fmt.Sprintf("%s:%d:%s", match.Path, match.Line, match.Text))
		}
		return strings.Join(lines, "\n"), nil
	}
}

func intArg(value any, fallback int) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return fallback
	}
}

func boolArg(value any) bool {
	v, _ := value.(bool)
	return v
}

func stringSliceArg(value any) []string {
	switch values := value.(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		result := make([]string, 0, len(values))
		for _, item := range values {
			if text, ok := item.(string); ok && text != "" {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}
