// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package reviewsource

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validManifest() p4Manifest {
	baseLength := int64(4)
	return p4Manifest{
		SchemaVersion: p4ManifestSchema, SourceKind: "p4-submitted",
		SnapshotID: strings.Repeat("1", 64), SnapshotRawSHA256: strings.Repeat("5", 64),
		CaseManifestRawSHA256: strings.Repeat("6", 64), SessionScopeKey: "p4://depot/main@42",
		P4Port:        "ssl:perforce.example:1666",
		ServerAddress: "perforce.example:1666", DepotPrefix: "//depot/main", ChangeCL: 42,
		DescribeSHA256: strings.Repeat("2", 64),
		Files: []p4ManifestFile{{
			DepotPath: "//depot/main/a.go", Path: "a.go", Action: "edit", Type: "text",
			BaseSpec: "//depot/main/a.go#3", HeadSpec: "//depot/main/a.go#4",
			BaseSHA256: strings.Repeat("3", 64), HeadSHA256: strings.Repeat("4", 64),
			BaseByteLength: &baseLength, HeadByteLength: 5,
		}},
	}
}

func writeManifest(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(t.TempDir(), "source.json")
	if err := os.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestDecodeP4ManifestStrictAndAbsolute(t *testing.T) {
	manifest := validManifest()
	path := writeManifest(t, manifest)
	socket := filepath.Join(t.TempDir(), "source.sock")
	got, _, err := decodeP4Manifest(path, socket)
	if err != nil || got.ChangeCL != 42 {
		t.Fatalf("decode manifest: got=%+v err=%v", got, err)
	}
	if _, _, err := decodeP4Manifest("relative.json", socket); err == nil {
		t.Fatal("relative manifest path was accepted")
	}
	if _, _, err := decodeP4Manifest(path, "relative.sock"); err == nil {
		t.Fatal("relative socket path was accepted")
	}

	raw := strings.TrimSuffix(string(mustJSON(t, manifest)), "}") + `,"unknown":true}`
	badPath := filepath.Join(t.TempDir(), "unknown.json")
	if err := os.WriteFile(badPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := decodeP4Manifest(badPath, socket); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown manifest field was not rejected: %v", err)
	}
	duplicate := strings.Replace(string(mustJSON(t, manifest)), `"schema_version":`, `"schema_version":"ocr.p4-review-source/v1","schema_version":`, 1)
	duplicatePath := filepath.Join(t.TempDir(), "duplicate.json")
	if err := os.WriteFile(duplicatePath, []byte(duplicate), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := decodeP4Manifest(duplicatePath, socket); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate manifest key was not rejected: %v", err)
	}
}

func TestManifestRejectsNonExactAndUnsupportedActions(t *testing.T) {
	manifest := validManifest()
	manifest.Files[0].HeadSpec = "//depot/main/a.go@42"
	if err := manifest.validate(); err == nil {
		t.Fatal("@CL head spec was accepted")
	}
	manifest = validManifest()
	manifest.Files[0].Action = "delete"
	if err := manifest.validate(); err == nil {
		t.Fatal("delete action was accepted")
	}
	manifest = validManifest()
	manifest.Files[0].Path = "../a.go"
	if err := manifest.validate(); err == nil {
		t.Fatal("escaping path was accepted")
	}
	manifest = validManifest()
	manifest.Files[0].Binary = true
	if err := manifest.validate(); err == nil {
		t.Fatal("binary flag inconsistent with file type was accepted")
	}
}

func TestIdentityDoesNotResolveRPC(t *testing.T) {
	manifest := validManifest()
	manifest.RuleArtifactSHA256 = strings.Repeat("7", 64)
	path := writeManifest(t, manifest)
	source, err := OpenP4(path, filepath.Join(t.TempDir(), "not-running.sock"))
	if err != nil {
		t.Fatal(err)
	}
	identity, ok := Identity(source)
	if !ok || identity.SubmittedCL != manifest.ChangeCL || identity.SessionScopeKey != manifest.SessionScopeKey ||
		identity.SnapshotRawSHA256 != manifest.SnapshotRawSHA256 || identity.CaseManifestRawSHA256 != manifest.CaseManifestRawSHA256 ||
		identity.RuleArtifactSHA256 != manifest.RuleArtifactSHA256 {
		t.Fatalf("identity = %+v, ok=%v", identity, ok)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
