// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package reviewsource

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestUnixSocketRPCEnvelope(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix domain sockets are exercised on Unix CI")
	}
	socket := filepath.Join(t.TempDir(), "source.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Skipf("Unix socket unavailable: %v", err)
	}
	defer listener.Close()
	manifestHash := strings.Repeat("a", 64)
	done := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			done <- acceptErr
			return
		}
		defer conn.Close()
		var request rpcRequest
		if decodeErr := json.NewDecoder(conn).Decode(&request); decodeErr != nil {
			done <- decodeErr
			return
		}
		if request.Schema != rpcSchema || request.Method != "find_files" || request.SourceManifestSHA256 != manifestHash {
			done <- &testError{"unexpected RPC envelope"}
			return
		}
		result, _ := json.Marshal(FindResult{Status: "missing_context", Reason: "disabled", Paths: []string{}})
		done <- json.NewEncoder(conn).Encode(rpcResponse{
			Schema: rpcSchema, OK: true, Result: result,
		})
	}()
	client := newRPCClient(socket, manifestHash)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var result FindResult
	if err := client.call(ctx, "find_files", map[string]any{"query_name": "x"}, &result); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type testError struct{ message string }

func (e *testError) Error() string { return e.message }
