package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileReadProvider_UsesFixtureBackend(t *testing.T) {
	p := &FileReadProvider{
		Backend: &FixtureQueryBackend{Files: map[string]string{
			"Game/Foo.lua": "first\nsecond\nthird\n",
		}},
	}

	result, err := p.Execute(context.Background(), map[string]any{
		"file_path":  "Game/Foo.lua",
		"start_line": float64(2),
		"end_line":   float64(3),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "LINE_RANGE: 2-3") {
		t.Fatalf("expected requested line range, got:\n%s", result)
	}
	if !strings.Contains(result, "2|second") || !strings.Contains(result, "3|third") {
		t.Fatalf("expected fixture file lines, got:\n%s", result)
	}
}

func TestFileFindProvider_UsesFixtureBackend(t *testing.T) {
	p := &FileFindProvider{
		Backend: &FixtureQueryBackend{Files: map[string]string{
			"Game/Foo.lua":     "foo",
			"Game/FooTest.lua": "test",
			"Game/Bar.lua":     "bar",
		}},
	}

	result, err := p.Execute(context.Background(), map[string]any{
		"query_name": "foo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Game/Foo.lua") || !strings.Contains(result, "Game/FooTest.lua") {
		t.Fatalf("expected fixture matches, got:\n%s", result)
	}
	if strings.Contains(result, "Game/Bar.lua") {
		t.Fatalf("unexpected non-matching file, got:\n%s", result)
	}
}

func TestCodeSearchProvider_UsesFixtureBackend(t *testing.T) {
	p := &CodeSearchProvider{
		Backend: &FixtureQueryBackend{Files: map[string]string{
			"Game/Foo.lua": "local function RegisterRedPoint()\nend\n",
			"Game/Bar.lua": "local function Other()\nend\n",
		}},
	}

	result, err := p.Execute(context.Background(), map[string]any{
		"search_text":   "RegisterRedPoint",
		"file_patterns": []any{"Game/"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "File: Game/Foo.lua") || !strings.Contains(result, "1|local function RegisterRedPoint()") {
		t.Fatalf("expected fixture search match, got:\n%s", result)
	}
}

func TestFixtureBackend_SearchRequiresScopeWhenConfigured(t *testing.T) {
	p := &CodeSearchProvider{
		Backend: &FixtureQueryBackend{
			RequireScope: true,
			Files: map[string]string{
				"Game/Foo.lua": "RegisterRedPoint()",
			},
		},
	}

	result, err := p.Execute(context.Background(), map[string]any{
		"search_text": "RegisterRedPoint",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "query scope is required") {
		t.Fatalf("expected scope error, got:\n%s", result)
	}
}

func TestLocalIndexBackend_ReadFindSearch(t *testing.T) {
	dir := t.TempDir()
	writeLocalIndexFile(t, dir, "files.jsonl", strings.Join([]string{
		`{"path":"Game/Foo.lua","content":"local function RegisterRedPoint()\n  return true\nend\n"}`,
		`{"path":"Game/FooTest.lua","content":"RegisterRedPoint()\n"}`,
		`{"path":"Other/Bar.lua","content":"RegisterRedPoint()\n"}`,
	}, "\n"))
	writeLocalIndexFile(t, dir, "grep_index.jsonl", strings.Join([]string{
		`{"path":"Game/Foo.lua","line":1,"text":"local function RegisterRedPoint()"}`,
		`{"path":"Game/FooTest.lua","line":1,"text":"RegisterRedPoint()"}`,
		`{"path":"Other/Bar.lua","line":1,"text":"RegisterRedPoint()"}`,
	}, "\n"))

	backend, err := NewLocalIndexQueryBackend(dir, []string{"Game/"})
	if err != nil {
		t.Fatal(err)
	}

	lines, total, cost, err := backend.ReadLines(context.Background(), "Game/Foo.lua", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 || len(lines) != 2 || lines[0] != "local function RegisterRedPoint()" {
		t.Fatalf("unexpected read result: total=%d lines=%q", total, lines)
	}
	if cost.Backend != "local-index" || cost.P4Calls != 0 {
		t.Fatalf("unexpected cost: %+v", cost)
	}

	files, cost, err := backend.FindFiles(context.Background(), "foo", false, 10)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(files, ",") != "Game/Foo.lua,Game/FooTest.lua" {
		t.Fatalf("unexpected file matches: %q", files)
	}
	if cost.P4Calls != 0 || cost.ScannedFiles != 2 {
		t.Fatalf("unexpected find cost: %+v", cost)
	}

	result, cost, err := backend.Search(context.Background(), "RegisterRedPoint", false, false, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "File: Game/Foo.lua") || strings.Contains(result, "Other/Bar.lua") {
		t.Fatalf("unexpected search result:\n%s", result)
	}
	if cost.P4Calls != 0 || cost.ScannedFiles != 2 {
		t.Fatalf("unexpected search cost: %+v", cost)
	}
}

func TestLocalIndexBackend_RequiresScope(t *testing.T) {
	_, err := NewLocalIndexQueryBackend(t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "requires --query-scope") {
		t.Fatalf("expected scope error, got %v", err)
	}
}

func TestLocalIndexBackend_RejectsOutOfScopeRead(t *testing.T) {
	dir := t.TempDir()
	writeLocalIndexFile(t, dir, "files.jsonl", strings.Join([]string{
		`{"path":"Game/Foo.lua","content":"ok\n"}`,
		`{"path":"Other/Bar.lua","content":"no\n"}`,
	}, "\n"))

	backend, err := NewLocalIndexQueryBackend(dir, []string{"Game/"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, cost, err := backend.ReadLines(context.Background(), "Other/Bar.lua", 1, 10)
	if err == nil || !strings.Contains(err.Error(), "outside query scope") {
		t.Fatalf("expected out-of-scope error, got %v", err)
	}
	if cost.P4Calls != 0 {
		t.Fatalf("local-index must not call p4: %+v", cost)
	}
}

func TestLocalIndexBackend_SearchFileContentsWithoutGrepIndex(t *testing.T) {
	dir := t.TempDir()
	writeLocalIndexFile(t, dir, "files.jsonl", strings.Join([]string{
		`{"path":"Game/Foo.lua","content":"alpha\nRegisterRedPoint()\n"}`,
		`{"path":"Game/Bar.lua","content":"alpha\n"}`,
	}, "\n"))

	backend, err := NewLocalIndexQueryBackend(dir, []string{"Game/"})
	if err != nil {
		t.Fatal(err)
	}
	result, cost, err := backend.Search(context.Background(), "RegisterRedPoint", true, false, []string{"*.lua"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "2|RegisterRedPoint()") {
		t.Fatalf("expected content search result, got:\n%s", result)
	}
	if cost.P4Calls != 0 || cost.ScannedFiles != 2 {
		t.Fatalf("unexpected cost: %+v", cost)
	}
}

func writeLocalIndexFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
