package tool

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/open-code-review/open-code-review/internal/pathutil"
)

const (
	localIndexFilesName = "files.jsonl"
	localIndexGrepName  = "grep_index.jsonl"
)

type localIndexFileRecord struct {
	Path        string   `json:"path"`
	Content     string   `json:"content,omitempty"`
	ContentPath string   `json:"content_path,omitempty"`
	Lines       []string `json:"lines,omitempty"`
}

type localIndexLineRecord struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

type localIndexMatch struct {
	lineNum int
	content string
}

// LocalIndexQueryBackend serves OCR context tools from bounded offline index
// files. It is intended for P4 workspaces where live depot-wide search is too
// expensive; this backend never invokes p4.
type LocalIndexQueryBackend struct {
	IndexDir string
	Scopes   []string

	files      map[string]localIndexFileRecord
	fileOrder  []string
	grepLines  []localIndexLineRecord
	hasGrepIdx bool
}

func NewLocalIndexQueryBackend(indexDir string, scopes []string) (*LocalIndexQueryBackend, error) {
	if strings.TrimSpace(indexDir) == "" {
		return nil, fmt.Errorf("local-index backend requires --query-index")
	}
	scopes = normalizeQueryScopes(scopes)
	if len(scopes) == 0 {
		return nil, fmt.Errorf("local-index backend requires --query-scope to bound searches")
	}

	absIndexDir, err := filepath.Abs(indexDir)
	if err != nil {
		return nil, fmt.Errorf("resolve query index: %w", err)
	}

	b := &LocalIndexQueryBackend{
		IndexDir: absIndexDir,
		Scopes:   scopes,
		files:    make(map[string]localIndexFileRecord),
	}
	if err := b.loadFiles(); err != nil {
		return nil, err
	}
	if err := b.loadGrepIndex(); err != nil {
		return nil, err
	}
	return b, nil
}

func (b *LocalIndexQueryBackend) ReadLines(ctx context.Context, path string, startLine, maxLines int) ([]string, int, QueryCost, error) {
	start := time.Now()
	path = normalizeIndexPath(path)
	if !pathInScope(path, b.Scopes) {
		return nil, 0, b.cost(start, 0, false), fmt.Errorf("file %q is outside query scope", path)
	}
	content, err := b.readFileContent(path)
	if err != nil {
		return nil, 0, b.cost(start, 1, false), err
	}
	lines, total, err := scanLines(strings.NewReader(content), startLine, maxLines)
	return lines, total, b.cost(start, 1, false), err
}

func (b *LocalIndexQueryBackend) FindFiles(ctx context.Context, queryName string, caseSensitive bool, maxCount int) ([]string, QueryCost, error) {
	start := time.Now()
	var matched []string
	scanned := 0
	for _, path := range b.fileOrder {
		if !pathInScope(path, b.Scopes) {
			continue
		}
		scanned++
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
	return matched, b.cost(start, scanned, len(matched) >= maxCount), nil
}

func (b *LocalIndexQueryBackend) Search(ctx context.Context, searchText string, caseSensitive bool, usePerlRegexp bool, patterns []string, maxCount int) (string, QueryCost, error) {
	start := time.Now()
	patterns = normalizeQueryScopes(patterns)

	var re *regexp.Regexp
	if usePerlRegexp {
		pattern := searchText
		if !caseSensitive {
			pattern = "(?i)" + pattern
		}
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return fmt.Sprintf("Error: %s", err), b.cost(start, 0, false), nil
		}
		re = compiled
	}

	if b.hasGrepIdx {
		return b.searchLineIndex(start, searchText, caseSensitive, re, patterns, maxCount)
	}
	return b.searchFileContents(ctx, start, searchText, caseSensitive, re, patterns, maxCount)
}

func (b *LocalIndexQueryBackend) searchLineIndex(start time.Time, searchText string, caseSensitive bool, re *regexp.Regexp, patterns []string, maxCount int) (string, QueryCost, error) {
	fileMatches := make(map[string][]localIndexMatch)
	var fileOrder []string
	seen := make(map[string]bool)
	totalMatches := 0
	scannedFiles := make(map[string]bool)

	for _, line := range b.grepLines {
		path := normalizeIndexPath(line.Path)
		if !b.pathAllowed(path, patterns) {
			continue
		}
		scannedFiles[path] = true
		if !lineMatchesSearch(line.Text, searchText, caseSensitive, re) {
			continue
		}
		if !seen[path] {
			seen[path] = true
			fileOrder = append(fileOrder, path)
		}
		fileMatches[path] = append(fileMatches[path], localIndexMatch{lineNum: line.Line, content: line.Text})
		totalMatches++
		if totalMatches >= maxCount {
			break
		}
	}

	return formatSearchResult(fileOrder, fileMatches, totalMatches, maxCount), b.cost(start, len(scannedFiles), totalMatches >= maxCount), nil
}

