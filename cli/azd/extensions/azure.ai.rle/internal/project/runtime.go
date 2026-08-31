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
	"net/url"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

type callOptions struct {
	timeout int
	action  string
	body    string
}

type AuthorizationProvider func(context.Context) (string, error)

func RunShellWithContext(
	ctx context.Context,
	input io.Reader,
	output io.Writer,
	baseUrl string,
	timeout int,
) error {
	return RunShellWithContextAndAuthorizationProvider(ctx, input, output, baseUrl, timeout, nil)
}

// RunShellWithContextAndAuthorizationProvider refreshes authorization for each runtime request.
func RunShellWithContextAndAuthorizationProvider(
	ctx context.Context,
	input io.Reader,
	output io.Writer,
	baseUrl string,
	timeout int,
	authorizationProvider AuthorizationProvider,
) error {
	done := make(chan error, 1)
	go func() {
		done <- runShell(ctx, input, output, baseUrl, timeout, authorizationProvider)
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

func runShell(
	ctx context.Context,
	input io.Reader,
	output io.Writer,
	baseUrl string,
	timeout int,
	authorizationProvider AuthorizationProvider,
) error {
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

		response, err := call(ctx, baseUrl, operation, flags, authorizationProvider)
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
	return waitForHealth(
		context.Background(),
		baseUrl,
		timeout,
		nil,
		"rle_local_container_not_ready",
		"Check the local container logs, then retry.",
	)
}

// WaitForHealthWithAuthorizationProvider refreshes authorization for each health request.
func WaitForHealthWithAuthorizationProvider(
	ctx context.Context,
	baseUrl string,
	timeout time.Duration,
	authorizationProvider AuthorizationProvider,
) error {
	return waitForHealth(
		ctx,
		baseUrl,
		timeout,
		authorizationProvider,
		"rle_remote_runtime_not_ready",
		"Check the remote RLE instance status and OpenEnv service logs, then retry.",
	)
}

func waitForHealth(
	ctx context.Context,
	baseUrl string,
	timeout time.Duration,
	authorizationProvider AuthorizationProvider,
	errorCode string,
	suggestion string,
) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	client := HTTPClient(2)
	for time.Now().Before(deadline) {
		healthUrl, err := RuntimeOperationURL(baseUrl, "health")
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthUrl, nil)
		if err != nil {
			return err
		}
		if err := setAuthorization(req, authorizationProvider); err != nil {
			return fmt.Errorf("authenticate to environment runtime: %w", err)
		}
		resp, err := client.Do(req) //nolint:gosec // Caller validates remote URLs; local run uses a loopback URL.
		if err == nil {
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				return nil
			}
			detail := readHealthErrorDetail(resp.Body)
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("health returned HTTP %d%s", resp.StatusCode, detail)
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return &azdext.LocalError{
		Message:    fmt.Sprintf("Environment runtime endpoint did not become healthy at %s: %v", baseUrl, lastErr),
		Code:       errorCode,
		Category:   azdext.LocalErrorCategoryUser,
		Suggestion: suggestion,
	}
}

func RuntimeOperationURL(baseUrl string, operation string) (string, error) {
	runtimeUrl, err := url.Parse(baseUrl)
	if err != nil {
		return "", fmt.Errorf("parse environment runtime URL: %w", err)
	}
	runtimeUrl.Path = strings.TrimRight(runtimeUrl.Path, "/") + "/" + operation
	runtimeUrl.RawPath = ""
	return runtimeUrl.String(), nil
}

func readHealthErrorDetail(body io.Reader) string {
	const maxErrorDetailBytes = 4096

	data, err := io.ReadAll(io.LimitReader(body, maxErrorDetailBytes+1))
	if err != nil {
		return ""
	}
	truncated := len(data) > maxErrorDetailBytes
	if truncated {
		data = data[:maxErrorDetailBytes]
	}
	detail := strings.TrimSpace(string(data))
	if detail == "" {
		return ""
	}
	if truncated {
		detail += "..."
	}
	return ": " + detail
}

func call(
	ctx context.Context,
	baseUrl string,
	operation string,
	flags *callOptions,
	authorizationProvider AuthorizationProvider,
) (string, error) {
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

	requestUrl, err := RuntimeOperationURL(baseUrl, operation)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, method, requestUrl, body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	if err := setAuthorization(req, authorizationProvider); err != nil {
		return "", fmt.Errorf("authenticate to environment runtime: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := HTTPClient(flags.timeout)
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("call environment runtime %s %s: %w", method, requestUrl, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &azdext.LocalError{
			Message: fmt.Sprintf(
				"Environment runtime endpoint returned HTTP %d: %s",
				resp.StatusCode,
				strings.TrimSpace(string(data)),
			),
			Code:     "rle_open_env_call_failed",
			Category: azdext.LocalErrorCategoryUser,
		}
	}

	return prettyJson(data), nil
}

func setAuthorization(req *http.Request, authorizationProvider AuthorizationProvider) error {
	if authorizationProvider == nil {
		return nil
	}
	authorization, err := authorizationProvider(req.Context())
	if err != nil {
		return err
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	return nil
}

func HTTPClient(timeoutSeconds int) *http.Client {
	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	if timeoutSeconds <= 0 {
		return client
	}
	client.Timeout = time.Duration(timeoutSeconds) * time.Second
	return client
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
