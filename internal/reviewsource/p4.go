// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package reviewsource

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/alibaba/open-code-review/internal/model"
)

type p4Source struct {
	manifest    p4Manifest
	manifestRaw []byte
	rpc         *rpcClient
	identity    SourceIdentity

	mu       sync.Mutex
	resolved bool
	diffs    []model.Diff
	err      error
}

// OpenP4 creates a strict manifest-bound submitted P4 review source.
func OpenP4(manifestPath, socketPath string) (ReviewSource, error) {
	manifest, raw, err := decodeP4Manifest(manifestPath, socketPath)
	if err != nil {
		return nil, err
	}
	repositoryBasis := strings.Join([]string{manifest.P4Port, manifest.ServerAddress, manifest.DepotPrefix}, "\x00")
	manifestHash := sha256Hex(raw)
	source := &p4Source{
		manifest:    manifest,
		manifestRaw: raw,
		rpc:         newRPCClient(socketPath, manifestHash),
		identity: SourceIdentity{
			Mode: "p4-submitted", SnapshotID: manifest.SnapshotID, ManifestSHA256: manifestHash,
			SnapshotRawSHA256: manifest.SnapshotRawSHA256, CaseManifestRawSHA256: manifest.CaseManifestRawSHA256,
			SessionScopeKey: manifest.SessionScopeKey, SubmittedCL: manifest.ChangeCL,
			RuleArtifactSHA256:       manifest.RuleArtifactSHA256,
			RepositoryIdentitySHA256: sha256Hex([]byte(repositoryBasis)),
		},
	}
	return source, nil
}

func (s *p4Source) SourceIdentity() SourceIdentity { return s.identity }

func (s *p4Source) View() SourceView { return &p4View{source: s} }

type resolveFilesResult struct {
	Files []resolveFileObserved `json:"files"`
}

type resolveFileObserved struct {
	Path   string           `json:"path"`
	Action string           `json:"action"`
	Binary bool             `json:"binary"`
	Base   *resolvedContent `json:"base,omitempty"`
	Head   resolvedContent  `json:"head"`
}

type resolvedContent struct {
	Spec          string `json:"spec"`
	ContentBase64 string `json:"content_base64"`
	SHA256        string `json:"sha256"`
	ByteLength    int64  `json:"length"`
}

func (s *p4Source) ResolveDiffs(ctx context.Context) ([]model.Diff, SourceIdentity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resolved {
		return cloneDiffs(s.diffs), s.identity, s.err
	}
	s.resolved = true

	var result resolveFilesResult
	if err := s.rpc.call(ctx, "resolve_changed_files", struct{}{}, &result); err != nil {
		s.err = err
		return nil, s.identity, err
	}
	if len(result.Files) != len(s.manifest.Files) {
		s.err = errors.New("resolved P4 files do not match manifest count")
		return nil, s.identity, s.err
	}
	observed := make(map[string]resolveFileObserved, len(result.Files))
	for _, file := range result.Files {
		if _, exists := observed[file.Path]; exists {
			s.err = fmt.Errorf("resolved P4 files contain duplicate path %q", file.Path)
			return nil, s.identity, s.err
		}
		observed[file.Path] = file
	}
	for _, expected := range s.manifest.Files {
		actual, ok := observed[expected.Path]
		if !ok {
			s.err = fmt.Errorf("resolved P4 file is missing %q", expected.Path)
			return nil, s.identity, s.err
		}
		diff, err := validateAndBuildDiff(expected, actual)
		if err != nil {
			s.err = fmt.Errorf("resolve P4 file %q: %w", expected.Path, err)
			return nil, s.identity, s.err
		}
		s.diffs = append(s.diffs, diff)
	}
	return cloneDiffs(s.diffs), s.identity, nil
}

