// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	azdexec "github.com/azure/azure-dev/cli/azd/pkg/exec"
)

const artifactAcrAPIVersion = "2025-03-01-preview"
const artifactRepositoryComponent = `[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*`

var artifactImagePattern = regexp.MustCompile(
	`^(?i:[a-z0-9]{3,50}\.azurecr\.io)/` + artifactRepositoryComponent + `(?:/` +
		artifactRepositoryComponent + `)*(?::[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}|@sha256:[a-f0-9]{64})$`,
)
var artifactDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var artifactRegistryIDPattern = regexp.MustCompile(
	`(?i)^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\.ContainerRegistry/registries/[a-z0-9]{3,50}$`,
)
var artifactURLPattern = regexp.MustCompile(`https?://[^\s"'<>]+`)
var artifactRunIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func validArtifactImage(image string) bool {
	return artifactImagePattern.MatchString(image)
}

func redactArtifactURLs(message string) string {
	return artifactURLPattern.ReplaceAllString(message, "[redacted URL]")
}

// PinnedTaskArtifactImage resolves a verified artifact's tag to its immutable registry digest.
func PinnedTaskArtifactImage(task TaskArtifact) (string, error) {
	if task.CLI == nil || task.CLI.ImagePath != task.AcrImagePath ||
		!artifactDigestPattern.MatchString(task.CLI.ImageDigest) || !validArtifactImage(task.AcrImagePath) {
		return "", fmt.Errorf("task %q has no verified image; prepare the task manifest first", task.TaskName)
	}
	repository, _, _ := strings.Cut(task.AcrImagePath, "@")
	host, name, _ := strings.Cut(repository, "/")
	name, _, _ = strings.Cut(name, ":")
	return host + "/" + name + "@" + task.CLI.ImageDigest, nil
}

// ACRTaskArtifactClient uses Azure CLI authentication for ACR management and registry reads.
// Source archives are uploaded directly to private storage; SAS URLs are never persisted or logged.
type ACRTaskArtifactClient struct{}

func artifactAzureJSON(ctx context.Context, result any, args ...string) error {
	var stdout, stderr bytes.Buffer
	args = append(args, "--output", "json", "--only-show-errors")
	_, err := runAcrBuild(ctx, azdexec.NewRunArgs("az", args...).WithStdOut(&stdout).WithStdErr(&stderr))
	if err != nil {
		return fmt.Errorf("Azure CLI %s failed: %s: %w", args[0], redactArtifactURLs(stderr.String()), err)
	}
	if err := json.Unmarshal(stdout.Bytes(), result); err != nil {
		// Do not embed response bytes: upload-location responses contain SAS credentials.
		return fmt.Errorf("Azure CLI returned an invalid JSON response: %w", err)
	}
	return nil
}

func artifactRegistryURL(id, operation string) (string, error) {
	if !artifactRegistryIDPattern.MatchString(id) || strings.ContainsAny(id, "?#") {
		return "", fmt.Errorf("invalid ACR registry resource ID")
	}
	return "https://management.azure.com" + id + "/" + operation + "?api-version=" + artifactAcrAPIVersion, nil
}

// Registry resolves a build registry using the Azure CLI's selected subscription.
func (ACRTaskArtifactClient) Registry(ctx context.Context, host string) (string, error) {
	var response struct {
		ID          string `json:"id"`
		LoginServer string `json:"loginServer"`
	}
	name := strings.TrimSuffix(strings.ToLower(host), ".azurecr.io")
	if err := artifactAzureJSON(ctx, &response, "acr", "show", "--name", name); err != nil {
		return "", err
	}
	if !strings.EqualFold(response.LoginServer, host) || !artifactRegistryIDPattern.MatchString(response.ID) {
		return "", fmt.Errorf("Azure CLI resolved a different or invalid ACR registry for %q", host)
	}
	return response.ID, nil
}

