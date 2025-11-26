// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// loggingPolicy is a custom pipeline policy that logs HTTP requests and responses
type loggingPolicy struct {
	logFile *os.File
}

// NewLoggingPolicy creates a new logging policy that writes to the specified file
func NewLoggingPolicy(logFile *os.File) policy.Policy {
	return &loggingPolicy{logFile: logFile}
}

// Do implements the policy.Policy interface
func (p *loggingPolicy) Do(req *policy.Request) (*http.Response, error) {
	p.logRequest(req)

	resp, err := req.Next()
	if err != nil {
		p.logError(err)
		return resp, err
	}

	p.logResponse(resp)

	return resp, nil
}

func (p *loggingPolicy) logRequest(req *policy.Request) {
	if p.logFile == nil {
		return
	}

	fmt.Fprintf(p.logFile, "\n=== REQUEST [%s] ===\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(p.logFile, "%s %s\n", req.Raw().Method, req.Raw().URL.String())

	// Log request body if present
	if req.Raw().Body == nil {
		fmt.Fprintf(p.logFile, "Body: (none)\n")
		return
	}

	body, err := io.ReadAll(req.Raw().Body)
	// Always restore the body, even if read failed (restore what we got)
	req.Raw().Body = io.NopCloser(bytes.NewReader(body))

	if err != nil {
		fmt.Fprintf(p.logFile, "Body: (error reading: %v)\n", err)
		return
	}

	if len(body) == 0 {
		fmt.Fprintf(p.logFile, "Body: (empty)\n")
		return
	}

	fmt.Fprintf(p.logFile, "Body:\n")
	var prettyPayload interface{}
	if err := json.Unmarshal(body, &prettyPayload); err == nil {
		prettyJSON, _ := json.MarshalIndent(prettyPayload, "", "  ")
		fmt.Fprintf(p.logFile, "%s\n", string(prettyJSON))
	} else {
		fmt.Fprintf(p.logFile, "%s\n", string(body))
	}
}

func (p *loggingPolicy) logResponse(resp *http.Response) {
	if p.logFile == nil || resp == nil {
		return
	}

	fmt.Fprintf(p.logFile, "\n=== RESPONSE [%s] ===\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(p.logFile, "Status Code: %d\n", resp.StatusCode)

	fmt.Fprintf(p.logFile, "Headers:\n")
	for _, key := range slices.Sorted(maps.Keys(resp.Header)) {
		for _, value := range resp.Header[key] {
			fmt.Fprintf(p.logFile, "  %s: %s\n", key, value)
		}
	}

	// Read and log response body, then restore it
	if resp.Body == nil {
		fmt.Fprintf(p.logFile, "Body: (none)\n\n")
		return
	}

	body, err := io.ReadAll(resp.Body)
	// Always restore the body, even if read failed (restore what we got)
	resp.Body = io.NopCloser(bytes.NewReader(body))

	if err != nil {
		fmt.Fprintf(p.logFile, "Body: (error reading: %v)\n\n", err)
		return
	}

	fmt.Fprintf(p.logFile, "Body:\n")
	if len(body) == 0 {
		fmt.Fprintf(p.logFile, "(empty)\n\n")
		return
	}

	var jsonResponse interface{}
	if err := json.Unmarshal(body, &jsonResponse); err == nil {
		prettyJSON, _ := json.MarshalIndent(jsonResponse, "", "  ")
		fmt.Fprintf(p.logFile, "%s\n\n", string(prettyJSON))
	} else {
		fmt.Fprintf(p.logFile, "%s\n\n", string(body))
	}
}

func (p *loggingPolicy) logError(err error) {
	if p.logFile == nil {
		return
	}

	fmt.Fprintf(p.logFile, "\n=== ERROR [%s] ===\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(p.logFile, "Error: %v\n\n", err)
}