func validateAndBuildDiff(expected p4ManifestFile, actual resolveFileObserved) (model.Diff, error) {
	if actual.Path != expected.Path || actual.Action != expected.Action || actual.Binary != expected.Binary {
		return model.Diff{}, errors.New("resolved action or binary policy differs from manifest")
	}
	head, err := validateContent(actual.Head, expected.HeadSpec, expected.HeadSHA256, expected.HeadByteLength)
	if err != nil {
		return model.Diff{}, fmt.Errorf("head content: %w", err)
	}
	var base []byte
	if expected.Action == "edit" {
		if actual.Base == nil || expected.BaseByteLength == nil {
			return model.Diff{}, errors.New("edit is missing base content")
		}
		base, err = validateContent(*actual.Base, expected.BaseSpec, expected.BaseSHA256, *expected.BaseByteLength)
		if err != nil {
			return model.Diff{}, fmt.Errorf("base content: %w", err)
		}
	} else if actual.Base != nil {
		return model.Diff{}, errors.New("add unexpectedly resolved base content")
	}
	return buildUnifiedDiff(expected.Path, expected.Action, expected.Binary, base, head), nil
}

func validateContent(actual resolvedContent, expectedSpec, expectedHash string, expectedLength int64) ([]byte, error) {
	if actual.Spec != expectedSpec || actual.SHA256 != expectedHash || actual.ByteLength != expectedLength {
		return nil, errors.New("spec, hash, or byte length differs from manifest")
	}
	if !lowerSHA256.MatchString(actual.SHA256) || actual.ByteLength < 0 {
		return nil, errors.New("source service returned invalid content metadata")
	}
	content, err := base64.StdEncoding.Strict().DecodeString(actual.ContentBase64)
	if err != nil {
		return nil, fmt.Errorf("decode base64: %w", err)
	}
	if int64(len(content)) != actual.ByteLength || sha256Hex(content) != actual.SHA256 {
		return nil, errors.New("decoded bytes do not match reported length/hash")
	}
	return content, nil
}

func cloneDiffs(in []model.Diff) []model.Diff { return append([]model.Diff(nil), in...) }

type p4View struct{ source *p4Source }

type readLinesWireResult struct {
	Lines      []string `json:"lines"`
	TotalLines int      `json:"total_lines"`
	Spec       string   `json:"spec"`
	SHA256     string   `json:"sha256"`
}

type searchWireMatch struct {
	Line    int    `json:"line"`
	Content string `json:"content"`
}

type searchWireResult struct {
	Matches []searchWireMatch `json:"matches"`
	Spec    string            `json:"spec"`
	SHA256  string            `json:"sha256"`
}

type findWireResult struct {
	Paths         []string `json:"paths"`
	ResolvedSpecs []string `json:"resolved_specs"`
	Truncated     *bool    `json:"truncated"`
}

type receiptWireResult struct {
	Schema        string `json:"schema"`
	QueryCount    int    `json:"query_count"`
	LedgerSHA256  string `json:"ledger_sha256"`
	ReceiptSHA256 string `json:"receipt_sha256"`
}

func (v *p4View) ReadLines(ctx context.Context, path string, startLine, maxLines int) (ReadLinesResult, error) {
	if startLine <= 0 || maxLines <= 0 || maxLines > 500 {
		return ReadLinesResult{}, errors.New("invalid P4 read line window")
	}
	if pathIsUnsafe(path) {
		return ReadLinesResult{}, errors.New("P4 read requires a canonical relative path")
	}
	var wire readLinesWireResult
	err := v.source.rpc.call(ctx, "read_lines", map[string]any{
		"path": path, "start_line": startLine, "max_lines": maxLines,
	}, &wire)
	if err != nil {
		return ReadLinesResult{}, err
	}
	endLine := startLine + len(wire.Lines) - 1
	if wire.TotalLines < 0 || len(wire.Lines) > maxLines || (len(wire.Lines) > 0 && endLine > wire.TotalLines) || !lowerSHA256.MatchString(wire.SHA256) {
		return ReadLinesResult{}, errors.New("P4 read response is inconsistent")
	}
	if err := v.validateDynamicContentIdentity(path, wire.Spec, wire.SHA256); err != nil {
		return ReadLinesResult{}, err
	}
	return ReadLinesResult{Path: path, StartLine: startLine, EndLine: endLine, TotalLines: wire.TotalLines,
		Content: strings.Join(wire.Lines, "\n"), ResolvedSpecs: []string{wire.Spec}}, nil
}

