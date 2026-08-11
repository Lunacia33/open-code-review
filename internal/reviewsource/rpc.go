// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package reviewsource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

const (
	rpcSchema           = "ocr.source-rpc/v1"
	maxRPCResponseBytes = 256 << 20
)

type rpcClient struct {
	socketPath           string
	sourceManifestSHA256 string
	dial                 func(context.Context, string, string) (net.Conn, error)
}

type rpcRequest struct {
	Schema               string `json:"schema"`
	Method               string `json:"method"`
	SourceManifestSHA256 string `json:"source_manifest_sha256"`
	Params               any    `json:"params"`
}

type rpcResponse struct {
	Schema string          `json:"schema"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcErrorBody   `json:"error,omitempty"`
}

type rpcErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type rpcCallError struct {
	Method  string
	Code    string
	Message string
}

func (e *rpcCallError) Error() string {
	return fmt.Sprintf("P4 source RPC %s failed (%s): %s", e.Method, e.Code, e.Message)
}

func newRPCClient(socketPath, sourceManifestSHA256 string) *rpcClient {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	return &rpcClient{
		socketPath:           socketPath,
		sourceManifestSHA256: sourceManifestSHA256,
		dial:                 dialer.DialContext,
	}
}

func (c *rpcClient) call(ctx context.Context, method string, params, result any) error {
	if c == nil || c.dial == nil {
		return errors.New("P4 source RPC client is not configured")
	}
	if !lowerSHA256.MatchString(c.sourceManifestSHA256) {
		return errors.New("P4 source RPC client has no valid source manifest hash")
	}
	conn, err := c.dial(ctx, "unix", c.socketPath)
	if err != nil {
		return fmt.Errorf("connect P4 source socket: %w", err)
	}
	defer conn.Close()
	stopCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopCancel()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	request := rpcRequest{
		Schema: rpcSchema, Method: method,
		SourceManifestSHA256: c.sourceManifestSHA256, Params: params,
	}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return fmt.Errorf("write P4 source RPC request: %w", err)
	}
	rawResponse, err := io.ReadAll(io.LimitReader(conn, maxRPCResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read P4 source RPC response: %w", err)
	}
	if len(rawResponse) > maxRPCResponseBytes {
		return errors.New("P4 source RPC response exceeds byte limit")
	}
	var response rpcResponse
	if err := decodeStrictJSON(rawResponse, &response); err != nil {
		return fmt.Errorf("read P4 source RPC response: %w", err)
	}
	if response.Schema != rpcSchema {
		return errors.New("P4 source RPC response schema mismatch")
	}
	if !response.OK {
		if response.Error == nil || response.Error.Code == "" || response.Error.Message == "" || len(response.Result) != 0 {
			return errors.New("P4 source RPC failure response is malformed")
		}
		return &rpcCallError{Method: method, Code: response.Error.Code, Message: response.Error.Message}
	}
	if response.Error != nil || len(response.Result) == 0 {
		return errors.New("P4 source RPC success response is malformed")
	}
	if err := decodeStrictJSON(response.Result, result); err != nil {
		return fmt.Errorf("decode P4 source RPC %s result: %w", method, err)
	}
	return nil
}
