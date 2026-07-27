// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

type callOptions struct {
	timeout int
	action  string
	body    string
}

func RunShellWithContext(
	ctx context.Context,
	input io.Reader,
	output io.Writer,
	baseUrl string,
	timeout int,
) error {
	done := make(chan error, 1)
	go func() {
		done <- runShell(input, output, baseUrl, timeout)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		fmt.Fprintln(output)
		return nil
	}
}

func normalizeOperation(operation string) (string, error) {
	operation = strings.Trim(strings.ToLower(operation), "/")
	switch operation {
	case "reset", "step", "state", "health", "metadata", "schema":
		return operation, nil
	default:
		return "", &azdext.LocalError{
			Message:    fmt.Sprintf("Unknown environment runtime operation %q.", operation),
			Code:       "rle_unknown_open_env_operation",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Use one of: reset, step, state, health, metadata, schema.",
		}
	}
}

func runShell(input io.Reader, output io.Writer, baseUrl string, timeout int) error {
	fmt.Fprintln(output, "Environment runtime shell. Type help for commands, exit to quit.")
	scanner := bufio.NewScanner(input)
	for {
		fmt.Fprint(output, "rle> ")
		if !scanner.Scan() {
			fmt.Fprintln(output)
			return scanner.Err()
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.EqualFold(line, "exit") || strings.EqualFold(line, "quit") {
			return nil
		}
		if strings.EqualFold(line, "help") {
			printShellHelp(output)
			continue
		}

		operation, payload, _ := strings.Cut(line, " ")
		operation, err := normalizeOperation(operation)
		if err != nil {
			fmt.Fprintf(output, "error: %v\n", err)
			continue
		}
		payload = strings.TrimSpace(payload)
		flags := &callOptions{timeout: timeout}
		switch operation {
		case "reset":
			flags.body = payload
		case "step":
			if payload == "" {
				fmt.Fprintln(output, "error: step requires a JSON action payload, for example: step {\"message\":\"hello\"}")
				continue
			}
			flags.action = payload
		default:
			if payload != "" {
				fmt.Fprintf(output, "error: %s does not accept a JSON payload\n", operation)
				continue
			}
		}

		response, err := call(baseUrl, operation, flags)
		if err != nil {
			fmt.Fprintf(output, "error: %v\n", err)
			continue
		}
		fmt.Fprintln(output, response)
	}
}

func printShellHelp(output io.Writer) {
	fmt.Fprintln(output, "Commands:")
	fmt.Fprintln(output, "  health")
	fmt.Fprintln(output, "  reset [json]")
	fmt.Fprintln(output, "  step [json-action]")
	fmt.Fprintln(output, "  state")
	fmt.Fprintln(output, "  metadata")
	fmt.Fprintln(output, "  schema")
	fmt.Fprintln(output, "  exit")
}

func WaitForHealth(baseUrl string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(strings.TrimRight(baseUrl, "/") + "/health") //nolint:gosec // Local user-selected endpoint.
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			lastErr = fmt.Errorf("health returned HTTP %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(time.Second)
	}
	return &azdext.LocalError{
		Message:    fmt.Sprintf("Environment runtime endpoint did not become healthy at %s: %v", baseUrl, lastErr),
		Code:       "rle_local_container_not_ready",
		Category:   azdext.LocalErrorCategoryUser,
		Suggestion: "Check the local container logs or remote sandbox status, then retry.",
	}
}

func call(baseUrl string, operation string, flags *callOptions) (string, error) {
	method := http.MethodGet
	var body io.Reader
	if operation == "reset" || operation == "step" {
		method = http.MethodPost
		requestBody, err := requestBody(operation, flags)
		if err != nil {
			return "", err
		}
		body = bytes.NewReader(requestBody)
	}

	url := strings.TrimRight(baseUrl, "/") + "/" + operation
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := HTTPClient(flags.timeout)
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("call environment runtime %s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &azdext.LocalError{
			Message:  fmt.Sprintf("Environment runtime endpoint returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data))),
			Code:     "rle_open_env_call_failed",
			Category: azdext.LocalErrorCategoryUser,
		}
	}

	return prettyJson(data), nil
}

func HTTPClient(timeoutSeconds int) *http.Client {
	if timeoutSeconds <= 0 {
		return &http.Client{}
	}
	return &http.Client{Timeout: time.Duration(timeoutSeconds) * time.Second}
}

func requestBody(operation string, flags *callOptions) ([]byte, error) {
	if strings.TrimSpace(flags.body) != "" {
		return validateJsonObject(flags.body, "body")
	}
	if operation == "reset" {
		return []byte("{}"), nil
	}

	action, err := validateJsonObject(flags.action, "action")
	if err != nil {
		return nil, err
	}
	var actionValue any
	if err := json.Unmarshal(action, &actionValue); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"action": actionValue})
}

func validateJsonObject(value string, flagName string) ([]byte, error) {
	var decoded map[string]any
	data := []byte(value)
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, &azdext.LocalError{
			Message:    fmt.Sprintf("--%s must be valid JSON object: %v", flagName, err),
			Code:       "rle_invalid_json",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: jsonFlagSuggestion(flagName),
		}
	}
	return data, nil
}

func jsonFlagSuggestion(flagName string) string {
	if flagName == "body" {
		return "Use reset {\"seed\":0} or another JSON object."
	}
	return "Use step {\"message\":\"hello\"} or another JSON object."
}

func prettyJson(data []byte) string {
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return string(data)
	}
	formatted, err := json.MarshalIndent(decoded, "", "  ")
	if err != nil {
		return string(data)
	}
	return string(formatted)
}