func (v *p4View) Find(ctx context.Context, request FindRequest) (FindResult, error) {
	directory, name, queryErr := parseP4FindQuery(request.QueryName)
	if queryErr != nil {
		return FindResult{Status: "missing_context", Reason: queryErr.Error(), Paths: []string{}}, nil
	}
	var wire findWireResult
	err := v.source.rpc.call(ctx, "find_files", map[string]any{
		"query_name": request.QueryName, "case_sensitive": request.CaseSensitive,
	}, &wire)
	if err != nil {
		var rpcErr *rpcCallError
		if errors.As(err, &rpcErr) && rpcErr.Code == "missing_context" {
			return FindResult{Status: "missing_context", Reason: rpcErr.Message, Paths: []string{}}, nil
		}
		return FindResult{}, err
	}
	if wire.Paths == nil || wire.ResolvedSpecs == nil || wire.Truncated == nil || len(wire.Paths) != len(wire.ResolvedSpecs) || len(wire.Paths) > 100 {
		return FindResult{}, errors.New("P4 find response is incomplete")
	}
	if !sort.StringsAreSorted(wire.Paths) || !sort.StringsAreSorted(wire.ResolvedSpecs) {
		return FindResult{}, errors.New("P4 find response is not sorted")
	}
	for index, foundPath := range wire.Paths {
		if index > 0 && wire.Paths[index-1] == foundPath {
			return FindResult{}, errors.New("P4 find response contains duplicate paths")
		}
		if pathIsUnsafe(foundPath) || strings.ContainsAny(foundPath, "@#%*?") {
			return FindResult{}, errors.New("P4 find response contains an unsafe path")
		}
		if !strings.HasPrefix(foundPath, directory+"/") || !findNameMatches(path.Base(foundPath), name, request.CaseSensitive) {
			return FindResult{}, errors.New("P4 find response is outside the requested fixed prefix or name")
		}
		if err := v.validateExactSpecForPath(wire.ResolvedSpecs[index], foundPath); err != nil {
			return FindResult{}, err
		}
	}
	return FindResult{Status: "ok", Paths: wire.Paths, Truncated: *wire.Truncated}, nil
}

func (v *p4View) Search(ctx context.Context, request SearchRequest) (SearchResult, error) {
	if strings.TrimSpace(request.SearchText) == "" || len(request.SearchText) > 256 || len(request.FilePatterns) != 1 || request.UsePerlRegexp {
		return SearchResult{}, errors.New("P4 search requires text and one exact file path")
	}
	pattern := request.FilePatterns[0]
	if strings.ContainsAny(pattern, "*?@#%") || pathIsUnsafe(pattern) {
		return SearchResult{}, errors.New("P4 search requires one canonical exact file path")
	}
	var wire searchWireResult
	if err := v.source.rpc.call(ctx, "search_exact_file", map[string]any{
		"search_text": request.SearchText,
		"path":        pattern, "case_sensitive": request.CaseSensitive,
	}, &wire); err != nil {
		return SearchResult{}, err
	}
	if !lowerSHA256.MatchString(wire.SHA256) {
		return SearchResult{}, errors.New("P4 search returned an invalid hash")
	}
	if err := v.validateDynamicContentIdentity(pattern, wire.Spec, wire.SHA256); err != nil {
		return SearchResult{}, err
	}
	result := SearchResult{Status: "ok", ResolvedSpecs: []string{wire.Spec}}
	for _, match := range wire.Matches {
		if match.Line <= 0 {
			return SearchResult{}, errors.New("P4 search returned an invalid match")
		}
		result.Matches = append(result.Matches, SearchMatch{Path: pattern, Line: match.Line, Text: match.Content})
	}
	return result, nil
}

