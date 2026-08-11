// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeP4PreviewRunsFromNonGitRoot(t *testing.T) {
	runtimeRoot := t.TempDir()
	if _, err := os.Stat(filepath.Join(runtimeRoot, ".git")); !os.IsNotExist(err) {
		t.Fatalf("test runtime root unexpectedly contains .git: %v", err)
	}

	head := []byte("package sample\n")
	headSum := sha256.Sum256(head)
	headSHA := hex.EncodeToString(headSum[:])
	manifest := map[string]any{
		"schema_version": "ocr.p4-review-source/v1", "source_kind": "p4-submitted",
		"snapshot_id": hex64("1"), "snapshot_raw_sha256": hex64("2"),
		"case_manifest_raw_sha256": hex64("3"), "session_scope_key": "submitted:10",
		"p4_port": "p4:1666", "server_address": "p4:1666", "depot_prefix": "//depot/main",
		"change_cl": int64(10), "describe_sha256": hex64("4"),
		"files": []map[string]any{{
			"depot_path": "//depot/main/a.go", "path": "a.go", "action": "add", "type": "text",
			"binary": false, "head_spec": "//depot/main/a.go#1", "head_sha256": headSHA,
			"head_byte_length": len(head),
		}},
	}
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(runtimeRoot, "source.json")
	if err := os.WriteFile(manifestPath, manifestRaw, 0o600); err != nil {
		t.Fatal(err)
	}

	ledgerSHA := hex64("5")
	receiptBasis, err := json.Marshal(struct {
		Schema       string `json:"schema"`
		QueryCount   int    `json:"query_count"`
		LedgerSHA256 string `json:"ledger_sha256"`
	}{Schema: "ocr.source-receipt/v1", QueryCount: 1, LedgerSHA256: ledgerSHA})
	if err != nil {
		t.Fatal(err)
	}
	receiptSum := sha256.Sum256(receiptBasis)
	receiptSHA := hex.EncodeToString(receiptSum[:])

	socketPath := filepath.Join(runtimeRoot, "source.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Skipf("unix sockets are unavailable: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	serverCtx, cancelServer := context.WithCancel(context.Background())
	t.Cleanup(cancelServer)
	go servePreviewSource(serverCtx, listener, head, headSHA, ledgerSHA, receiptSHA)

	err = executeReview(reviewOptions{
		reviewSource: "p4-submitted", sourceManifest: manifestPath, sourceSocket: socketPath,
		repoDir: runtimeRoot, preview: true, outputFormat: "json", audience: "agent",
	})
	if err != nil {
		t.Fatalf("native P4 preview failed from a non-Git runtime root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(runtimeRoot, ".git")); !os.IsNotExist(err) {
		t.Fatalf("native P4 preview created .git: %v", err)
	}
}

func TestReviewSourceFlagsFailClosed(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "source")
	base := reviewOptions{reviewSource: "p4-submitted", sourceManifest: abs, sourceSocket: abs, repoDir: t.TempDir(), audience: "agent"}
	if err := validateReviewOptions(&base); err != nil {
		t.Fatalf("valid native P4 flags rejected: %v", err)
	}
	for name, mutate := range map[string]func(*reviewOptions){
		"git with source flag": func(opts *reviewOptions) { opts.reviewSource = "git" },
		"git ref":              func(opts *reviewOptions) { opts.from = "HEAD~1" },
		"resume":               func(opts *reviewOptions) { opts.resume = "run-id" },
		"custom tools":         func(opts *reviewOptions) { opts.toolConfigPath = abs },
	} {
		t.Run(name, func(t *testing.T) {
			opts := base
			mutate(&opts)
			if err := validateReviewOptions(&opts); err == nil {
				t.Fatalf("unsafe source flag combination was accepted: %#v", opts)
			}
		})
	}
}

func servePreviewSource(ctx context.Context, listener net.Listener, head []byte, headSHA, ledgerSHA, receiptSHA string) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			var request struct {
				Schema string `json:"schema"`
				Method string `json:"method"`
			}
			if json.NewDecoder(conn).Decode(&request) != nil || request.Schema != "ocr.source-rpc/v1" {
				return
			}
			var result any
			switch request.Method {
			case "resolve_changed_files":
				result = map[string]any{"files": []map[string]any{{
					"path": "a.go", "action": "add", "binary": false,
					"head": map[string]any{"spec": "//depot/main/a.go#1", "content_base64": base64.StdEncoding.EncodeToString(head), "sha256": headSHA, "length": len(head)},
				}}}
			case "finalize_receipt":
				result = map[string]any{"schema": "ocr.source-receipt/v1", "query_count": 1, "ledger_sha256": ledgerSHA, "receipt_sha256": receiptSHA}
			default:
				_ = json.NewEncoder(conn).Encode(map[string]any{"schema": "ocr.source-rpc/v1", "ok": false, "error": map[string]any{"code": "unknown_method", "message": "unexpected method"}})
				return
			}
			select {
			case <-ctx.Done():
				return
			default:
				_ = json.NewEncoder(conn).Encode(map[string]any{"schema": "ocr.source-rpc/v1", "ok": true, "result": result})
			}
		}()
	}
}

func hex64(digit string) string {
	value := ""
	for len(value) < 64 {
		value += digit
	}
	return value[:64]
}
