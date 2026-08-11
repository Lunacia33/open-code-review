// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/alibaba/open-code-review/internal/reviewsource"
)

type findOutputView struct {
	reviewsource.SourceView
	truncated bool
}

func (v findOutputView) Find(context.Context, reviewsource.FindRequest) (reviewsource.FindResult, error) {
	return reviewsource.FindResult{Status: "ok", Paths: []string{"Source/a.go"}, Truncated: v.truncated}, nil
}

func TestSourceFileFindPrintsExplicitTruncation(t *testing.T) {
	for _, truncated := range []bool{false, true} {
		output, err := sourceFileFind(findOutputView{truncated: truncated})(context.Background(), map[string]any{"query_name": "Source/a"})
		if err != nil {
			t.Fatal(err)
		}
		expected := "TRUNCATED: " + fmt.Sprint(truncated)
		if !strings.Contains(output, expected) || !strings.Contains(output, "Source/a.go") {
			t.Fatalf("find output = %q", output)
		}
	}
}