func (v *p4View) FinalizeReceipt(ctx context.Context) (SourceReceipt, error) {
	var wire receiptWireResult
	if err := v.source.rpc.call(ctx, "finalize_receipt", struct{}{}, &wire); err != nil {
		return SourceReceipt{}, err
	}
	if wire.Schema != "ocr.source-receipt/v1" || wire.QueryCount < 0 ||
		!lowerSHA256.MatchString(wire.LedgerSHA256) || !lowerSHA256.MatchString(wire.ReceiptSHA256) {
		return SourceReceipt{}, errors.New("P4 source receipt is invalid")
	}
	receiptBasis, err := json.Marshal(struct {
		Schema       string `json:"schema"`
		QueryCount   int    `json:"query_count"`
		LedgerSHA256 string `json:"ledger_sha256"`
	}{Schema: wire.Schema, QueryCount: wire.QueryCount, LedgerSHA256: wire.LedgerSHA256})
	if err != nil || sha256Hex(receiptBasis) != wire.ReceiptSHA256 {
		return SourceReceipt{}, errors.New("P4 source receipt hash is invalid")
	}
	return SourceReceipt{SchemaVersion: wire.Schema, QueryCount: wire.QueryCount,
		LedgerSHA256: wire.LedgerSHA256, ReceiptSHA256: wire.ReceiptSHA256}, nil
}

func validateResolvedSpecs(specs []string) error {
	if len(specs) == 0 {
		return errors.New("P4 source response has no exact resolved spec")
	}
	if !sort.StringsAreSorted(specs) {
		return errors.New("P4 resolved specs are not sorted")
	}
	for index, spec := range specs {
		if index > 0 && specs[index-1] == spec {
			return errors.New("P4 resolved specs contain duplicates")
		}
		hash := strings.LastIndexByte(spec, '#')
		if hash <= 2 || !strings.HasPrefix(spec, "//") || !positiveInt.MatchString(spec[hash+1:]) || strings.ContainsAny(spec[:hash], "@%") {
			return fmt.Errorf("P4 response contains non-exact spec %q", spec)
		}
	}
	return nil
}

func pathIsUnsafe(value string) bool {
	clean := strings.ReplaceAll(value, "\\", "/")
	return clean == "" || clean == "." || path.Clean(clean) != clean || path.IsAbs(clean) || strings.HasPrefix(clean, "../")
}

func (v *p4View) validateResolvedSpecsForPath(specs []string, reviewPath string) error {
	if err := validateResolvedSpecs(specs); err != nil {
		return err
	}
	for _, spec := range specs {
		if err := v.validateExactSpecForPath(spec, reviewPath); err != nil {
			return err
		}
	}
	return nil
}

func (v *p4View) validateExactSpecForPath(spec, reviewPath string) error {
	if err := validateResolvedSpecs([]string{spec}); err != nil {
		return err
	}
	hash := strings.LastIndexByte(spec, '#')
	if hash < 0 || spec[:hash] != v.source.manifest.DepotPrefix+"/"+reviewPath {
		return fmt.Errorf("P4 response resolved a spec outside requested path: %q", spec)
	}
	for _, file := range v.source.manifest.Files {
		if file.Path == reviewPath && spec != file.HeadSpec {
			return fmt.Errorf("P4 response spec differs from manifest head for %q", reviewPath)
		}
	}
	return nil
}

func (v *p4View) validateDynamicContentIdentity(reviewPath, spec, hash string) error {
	if err := v.validateExactSpecForPath(spec, reviewPath); err != nil {
		return err
	}
	for _, file := range v.source.manifest.Files {
		if file.Path == reviewPath && hash != file.HeadSHA256 {
			return fmt.Errorf("P4 response hash differs from manifest head for %q", reviewPath)
		}
	}
	return nil
}

func parseP4FindQuery(query string) (string, string, error) {
	query = strings.ReplaceAll(strings.TrimSpace(query), "\\", "/")
	if query == "" || len(query) > 512 || strings.HasPrefix(query, "//") || strings.HasPrefix(query, "../") || path.IsAbs(query) ||
		strings.ContainsAny(query, "@#%*?\x00\r\n") || path.Clean(query) != query {
		return "", "", errors.New("find requires a canonical relative query with a fixed directory prefix")
	}
	slash := strings.LastIndexByte(query, '/')
	if slash <= 0 || slash == len(query)-1 || strings.Contains(query[:slash], "...") || query[slash+1:] == "..." {
		return "", "", errors.New("find rejects depot roots, bare ..., and unfixed directory prefixes")
	}
	return query[:slash], query[slash+1:], nil
}

func findNameMatches(base, query string, caseSensitive bool) bool {
	if !caseSensitive {
		base, query = strings.ToLower(base), strings.ToLower(query)
	}
	return strings.Contains(base, query)
}
