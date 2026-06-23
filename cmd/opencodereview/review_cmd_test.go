package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateReviewRefsRejectsOptionLikeCommit(t *testing.T) {
	err := validateReviewRefs(t.TempDir(), reviewOptions{commit: "-O./pwn.sh"})
	if err == nil {
		t.Fatal("expected option-like --commit ref to be rejected")
	}
	if !strings.Contains(err.Error(), "--commit") || !strings.Contains(err.Error(), "must not start with '-'") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateReviewRefsRejectsOptionLikeRangeRef(t *testing.T) {
	err := validateReviewRefs(t.TempDir(), reviewOptions{to: "-O./pwn.sh"})
	if err == nil {
		t.Fatal("expected option-like --to ref to be rejected")
	}
	if !strings.Contains(err.Error(), "--to") || !strings.Contains(err.Error(), "must not start with '-'") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseReviewFlagsAcceptsLocalIndexQueryBackend(t *testing.T) {
	opts, err := parseReviewFlags([]string{
		"--query-backend", "local-index",
		"--query-index", "H:/tmp/ocr-index",
		"--query-scope", "Game/,Plugins/Foo/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if opts.queryBackend != "local-index" || opts.queryIndex == "" || opts.queryScope == "" {
		t.Fatalf("unexpected opts: %+v", opts)
	}
}

func TestParseReviewFlagsRejectsUnknownQueryBackend(t *testing.T) {
	_, err := parseReviewFlags([]string{"--query-backend", "p4-live"})
	if err == nil || !strings.Contains(err.Error(), "invalid --query-backend") {
		t.Fatalf("expected invalid backend error, got %v", err)
	}
}

func TestBuildQueryBackendLocalIndexRequiresScope(t *testing.T) {
	_, err := buildQueryBackend(reviewOptions{
		queryBackend: "local-index",
		queryIndex:   t.TempDir(),
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "requires --query-scope") {
		t.Fatalf("expected scope error, got %v", err)
	}
}

func TestBuildQueryBackendLocalIndexLoadsIndex(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "files.jsonl"), []byte(`{"path":"Game/Foo.lua","content":"ok\n"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	backend, err := buildQueryBackend(reviewOptions{
		queryBackend: "local-index",
		queryIndex:   dir,
		queryScope:   "Game/",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if backend == nil {
		t.Fatal("expected backend")
	}
}