func (b *LocalIndexQueryBackend) searchFileContents(ctx context.Context, start time.Time, searchText string, caseSensitive bool, re *regexp.Regexp, patterns []string, maxCount int) (string, QueryCost, error) {
	fileMatches := make(map[string][]localIndexMatch)
	var fileOrder []string
	totalMatches := 0
	scanned := 0

	for _, path := range b.fileOrder {
		if err := ctx.Err(); err != nil {
			return "", b.cost(start, scanned, false), err
		}
		if !b.pathAllowed(path, patterns) {
			continue
		}
		scanned++
		content, err := b.readFileContent(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(strings.NewReader(content))
		scanner.Buffer(make([]byte, 1024), 1024*1024)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			if !lineMatchesSearch(line, searchText, caseSensitive, re) {
				continue
			}
			if len(fileMatches[path]) == 0 {
				fileOrder = append(fileOrder, path)
			}
			fileMatches[path] = append(fileMatches[path], localIndexMatch{lineNum: lineNum, content: line})
			totalMatches++
			if totalMatches >= maxCount {
				break
			}
		}
		if totalMatches >= maxCount {
			break
		}
	}

	return formatSearchResult(fileOrder, fileMatches, totalMatches, maxCount), b.cost(start, scanned, totalMatches >= maxCount), nil
}

func (b *LocalIndexQueryBackend) loadFiles() error {
	path := filepath.Join(b.IndexDir, localIndexFilesName)
	return readJSONL(path, func(lineNo int, data []byte) error {
		var rec localIndexFileRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			return fmt.Errorf("%s:%d: %w", path, lineNo, err)
		}
		rec.Path = normalizeIndexPath(rec.Path)
		if rec.Path == "" {
			return fmt.Errorf("%s:%d: path is required", path, lineNo)
		}
		b.files[rec.Path] = rec
		b.fileOrder = append(b.fileOrder, rec.Path)
		return nil
	})
}

func (b *LocalIndexQueryBackend) loadGrepIndex() error {
	path := filepath.Join(b.IndexDir, localIndexGrepName)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	err := readJSONL(path, func(lineNo int, data []byte) error {
		var rec localIndexLineRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			return fmt.Errorf("%s:%d: %w", path, lineNo, err)
		}
		rec.Path = normalizeIndexPath(rec.Path)
		if rec.Path == "" || rec.Line <= 0 {
			return fmt.Errorf("%s:%d: path and positive line are required", path, lineNo)
		}
		b.grepLines = append(b.grepLines, rec)
		return nil
	})
	if err != nil {
		return err
	}
	b.hasGrepIdx = true
	return nil
}

func (b *LocalIndexQueryBackend) readFileContent(path string) (string, error) {
	rec, ok := b.files[path]
	if !ok {
		return "", fmt.Errorf("read file %q: not found in local index", path)
	}
	if rec.Content != "" {
		return rec.Content, nil
	}
	if rec.Lines != nil {
		return strings.Join(rec.Lines, "\n"), nil
	}
	if rec.ContentPath == "" {
		return "", fmt.Errorf("read file %q: local index record has no content", path)
	}

	indexRoot, err := pathutil.CanonicalPath(b.IndexDir)
	if err != nil {
		return "", fmt.Errorf("resolve query index: %w", err)
	}
	contentPath := filepath.Join(indexRoot, filepath.FromSlash(rec.ContentPath))
	if !pathutil.WithinBase(indexRoot, contentPath) {
		return "", fmt.Errorf("content_path %q is outside query index", rec.ContentPath)
	}
	data, err := os.ReadFile(contentPath)
	if err != nil {
		return "", fmt.Errorf("read content_path %q: %w", rec.ContentPath, err)
	}
	return string(data), nil
}

func (b *LocalIndexQueryBackend) pathAllowed(path string, patterns []string) bool {
	if !pathInScope(path, b.Scopes) {
		return false
	}
	if len(patterns) == 0 {
		return true
	}
	return pathInScope(path, patterns)
}

func (b *LocalIndexQueryBackend) cost(start time.Time, scannedFiles int, truncated bool) QueryCost {
	return QueryCost{
		Backend:      "local-index",
		P4Calls:      0,
		ScannedFiles: scannedFiles,
		ElapsedMS:    time.Since(start).Milliseconds(),
		Truncated:    truncated,
	}
}

func readJSONL(path string, consume func(lineNo int, data []byte) error) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open local index %q: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if err := consume(lineNo, []byte(line)); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read local index %q: %w", path, err)
	}
	return nil
}

func normalizeIndexPath(path string) string {
	path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
	path = strings.TrimPrefix(path, "./")
	return strings.TrimLeft(path, "/")
}

func normalizeQueryScopes(scopes []string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, scope := range scopes {
		for _, part := range strings.FieldsFunc(scope, func(r rune) bool { return r == ',' || r == ';' }) {
			part = normalizeIndexPath(part)
			if part == "" || seen[part] {
				continue
			}
			seen[part] = true
			out = append(out, part)
		}
	}
	sort.Strings(out)
	return out
}

func lineMatchesSearch(line, searchText string, caseSensitive bool, re *regexp.Regexp) bool {
	if re != nil {
		return re.MatchString(line)
	}
	return matchesText(line, searchText, caseSensitive)
}

func formatSearchResult(fileOrder []string, fileMatches map[string][]localIndexMatch, totalMatches int, maxCount int) string {
	if totalMatches == 0 {
		return "No matches found"
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
	return sb.String()
}
