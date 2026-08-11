// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

// Package reviewsource defines review inputs that are independent of a Git
// working tree. The first implementation is a manifest-bound, submitted P4
// source whose bytes and dynamic queries are served by an audited Unix socket.
package reviewsource

import (
	"context"

	"github.com/alibaba/open-code-review/internal/model"
)

// ReviewSource resolves the immutable changed-file input and exposes the
// matching dynamic source view.
type ReviewSource interface {
	ResolveDiffs(context.Context) ([]model.Diff, SourceIdentity, error)
	View() SourceView
}

// IdentityProvider exposes manifest-derived identity without resolving source
// bytes or consuming a source-service query.
type IdentityProvider interface {
	SourceIdentity() SourceIdentity
}

// Identity returns an already parsed source identity without triggering RPC.
func Identity(source ReviewSource) (SourceIdentity, bool) {
	provider, ok := source.(IdentityProvider)
	if !ok || provider == nil {
		return SourceIdentity{}, false
	}
	return provider.SourceIdentity(), true
}

// SourceView is the dynamic context surface used by review tools.
type SourceView interface {
	ReadLines(context.Context, string, int, int) (ReadLinesResult, error)
	Find(context.Context, FindRequest) (FindResult, error)
	Search(context.Context, SearchRequest) (SearchResult, error)
	FinalizeReceipt(context.Context) (SourceReceipt, error)
}

// SourceIdentity is the non-secret identity returned with resolved diffs.
type SourceIdentity struct {
	Mode                     string `json:"mode"`
	SnapshotID               string `json:"snapshot_id"`
	ManifestSHA256           string `json:"manifest_sha256"`
	SnapshotRawSHA256        string `json:"snapshot_raw_sha256"`
	CaseManifestRawSHA256    string `json:"case_manifest_raw_sha256"`
	SessionScopeKey          string `json:"session_scope_key"`
	RuleArtifactSHA256       string `json:"rule_artifact_sha256,omitempty"`
	SubmittedCL              int64  `json:"submitted_cl"`
	RepositoryIdentitySHA256 string `json:"repository_identity_sha256"`
}

type ReadLinesResult struct {
	Path          string   `json:"path"`
	StartLine     int      `json:"start_line"`
	EndLine       int      `json:"end_line"`
	TotalLines    int      `json:"total_lines"`
	Content       string   `json:"content"`
	ResolvedSpecs []string `json:"resolved_specs"`
}

type FindRequest struct {
	QueryName     string `json:"query_name"`
	CaseSensitive bool   `json:"case_sensitive"`
}

type FindResult struct {
	Status    string   `json:"status"`
	Reason    string   `json:"reason,omitempty"`
	Paths     []string `json:"paths"`
	Truncated bool     `json:"truncated"`
}

type SearchRequest struct {
	SearchText    string   `json:"search_text"`
	FilePatterns  []string `json:"file_patterns"`
	CaseSensitive bool     `json:"case_sensitive"`
	UsePerlRegexp bool     `json:"use_perl_regexp"`
}

type SearchMatch struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

type SearchResult struct {
	Status        string        `json:"status"`
	Reason        string        `json:"reason,omitempty"`
	Matches       []SearchMatch `json:"matches"`
	ResolvedSpecs []string      `json:"resolved_specs"`
}

type SourceReceipt struct {
	SchemaVersion string `json:"schema_version"`
	SnapshotID    string `json:"snapshot_id"`
	QueryCount    int    `json:"query_count"`
	LedgerSHA256  string `json:"ledger_sha256"`
	ReceiptSHA256 string `json:"receipt_sha256"`
}
