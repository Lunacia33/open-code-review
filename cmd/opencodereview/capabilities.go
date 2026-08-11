// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

type capabilityDocument struct {
	Schema                string   `json:"schema"`
	ReviewSources         []string `json:"review_sources"`
	SourceManifestSchemas []string `json:"source_manifest_schemas"`
	SourceRPCSchemas      []string `json:"source_rpc_schemas"`
	RunManifestSchemas    []string `json:"run_manifest_schemas"`
	SourceMethods         []string `json:"source_methods"`
	NativeP4BaseCommit    string   `json:"native_p4_base_commit"`
	BuildCommit           string   `json:"build_commit"`
}

var capabilitiesCmd = &cobra.Command{
	Use:   "capabilities",
	Short: "Show machine-readable runtime capabilities",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		doc := capabilityDocument{
			Schema:                "ocr.capabilities/v1",
			ReviewSources:         []string{"git", "p4-submitted"},
			SourceManifestSchemas: []string{"ocr.p4-review-source/v1"},
			SourceRPCSchemas:      []string{"ocr.source-rpc/v1"},
			RunManifestSchemas:    []string{"ocr.run-manifest/v1", "ocr.run-manifest/v2"},
			SourceMethods:         []string{"resolve_changed_files", "read_lines", "find_files", "search_exact_file", "finalize_receipt"},
			NativeP4BaseCommit:    "200424c88ae45d2a51eec7388cd29344f54d4fbd",
			BuildCommit:           GitCommit,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(doc); err != nil {
			return fmt.Errorf("encode capabilities: %w", err)
		}
		return nil
	},
}
