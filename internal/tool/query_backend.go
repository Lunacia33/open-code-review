package tool

import (
	"bufio"
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// QueryCost records the bounded cost of a tool query. Backends that cannot
// measure a field should leave it at zero.
type QueryCost struct {
	Backend      string
	P4Calls      int
	ScannedFiles int
	ElapsedMS    int64
	Truncated    bool
}

// QueryBackend is the common backend for file_read, file_find, and code_search.
// The default implementation wraps the existing git/file-system behavior.
type QueryBackend interface {
	ReadLines(ctx context.Context, path string, startLine, maxLines int) ([]string, int, QueryCost, error)
	FindFiles(ctx context.Context, queryName string, caseSensitive bool, maxCount int) ([]string, QueryCost, error)
	Search(ctx context.Context, searchText string, caseSensitive bool, usePerlRegexp bool, patterns []string, maxCount int) (string, QueryCost, error)
}

// GitQueryBackend preserves the historical OCR behavior by delegating to the
// existing FileReader and git-backed search helpers.
type GitQueryBackend struct {
	FileReader *FileReader
}

func NewGitQueryBackend(fr *FileReader) *GitQueryBackend {
	return &GitQueryBackend{FileReader: fr}
}

func (b *GitQueryBackend) ReadLines(ctx context.Context, path string, startLine, maxLines int) ([]string, int, QueryCost, error) {
	start := time.Now()
	lines, total, err := b.FileReader.ReadLines(ctx, path, startLine, maxLines)
	return lines, total, QueryCost{
		Backend:   "git",
		ElapsedMS: time.Since(start).Milliseconds(),
	}, err
}

func (b *GitQueryBackend) FindFiles(ctx context.Context, queryName string, caseSensitive bool, maxCount int) ([]string, QueryCost, error) {
	start := time.Now()
	provider := &FileFindProvider{FileReader: b.FileReader}
	files, err := provider.listGitFiles(ctx)
	if err != nil {
		return nil, QueryCost{Backend: "git", ElapsedMS: time.Since(start).Milliseconds()}, err
	}

	var matched []string
	for _, f := range files {
		base := f
		if idx := strings.LastIndex(f, "/"); idx != -1 {
			base = f[idx+1:]
		}
		match := false
		if caseSensitive {
			match = strings.Contains(base, queryName)
		} else {
			match = strings.Contains(strings.ToLower(base), strings.ToLower(queryName))
		}
		if match {
			matched = append(matched, f)
		}
		if len(matched) >= maxCount {
			break
		}
	}
	return matched, QueryCost{
		Backend:      "git",
		ScannedFiles: len(files),
		ElapsedMS:    time.Since(start).Milliseconds(),
		Truncated:    len(matched) >= maxCount,
	}, nil
}

func (b *GitQueryBackend) Search(ctx context.Context, searchText string, caseSensitive bool, usePerlRegexp bool, patterns []string, maxCount int) (string, QueryCost, error) {
	start := time.Now()
	provider := &CodeSearchProvider{FileReader: b.FileReader}
	result, err := provider.gitGrep(ctx, searchText, caseSensitive, usePerlRegexp, patterns)
	return result, QueryCost{
		Backend:   "git",
		ElapsedMS: time.Since(start).Milliseconds(),
		Truncated: strings.Contains(result, "The results have been truncated"),
	}, err
}

// FixtureQueryBackend is a deterministic in-memory backend for unit tests and
// P4-safe smoke tests. It never shells out to git or p4.
type FixtureQueryBackend struct {
	Files        map[string]string
	RequireScope bool
}

func (b *FixtureQueryBackend) ReadLines(_ context.Context, path string, startLine, maxLines int) ([]string, int, QueryCost, error) {
	content, ok := b.Files[path]
	if !ok {
		return nil, 0, QueryCost{Backend: "fixture"}, fmt.Errorf("read file %q: not found", path)
	}
	lines, total, err := scanLines(strings.NewReader(content), startLine, maxLines)
	return lines, total, QueryCost{Backend: "fixture", ScannedFiles: 1}, err
}

func (b *FixtureQueryBackend) FindFiles(_ context.Context, queryName string, caseSensitive bool, maxCount int) ([]string, QueryCost, error) {
	var paths []string
	for path := range b.Files {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	var matched []string
	for _, path := range paths {
		base := path
		if idx := strings.LastIndex(path, "/"); idx != -1 {
			base = path[idx+1:]
		}
		if matchesText(base, queryName, caseSensitive) {
			matched = append(matched, path)
		}
		if len(matched) >= maxCount {
			break
		}
	}
	return matched, QueryCost{
		Backend:      "fixture",
		ScannedFiles: len(paths),
		Truncated:    len(matched) >= maxCount,
	}, nil
}

func (b *FixtureQueryBackend) Search(_ context.Context, searchText string, caseSensitive bool, usePerlRegexp bool, patterns []string, maxCount int) (string, QueryCost, error) {
	if b.RequireScope && len(patterns) == 0 {
		return "Error: query scope is required for this backend", QueryCost{Backend: "fixture"}, nil
	}

	var re *regexp.Regexp
	if usePerlRegexp {
		pattern := searchText
		if !caseSensitive {
			pattern = "(?i)" + pattern
		}
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return fmt.Sprintf("Error: %s", err), QueryCost{Backend: "fixture"}, nil
		}
		re = compiled
	}

	var paths []string
	for path := range b.Files {
		if pathInScope(path, patterns) {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)

	type match struct {
		lineNum int
		content string
	}
	fileMatches := make(map[string][]match)
	var fileOrder []string
	totalMatches := 0

	for _, path := range paths {
		scanner := bufio.NewScanner(strings.NewReader(b.Files[path]))
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			ok := false
			if re != nil {
				ok = re.MatchString(line)
			} else {
				ok = matchesText(line, searchText, caseSensitive)
			}
			if !ok {
				continue
			}
			if len(fileMatches[path]) == 0 {
				fileOrder = append(fileOrder, path)
			}
			fileMatches[path] = append(fileMatches[path], match{lineNum: lineNum, content: line})
			totalMatches++
			if totalMatches >= maxCount {
				break
			}
		}
		if totalMatches >= maxCount {
			break
		}
	}

	if totalMatches == 0 {
		return "No matches found", QueryCost{Backend: "fixture", ScannedFiles: len(paths)}, nil
	}

	var sb strings.Builder
	if totalMatches >= maxCount {
		sb.WriteString(fmt.Sprintf("Note: The results have been truncated. Only showing first %d results.\n", maxCount))
	}
	for _, path := range fileOrder {
		matches := fileMatches[path]
		sb.WriteString(fmt.Sprintf("File: %s\nMatch lines: %d\n", path, len(matches)))
		for _, m := range matches {
			sb.WriteString(fmt.Sprintf("%d|%s\n", m.lineNum, m.content))
		}
		sb.WriteString("\n")
	}

	return sb.String(), QueryCost{
		Backend:      "fixture",
		ScannedFiles: len(paths),
		Truncated:    totalMatches >= maxCount,
	}, nil
}

func matchesText(s, query string, caseSensitive bool) bool {
	if caseSensitive {
		return strings.Contains(s, query)
	}
	return strings.Contains(strings.ToLower(s), strings.ToLower(query))
}

func pathInScope(path string, patterns []string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if strings.HasPrefix(pattern, ":(exclude)") {
			continue
		}
		if strings.HasPrefix(pattern, "*.") && strings.HasSuffix(path, strings.TrimPrefix(pattern, "*")) {
			return true
		}
		prefix := strings.TrimSuffix(pattern, "/")
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}