// Upload sends the original compressed bytes to ACR's private one-shot build-source location.
func (ACRTaskArtifactClient) Upload(ctx context.Context, registryID string, raw []byte) (string, error) {
	endpoint, err := artifactRegistryURL(registryID, "listBuildSourceUploadUrl")
	if err != nil {
		return "", err
	}
	var location struct {
		UploadURL    string `json:"uploadUrl"`
		RelativePath string `json:"relativePath"`
	}
	if err := artifactAzureJSON(ctx, &location, "rest", "--method", "post", "--url", endpoint); err != nil {
		return "", err
	}
	target, err := url.Parse(location.UploadURL)
	if err != nil || target.Scheme != "https" || target.Host == "" || target.User != nil ||
		location.RelativePath == "" || strings.ContainsAny(location.RelativePath, "?#") ||
		strings.Contains(location.RelativePath, "://") {
		return "", fmt.Errorf("ACR returned an invalid private build-source location")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, target.String(), bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("could not create private build-source upload request")
	}
	req.Header.Set("x-ms-blob-type", "BlockBlob")
	req.Header.Set("x-ms-version", "2021-08-06")
	req.Header.Set("Content-Type", "application/gzip")
	client := &http.Client{
		Timeout: 2 * time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		// url.Error includes the SAS URL; preserve the underlying cause without its credential-bearing wrapper.
		if urlErr, ok := errors.AsType[*url.Error](err); ok {
			err = urlErr.Err
		}
		return "", fmt.Errorf("upload private ACR source: %w", err)
	}
	defer resp.Body.Close()
	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, 4096)); err != nil {
		return "", fmt.Errorf("read ACR source upload response: %w", err)
	}
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("private ACR source upload returned HTTP %d", resp.StatusCode)
	}
	return location.RelativePath, nil
}

func artifactTaskYAML(image string) (string, error) {
	if !validArtifactImage(image) || strings.Contains(image, "@") {
		return "", fmt.Errorf("ACR builds require a tagged image reference")
	}
	return "version: v1.1.0\nstepTimeout: 900\nsteps:\n" +
		"  - id: build\n    build: -t " + image + " -f Dockerfile .\n" +
		"    workingDirectory: /workspace/environment\n" +
		"  - id: push\n    push:\n      - " + image + "\n", nil
}

// Schedule submits exactly one encoded build task. The caller journals uncertainty before retrying.
func (ACRTaskArtifactClient) Schedule(ctx context.Context, registryID, source, image string) (string, error) {
	endpoint, err := artifactRegistryURL(registryID, "scheduleRun")
	if err != nil {
		return "", err
	}
	yaml, err := artifactTaskYAML(image)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(map[string]any{
		"type": "EncodedTaskRunRequest", "encodedTaskContent": base64.StdEncoding.EncodeToString([]byte(yaml)),
		"sourceLocation": source, "platform": map[string]string{"os": "Linux", "architecture": "amd64"},
		"credentials": map[string]any{"sourceRegistry": map[string]string{"loginMode": "Default"}}, "timeout": 900,
	})
	if err != nil {
		return "", err
	}
	var run struct {
		Properties struct {
			RunID string `json:"runId"`
		} `json:"properties"`
	}
	if err := artifactAzureJSON(ctx, &run, "rest", "--method", "post", "--url", endpoint,
		"--body", string(body)); err != nil {
		return "", err
	}
	if !artifactRunIDPattern.MatchString(run.Properties.RunID) {
		return "", fmt.Errorf("ACR returned no valid run ID; reconcile registry runs before retrying")
	}
	return run.Properties.RunID, nil
}

// Run reads a recorded run rather than scheduling another build.
func (ACRTaskArtifactClient) Run(ctx context.Context, registryID, runID string) (ArtifactRun, error) {
	if !artifactRunIDPattern.MatchString(runID) {
		return ArtifactRun{}, fmt.Errorf("invalid recorded ACR run ID")
	}
	endpoint, err := artifactRegistryURL(registryID, "runs/"+runID)
	if err != nil {
		return ArtifactRun{}, err
	}
	var response struct {
		Properties struct {
			Status string `json:"status"`
		} `json:"properties"`
	}
	if err := artifactAzureJSON(ctx, &response, "rest", "--method", "get", "--url", endpoint); err != nil {
		return ArtifactRun{}, err
	}
	if response.Properties.Status == "" {
		return ArtifactRun{}, fmt.Errorf("ACR returned no run status")
	}
	return ArtifactRun{Status: response.Properties.Status}, nil
}

// Digest verifies an externally supplied or freshly built image without rebuilding it.
func (ACRTaskArtifactClient) Digest(ctx context.Context, image string) (string, error) {
	if !validArtifactImage(image) {
		return "", fmt.Errorf("invalid ACR task image reference")
	}
	host, name, _ := strings.Cut(image, "/")
	var result struct {
		Digest string `json:"digest"`
	}
	if err := artifactAzureJSON(ctx, &result, "acr", "manifest", "show-metadata",
		"--registry", strings.TrimSuffix(strings.ToLower(host), ".azurecr.io"), "--name", name); err != nil {
		return "", err
	}
	if !artifactDigestPattern.MatchString(result.Digest) {
		return "", fmt.Errorf("ACR returned an invalid image digest")
	}
	if _, pinned, ok := strings.Cut(image, "@"); ok && result.Digest != pinned {
		return "", fmt.Errorf("ACR returned a different image digest than requested")
	}
	return result.Digest, nil
}
