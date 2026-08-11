// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package reviewsource

import (
	"strings"
	"testing"
)

func TestBuildUnifiedDiffAddAndFinalNewline(t *testing.T) {
	diff := buildUnifiedDiff("new.go", "add", false, nil, []byte("one\ntwo\n"))
	if !diff.IsNew || diff.OldPath != "/dev/null" || diff.NewFileContent != "one\ntwo\n" {
		t.Fatalf("add metadata = %+v", diff)
	}
	for _, want := range []string{"new file mode 100644", "@@ -0,0 +1,2 @@", "+one\n+two\n"} {
		if !strings.Contains(diff.Diff, want) {
			t.Fatalf("diff does not contain %q:\n%s", want, diff.Diff)
		}
	}
	if diff.Insertions != 2 || diff.Deletions != 0 {
		t.Fatalf("counts = +%d -%d", diff.Insertions, diff.Deletions)
	}
}

func TestBuildUnifiedDiffEditHasSeparateHunksAndNoNewlineMarker(t *testing.T) {
	base := []byte("a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk")
	head := []byte("a\nB\nc\nd\ne\nf\ng\nh\ni\nJ\nk")
	diff := buildUnifiedDiff("a.txt", "edit", false, base, head)
	if strings.Count(diff.Diff, "@@ ") != 2 {
		t.Fatalf("expected two hunks:\n%s", diff.Diff)
	}
	if strings.Count(diff.Diff, "\\ No newline at end of file") != 1 {
		t.Fatalf("expected final newline marker:\n%s", diff.Diff)
	}
	if diff.Insertions != 2 || diff.Deletions != 2 || diff.NewFileContent != string(head) {
		t.Fatalf("edit metadata = %+v", diff)
	}
}

func TestBuildUnifiedDiffBinary(t *testing.T) {
	diff := buildUnifiedDiff("asset.bin", "edit", true, []byte{0}, []byte{1})
	if !diff.IsBinary || diff.NewFileContent != "" || !strings.Contains(diff.Diff, "Binary files") {
		t.Fatalf("binary diff = %+v", diff)
	}
}

func TestLargeDiffUsesBoundedFallback(t *testing.T) {
	base := make([]sourceLine, 5000)
	head := make([]sourceLine, 5000)
	for index := range base {
		base[index] = sourceLine{text: "old", terminated: true}
		head[index] = sourceLine{text: "new", terminated: true}
	}
	ops := myersDiff(base, head)
	if len(ops) != 10000 || ops[0].kind != '-' || ops[len(ops)-1].kind != '+' {
		t.Fatalf("bounded fallback produced invalid operations: len=%d", len(ops))
	}
}

func TestMyersOperationsReconstructBothSides(t *testing.T) {
	cases := []struct{ old, new string }{
		{"", ""}, {"a\n", "a\n"}, {"a", "a\n"}, {"a\nb\n", "a\nc\nb\n"}, {"x\ny\nz", "y\nq"},
	}
	for _, test := range cases {
		ops := myersDiff(splitSourceLines([]byte(test.old)), splitSourceLines([]byte(test.new)))
		var oldLines, newLines []sourceLine
		for _, op := range ops {
			if op.kind != '+' {
				oldLines = append(oldLines, op.line)
			}
			if op.kind != '-' {
				newLines = append(newLines, op.line)
			}
		}
		if !equalSourceLines(oldLines, splitSourceLines([]byte(test.old))) || !equalSourceLines(newLines, splitSourceLines([]byte(test.new))) {
			t.Fatalf("operations do not reconstruct old=%q new=%q: %+v", test.old, test.new, ops)
		}
	}
}

func equalSourceLines(left, right []sourceLine) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
