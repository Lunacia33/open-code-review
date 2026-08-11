// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package reviewsource

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"strings"
	"testing"
)

func sourceWithRPC(t *testing.T, manifest p4Manifest, responder func(rpcRequest) any) *p4Source {
	t.Helper()
	raw := mustJSON(t, manifest)
	client := &rpcClient{socketPath: "/unused", sourceManifestSHA256: sha256Hex(raw), dial: func(context.Context, string, string) (net.Conn, error) {
		clientConn, serverConn := net.Pipe()
		go func() {
			defer serverConn.Close()
			var request rpcRequest
			if err := json.NewDecoder(serverConn).Decode(&request); err != nil {
				return
			}
			if request.Schema != rpcSchema || request.SourceManifestSHA256 != sha256Hex(raw) {
				return
			}
			value := responder(request)
			response := rpcResponse{Schema: rpcSchema, OK: true, Result: mustJSON(t, value)}
			if failure, ok := value.(rpcErrorBody); ok {
				response = rpcResponse{Schema: rpcSchema, OK: false, Error: &failure}
			}
			_ = json.NewEncoder(serverConn).Encode(response)
		}()
		return clientConn, nil
	}}
	return &p4Source{
		manifest: manifest, manifestRaw: raw, rpc: client,
		identity: SourceIdentity{Mode: "p4-submitted", SnapshotID: manifest.SnapshotID, ManifestSHA256: sha256Hex(raw)},
	}
}

func observedContent(spec string, data []byte) resolvedContent {
	return resolvedContent{
		Spec: spec, ContentBase64: base64.StdEncoding.EncodeToString(data),
		SHA256: sha256Hex(data), ByteLength: int64(len(data)),
	}
}

func TestResolveChangedFilesAddEditAndHashFailure(t *testing.T) {
	base, head, added := []byte("old\n"), []byte("new\n"), []byte("package p\n")
	baseLength, headLength := int64(len(base)), int64(len(head))
	manifest := validManifest()
	manifest.Files = []p4ManifestFile{
		{DepotPath: "//depot/main/a.go", Path: "a.go", Action: "edit", Type: "text",
			BaseSpec: "//depot/main/a.go#3", HeadSpec: "//depot/main/a.go#4",
			BaseSHA256: sha256Hex(base), HeadSHA256: sha256Hex(head), BaseByteLength: &baseLength, HeadByteLength: headLength},
		{DepotPath: "//depot/main/new.go", Path: "new.go", Action: "add", Type: "text",
			HeadSpec: "//depot/main/new.go#1", HeadSHA256: sha256Hex(added), HeadByteLength: int64(len(added))},
	}
	source := sourceWithRPC(t, manifest, func(request rpcRequest) any {
		if request.Method != "resolve_changed_files" {
			t.Fatalf("method = %q", request.Method)
		}
		params, ok := request.Params.(map[string]any)
		if !ok || len(params) != 0 {
			t.Fatalf("resolve params = %#v", request.Params)
		}
		return resolveFilesResult{Files: []resolveFileObserved{
			{Path: "new.go", Action: "add", Head: observedContent("//depot/main/new.go#1", added)},
			{Path: "a.go", Action: "edit", Base: pointer(observedContent("//depot/main/a.go#3", base)), Head: observedContent("//depot/main/a.go#4", head)},
		}}
	})
	diffs, identity, err := source.ResolveDiffs(context.Background())
	if err != nil || len(diffs) != 2 || identity.Mode != "p4-submitted" || !diffs[1].IsNew {
		t.Fatalf("ResolveDiffs diffs=%+v identity=%+v err=%v", diffs, identity, err)
	}

	bad := manifest
	bad.Files = append([]p4ManifestFile(nil), manifest.Files...)
	bad.Files[0].HeadSHA256 = strings.Repeat("f", 64)
	badSource := sourceWithRPC(t, bad, func(rpcRequest) any {
		return resolveFilesResult{Files: []resolveFileObserved{
			{Path: "a.go", Action: "edit", Base: pointer(observedContent("//depot/main/a.go#3", base)), Head: observedContent("//depot/main/a.go#4", head)},
			{Path: "new.go", Action: "add", Head: observedContent("//depot/main/new.go#1", added)},
		}}
	})
	if _, _, err := badSource.ResolveDiffs(context.Background()); err == nil || !strings.Contains(err.Error(), "differs from manifest") {
		t.Fatalf("hash mismatch was not rejected: %v", err)
	}
}

