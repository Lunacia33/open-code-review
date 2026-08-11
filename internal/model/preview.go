// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package model

// ExcludeReason describes why a file was excluded from review. Shared by
// both diff review (internal/agent) and full-file scan (internal/scan).
type ExcludeReason string

const (
	ExcludeNone        ExcludeReason = ""
	ExcludeUserRule    ExcludeReason = "user_exclude"
	ExcludeExtension   ExcludeReason = "unsupported_ext"
	ExcludeDefaultPath ExcludeReason = "default_path"
	ExcludeDeleted     ExcludeReason = "deleted"
	ExcludeBinary      ExcludeReason = "binary"
)

// PreviewEntry is one file's preview record (mode-agnostic).
type PreviewEntry struct {
	Path          string        `json:"path"`
	Status        string        `json:"status"`
	Insertions    int64         `json:"insertions"`
	Deletions     int64         `json:"deletions"`
	WillReview    bool          `json:"will_review"`
	ExcludeReason ExcludeReason `json:"exclude_reason,omitempty"`
}

// Preview is the full preview result, mode-agnostic so cmd/opencodereview
// can render it the same way for review and scan.
type Preview struct {
	Entries         []PreviewEntry `json:"files"`
	TotalInsertions int64          `json:"total_insertions"`
	TotalDeletions  int64          `json:"total_deletions"`
	TotalFiles      int            `json:"total_files"`
	ReviewableCount int            `json:"reviewable_count"`
	ExcludedCount   int            `json:"excluded_count"`
	Source          *PreviewSource `json:"source,omitempty"`
}

// PreviewSource carries the same source identity and final audit receipt used
// by a P4 run manifest, without exposing connection details or raw paths.
type PreviewSource struct {
	InputMode                string `json:"input_mode"`
	RepositoryIdentitySHA256 string `json:"repository_identity_sha256"`
	SourceManifestSHA256     string `json:"source_manifest_sha256"`
	SessionScopeSHA256       string `json:"session_scope_sha256"`
	SubmittedCL              int64  `json:"submitted_cl"`
	ReceiptSchema            string `json:"receipt_schema"`
	QueryCount               int    `json:"query_count"`
	LedgerSHA256             string `json:"ledger_sha256"`
	ReceiptSHA256            string `json:"receipt_sha256"`
}
