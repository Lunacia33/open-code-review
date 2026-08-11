// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package reviewsource

import (
	"fmt"
	"strings"

	"github.com/alibaba/open-code-review/internal/model"
)

const unifiedContextLines = 3

type sourceLine struct {
	text       string
	terminated bool
}

type diffOp struct {
	kind byte
	line sourceLine
}

type opRange struct{ start, end int }

func buildUnifiedDiff(filePath, action string, binary bool, base, head []byte) model.Diff {
	d := model.Diff{OldPath: filePath, NewPath: filePath, IsBinary: binary, IsNew: action == "add"}
	if action == "add" {
		d.OldPath = "/dev/null"
	}
	if binary {
		d.Diff = fmt.Sprintf("diff --git a/%s b/%s\nBinary files a/%s and b/%s differ\n", filePath, filePath, filePath, filePath)
		return d
	}
	d.NewFileContent = string(head)
	ops := myersDiff(splitSourceLines(base), splitSourceLines(head))
	var out strings.Builder
	fmt.Fprintf(&out, "diff --git a/%s b/%s\n", filePath, filePath)
	if action == "add" {
		out.WriteString("new file mode 100644\n--- /dev/null\n")
	} else {
		fmt.Fprintf(&out, "--- a/%s\n", filePath)
	}
	fmt.Fprintf(&out, "+++ b/%s\n", filePath)
	for _, interval := range hunkRanges(ops, unifiedContextLines) {
		oldStart, newStart := positionsAt(ops, interval.start)
		oldCount, newCount := rangeCounts(ops[interval.start:interval.end])
		if oldCount == 0 {
			oldStart--
		}
		if newCount == 0 {
			newStart--
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount)
		for _, op := range ops[interval.start:interval.end] {
			out.WriteByte(op.kind)
			out.WriteString(op.line.text)
			out.WriteByte('\n')
			if !op.line.terminated {
				out.WriteString("\\ No newline at end of file\n")
			}
		}
	}
	d.Diff = out.String()
	for _, op := range ops {
		switch op.kind {
		case '+':
			d.Insertions++
		case '-':
			d.Deletions++
		}
	}
	return d
}

func splitSourceLines(data []byte) []sourceLine {
	if len(data) == 0 {
		return nil
	}
	text := string(data)
	parts := strings.SplitAfter(text, "\n")
	lines := make([]sourceLine, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		terminated := strings.HasSuffix(part, "\n")
		if terminated {
			part = strings.TrimSuffix(part, "\n")
		}
		lines = append(lines, sourceLine{text: part, terminated: terminated})
	}
	return lines
}

// myersDiff computes a deterministic shortest edit script over complete source
// lines. Equal lines retain their newline-termination bit, so the generated diff
// can represent a final-newline-only change.
func myersDiff(a, b []sourceLine) []diffOp {
	// Myers keeps a trace for backtracking. Bound its worst case for generated
	// files with thousands of entirely different lines; the deterministic
	// prefix/suffix fallback remains correct, though intentionally less compact.
	if len(a)+len(b) > 8000 || int64(len(a))*int64(len(b)) > 4_000_000 {
		return prefixSuffixDiff(a, b)
	}
	max := len(a) + len(b)
	v := map[int]int{1: 0}
	trace := make([]map[int]int, 0, max+1)
	for d := 0; d <= max; d++ {
		trace = append(trace, cloneVector(v))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[k-1] < v[k+1]) {
				x = v[k+1]
			} else {
				x = v[k-1] + 1
			}
			y := x - k
			for x < len(a) && y < len(b) && a[x] == b[y] {
				x++
				y++
			}
			v[k] = x
			if x >= len(a) && y >= len(b) {
				return backtrackMyers(trace, a, b, d)
			}
		}
	}
	return nil
}

func prefixSuffixDiff(a, b []sourceLine) []diffOp {
	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}
	result := make([]diffOp, 0, len(a)+len(b))
	for _, line := range a[:prefix] {
		result = append(result, diffOp{kind: ' ', line: line})
	}
	for _, line := range a[prefix : len(a)-suffix] {
		result = append(result, diffOp{kind: '-', line: line})
	}
	for _, line := range b[prefix : len(b)-suffix] {
		result = append(result, diffOp{kind: '+', line: line})
	}
	for _, line := range a[len(a)-suffix:] {
		result = append(result, diffOp{kind: ' ', line: line})
	}
	return result
}

func cloneVector(in map[int]int) map[int]int {
	out := make(map[int]int, len(in))
	for k, value := range in {
		out[k] = value
	}
	return out
}

func backtrackMyers(trace []map[int]int, a, b []sourceLine, distance int) []diffOp {
	x, y := len(a), len(b)
	reversed := make([]diffOp, 0, len(a)+len(b))
	for d := distance; d >= 0; d-- {
		v := trace[d]
		k := x - y
		var previousK int
		if k == -d || (k != d && v[k-1] < v[k+1]) {
			previousK = k + 1
		} else {
			previousK = k - 1
		}
		previousX := v[previousK]
		previousY := previousX - previousK
		for x > previousX && y > previousY {
			reversed = append(reversed, diffOp{kind: ' ', line: a[x-1]})
			x--
			y--
		}
		if d == 0 {
			break
		}
		if x == previousX {
			reversed = append(reversed, diffOp{kind: '+', line: b[previousY]})
		} else {
			reversed = append(reversed, diffOp{kind: '-', line: a[previousX]})
		}
		x, y = previousX, previousY
	}
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	return reversed
}

func hunkRanges(ops []diffOp, context int) []opRange {
	var ranges []opRange
	for index, op := range ops {
		if op.kind == ' ' {
			continue
		}
		start, equals := index, 0
		for start > 0 && equals < context {
			start--
			if ops[start].kind == ' ' {
				equals++
			}
		}
		end, equals := index+1, 0
		for end < len(ops) && equals < context {
			if ops[end].kind == ' ' {
				equals++
			}
			end++
		}
		if len(ranges) > 0 && start <= ranges[len(ranges)-1].end {
			if end > ranges[len(ranges)-1].end {
				ranges[len(ranges)-1].end = end
			}
			continue
		}
		ranges = append(ranges, opRange{start: start, end: end})
	}
	return ranges
}

func positionsAt(ops []diffOp, end int) (oldLine, newLine int) {
	oldLine, newLine = 1, 1
	for _, op := range ops[:end] {
		if op.kind != '+' {
			oldLine++
		}
		if op.kind != '-' {
			newLine++
		}
	}
	return oldLine, newLine
}

func rangeCounts(ops []diffOp) (oldCount, newCount int) {
	for _, op := range ops {
		if op.kind != '+' {
			oldCount++
		}
		if op.kind != '-' {
			newCount++
		}
	}
	return oldCount, newCount
}