func TestViewRPCAndBoundedFileFind(t *testing.T) {
	manifest := validManifest()
	source := sourceWithRPC(t, manifest, func(request rpcRequest) any {
		switch request.Method {
		case "read_lines":
			return readLinesWireResult{Lines: []string{"x"}, TotalLines: 1, Spec: "//depot/main/a.go#4", SHA256: strings.Repeat("4", 64)}
		case "find_files":
			truncated := true
			return findWireResult{Paths: []string{"Source/a.go"}, ResolvedSpecs: []string{"//depot/main/Source/a.go#2"}, Truncated: &truncated}
		case "search_exact_file":
			params := request.Params.(map[string]any)
			if _, exists := params["use_perl_regexp"]; exists {
				t.Fatal("search sent unsupported use_perl_regexp")
			}
			return searchWireResult{Matches: []searchWireMatch{{Line: 1, Content: "x"}}, Spec: "//depot/main/a.go#4", SHA256: strings.Repeat("4", 64)}
		case "finalize_receipt":
			ledgerHash := strings.Repeat("a", 64)
			basis := struct {
				Schema       string `json:"schema"`
				QueryCount   int    `json:"query_count"`
				LedgerSHA256 string `json:"ledger_sha256"`
			}{Schema: "ocr.source-receipt/v1", QueryCount: 3, LedgerSHA256: ledgerHash}
			return receiptWireResult{Schema: basis.Schema, QueryCount: basis.QueryCount, LedgerSHA256: ledgerHash, ReceiptSHA256: sha256Hex(mustJSON(t, basis))}
		default:
			t.Fatalf("unexpected method %q", request.Method)
			return nil
		}
	})
	view := source.View()
	if _, err := view.ReadLines(context.Background(), "a.go", 1, 10); err != nil {
		t.Fatal(err)
	}
	find, err := view.Find(context.Background(), FindRequest{QueryName: "Source/a"})
	if err != nil || find.Status != "ok" || !find.Truncated || len(find.Paths) != 1 {
		t.Fatalf("find=%+v err=%v", find, err)
	}
	missing, err := view.Find(context.Background(), FindRequest{QueryName: "a"})
	if err != nil || missing.Status != "missing_context" {
		t.Fatalf("unsafe find=%+v err=%v", missing, err)
	}
	if _, err := view.Search(context.Background(), SearchRequest{SearchText: "x", FilePatterns: []string{"a.go"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := view.Search(context.Background(), SearchRequest{SearchText: "x", FilePatterns: []string{"**/*.go"}}); err == nil {
		t.Fatal("wildcard search was accepted")
	}
	if _, err := view.Search(context.Background(), SearchRequest{SearchText: "x", FilePatterns: []string{"a.go"}, UsePerlRegexp: true}); err == nil {
		t.Fatal("unsupported regexp search was accepted")
	}
	if _, err := view.FinalizeReceipt(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDynamicChangedPathRequiresExactManifestHeadIdentity(t *testing.T) {
	manifest := validManifest()
	for name, result := range map[string]any{
		"read spec": readLinesWireResult{Lines: []string{"x"}, TotalLines: 1, Spec: "//depot/main/a.go#5", SHA256: strings.Repeat("4", 64)},
		"read hash": readLinesWireResult{Lines: []string{"x"}, TotalLines: 1, Spec: "//depot/main/a.go#4", SHA256: strings.Repeat("c", 64)},
	} {
		t.Run(name, func(t *testing.T) {
			source := sourceWithRPC(t, manifest, func(request rpcRequest) any { return result })
			if _, err := source.View().ReadLines(context.Background(), "a.go", 1, 1); err == nil || !strings.Contains(err.Error(), "manifest head") {
				t.Fatalf("mismatched dynamic read was accepted: %v", err)
			}
		})
	}
	source := sourceWithRPC(t, manifest, func(request rpcRequest) any {
		return searchWireResult{Spec: "//depot/main/a.go#4", SHA256: strings.Repeat("c", 64)}
	})
	if _, err := source.View().Search(context.Background(), SearchRequest{SearchText: "x", FilePatterns: []string{"a.go"}}); err == nil || !strings.Contains(err.Error(), "manifest head") {
		t.Fatalf("mismatched dynamic search was accepted: %v", err)
	}
}

func TestFindRejectsManifestHeadSpecMismatch(t *testing.T) {
	manifest := validManifest()
	manifest.Files[0].DepotPath = "//depot/main/Source/a.go"
	manifest.Files[0].Path = "Source/a.go"
	manifest.Files[0].BaseSpec = "//depot/main/Source/a.go#3"
	manifest.Files[0].HeadSpec = "//depot/main/Source/a.go#4"
	source := sourceWithRPC(t, manifest, func(request rpcRequest) any {
		truncated := false
		return findWireResult{Paths: []string{"Source/a.go"}, ResolvedSpecs: []string{"//depot/main/Source/a.go#5"}, Truncated: &truncated}
	})
	if _, err := source.View().Find(context.Background(), FindRequest{QueryName: "Source/a"}); err == nil || !strings.Contains(err.Error(), "manifest head") {
		t.Fatalf("find manifest mismatch was accepted: %v", err)
	}
}

func pointer[T any](value T) *T { return &value }
