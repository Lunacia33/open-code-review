// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package reviewsource

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

const p4ManifestSchema = "ocr.p4-review-source/v1"

var (
	lowerSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)
	positiveInt = regexp.MustCompile(`^[1-9][0-9]*$`)
)

type p4Manifest struct {
	SchemaVersion         string           `json:"schema_version"`
	SourceKind            string           `json:"source_kind"`
	SnapshotID            string           `json:"snapshot_id"`
	SnapshotRawSHA256     string           `json:"snapshot_raw_sha256"`
	CaseManifestRawSHA256 string           `json:"case_manifest_raw_sha256"`
	SessionScopeKey       string           `json:"session_scope_key"`
	RuleArtifactSHA256    string           `json:"rule_artifact_sha256,omitempty"`
	P4Port                string           `json:"p4_port"`
	ServerAddress         string           `json:"server_address"`
	DepotPrefix           string           `json:"depot_prefix"`
	ChangeCL              int64            `json:"change_cl"`
	DescribeSHA256        string           `json:"describe_sha256"`
	Files                 []p4ManifestFile `json:"files"`
}

type p4ManifestFile struct {
	DepotPath      string `json:"depot_path"`
	Path           string `json:"path"`
	Action         string `json:"action"`
	Type           string `json:"type"`
	Binary         bool   `json:"binary"`
	BaseSpec       string `json:"base_spec,omitempty"`
	HeadSpec       string `json:"head_spec"`
	BaseSHA256     string `json:"base_sha256,omitempty"`
	HeadSHA256     string `json:"head_sha256"`
	BaseByteLength *int64 `json:"base_byte_length,omitempty"`
	HeadByteLength int64  `json:"head_byte_length"`
}

func decodeP4Manifest(manifestPath, socketPath string) (p4Manifest, []byte, error) {
	if !filepath.IsAbs(manifestPath) {
		return p4Manifest{}, nil, errors.New("P4 source manifest path must be absolute")
	}
	if !filepath.IsAbs(socketPath) {
		return p4Manifest{}, nil, errors.New("P4 source socket path must be absolute")
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return p4Manifest{}, nil, fmt.Errorf("read P4 source manifest: %w", err)
	}
	var manifest p4Manifest
	if err := decodeStrictJSON(raw, &manifest); err != nil {
		return p4Manifest{}, nil, fmt.Errorf("decode P4 source manifest: %w", err)
	}
	if err := manifest.validate(); err != nil {
		return p4Manifest{}, nil, err
	}
	return manifest, raw, nil
}

func decodeStrictJSON(data []byte, target any) error {
	if err := rejectDuplicateKeys(data); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return errors.New("multiple JSON values are not allowed")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func rejectDuplicateKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	var walk func() error
	walk = func() error {
		token, err := dec.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := make(map[string]struct{})
			for dec.More() {
				keyToken, err := dec.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return errors.New("JSON object key is not a string")
				}
				if _, exists := seen[key]; exists {
					return fmt.Errorf("duplicate JSON key %q", key)
				}
				seen[key] = struct{}{}
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		case '[':
			for dec.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		default:
			return errors.New("unexpected JSON delimiter")
		}
	}
	if err := walk(); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func (m p4Manifest) validate() error {
	if m.SchemaVersion != p4ManifestSchema || m.SourceKind != "p4-submitted" {
		return errors.New("unsupported P4 source manifest schema or source_kind")
	}
	if !lowerSHA256.MatchString(m.SnapshotID) || !lowerSHA256.MatchString(m.SnapshotRawSHA256) ||
		!lowerSHA256.MatchString(m.CaseManifestRawSHA256) || !lowerSHA256.MatchString(m.DescribeSHA256) ||
		(m.RuleArtifactSHA256 != "" && !lowerSHA256.MatchString(m.RuleArtifactSHA256)) {
		return errors.New("P4 source manifest has an invalid snapshot or describe hash")
	}
	if strings.TrimSpace(m.SessionScopeKey) == "" || len(m.SessionScopeKey) > 512 || strings.ContainsRune(m.SessionScopeKey, 0) {
		return errors.New("P4 source manifest has an invalid session_scope_key")
	}
	if strings.TrimSpace(m.P4Port) == "" || strings.TrimSpace(m.ServerAddress) == "" || m.ChangeCL <= 0 {
		return errors.New("P4 source manifest backend identity is incomplete")
	}
	if !strings.HasPrefix(m.DepotPrefix, "//") || strings.HasSuffix(m.DepotPrefix, "/") || strings.ContainsAny(m.DepotPrefix, "@#%") {
		return errors.New("P4 source manifest depot_prefix is not canonical")
	}
	if len(m.Files) == 0 {
		return errors.New("P4 source manifest has no files")
	}
	paths := make(map[string]struct{}, len(m.Files))
	depots := make(map[string]struct{}, len(m.Files))
	for _, f := range m.Files {
		if err := f.validate(m.DepotPrefix); err != nil {
			return fmt.Errorf("invalid P4 source file %q: %w", f.Path, err)
		}
		if _, ok := paths[f.Path]; ok {
			return fmt.Errorf("duplicate P4 source path %q", f.Path)
		}
		if _, ok := depots[f.DepotPath]; ok {
			return fmt.Errorf("duplicate P4 source depot path %q", f.DepotPath)
		}
		paths[f.Path] = struct{}{}
		depots[f.DepotPath] = struct{}{}
	}
	return nil
}

func (f p4ManifestFile) validate(prefix string) error {
	clean := path.Clean(strings.ReplaceAll(f.Path, "\\", "/"))
	if f.Path == "" || clean != f.Path || clean == "." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
		return errors.New("review path is not canonical and relative")
	}
	if f.DepotPath != prefix+"/"+f.Path || strings.ContainsAny(f.DepotPath, "@#%") {
		return errors.New("depot path is outside depot_prefix")
	}
	if f.Action != "add" && f.Action != "edit" {
		return errors.New("only submitted add/edit actions are supported")
	}
	if strings.TrimSpace(f.Type) == "" {
		return errors.New("P4 file type is required")
	}
	if err := validateExactSpec(f.HeadSpec, f.DepotPath); err != nil {
		return fmt.Errorf("invalid head_spec: %w", err)
	}
	if !lowerSHA256.MatchString(f.HeadSHA256) || f.HeadByteLength < 0 {
		return errors.New("invalid head hash or byte length")
	}
	typeIsBinary := strings.Contains(strings.ToLower(f.Type), "binary")
	if typeIsBinary != f.Binary {
		return errors.New("binary flag does not match P4 file type")
	}
	if f.Action == "add" {
		if f.BaseSpec != "" || f.BaseSHA256 != "" || f.BaseByteLength != nil {
			return errors.New("add must not carry base content")
		}
		return nil
	}
	if err := validateExactSpec(f.BaseSpec, f.DepotPath); err != nil {
		return fmt.Errorf("invalid base_spec: %w", err)
	}
	if !lowerSHA256.MatchString(f.BaseSHA256) || f.BaseByteLength == nil || *f.BaseByteLength < 0 {
		return errors.New("edit has invalid base hash or byte length")
	}
	return nil
}

func validateExactSpec(spec, depot string) error {
	prefix := depot + "#"
	if !strings.HasPrefix(spec, prefix) || !positiveInt.MatchString(strings.TrimPrefix(spec, prefix)) {
		return errors.New("spec must be an exact positive #revision")
	}
	return nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
