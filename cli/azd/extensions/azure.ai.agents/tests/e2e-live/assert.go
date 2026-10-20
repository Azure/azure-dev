// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package e2elive contains the Tier 2 live golden-path end-to-end test for the
// azure.ai.agents extension: init -> provision -> deploy -> invoke -> down,
// driven against real Azure resources. See README.md for setup and how to run.
package e2elive

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// spelledFourRe matches the spelled-out word "four" as a standalone word
// (case-insensitive), e.g. "the answer is four".
var spelledFourRe = regexp.MustCompile(`(?i)\bfour\b`)

// agentLineRe matches the start of an agent reply line, which invoke prints as
// "[<agentName>] <text>" (invoke.go printf "[%s] %s"). responseEndRe matches the
// timing footer. Accept the previous footer too when testing older releases.
var (
	agentLineRe   = regexp.MustCompile(`(?m)^\[[^\]]+\] `)
	responseEndRe = regexp.MustCompile(`(?m)^(?:Client elapsed:|Server responded in)`)
)

// agentResponseRegion returns just the agent's printed answer, sliced from the
// first "[<agent>] " line to the client timing footer. Scoping the
// 2+2 check to this region keeps stray "4"s from the rest of the CLI output
// (model names, versions, status codes) from passing the test. If either marker
// is missing the format changed, so it returns the full text and lets the
// standalone-digit rules below guard against false positives.
func agentResponseRegion(out string) string {
	start := agentLineRe.FindStringIndex(out)
	if start == nil {
		return out
	}
	rest := out[start[0]:]
	if end := responseEndRe.FindStringIndex(rest); end != nil {
		return rest[:end[0]]
	}
	return out
}

// responseHasExpectedAnswer reports whether text answers "what is 2+2?" with a
// standalone "4" or the spelled-out word "four".
//
// A live model may answer either, and the captured CLI output also contains
// unrelated digits — model names ("gpt-4o-mini"), versions ("4.1"), or status
// codes ("404") — so a bare substring search would produce false positives.
// The "4" must therefore stand alone: not part of a larger word or number.
// The standalone-"4" rule is the lookaround (?<![\w.])4(?!\.\d)(?!\w); the
// spelled-out "four" is matched case-insensitively as a whole word.
//
// A decimal such as "4.0" is deliberately rejected too: although 4.0 == 4
// mathematically, the "4.<digit>" form is treated as a version/decimal token to
// keep "4.1"-style strings out, and a live model answering "2+2" replies "4" or
// "four", never "4.0".
//
// Go's regexp engine (RE2) has no lookahead/lookbehind, so the standalone-"4"
// rule is implemented by scanning runes instead of with a single expression.
func responseHasExpectedAnswer(text string) bool {
	if spelledFourRe.MatchString(text) {
		return true
	}
	return hasStandaloneFour(text)
}

// hasStandaloneFour reports whether text contains a "4" digit that stands alone,
// reproducing the lookaround in the Python regex (?<![\w.])4(?!\.\d)(?!\w):
//   - not preceded by a word rune or '.'  (rejects "x4", "_4", ".4")
//   - not followed by '.' then a digit    (rejects "4.1", "4.0")
//   - not followed by a word rune         (rejects "40", "4o")
func hasStandaloneFour(text string) bool {
	runes := []rune(text)
	for i, r := range runes {
		if r != '4' {
			continue
		}
		if i > 0 {
			if prev := runes[i-1]; prev == '.' || isWordRune(prev) {
				continue
			}
		}
		if i+2 < len(runes) && runes[i+1] == '.' && unicode.IsDigit(runes[i+2]) {
			continue
		}
		if i+1 < len(runes) && isWordRune(runes[i+1]) {
			continue
		}
		return true
	}
	return false
}

// isWordRune reports whether r is a word character, matching the Python regex
// \w class (Unicode letters, digits, and underscore).
func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func snapshotDependencyCommands() [][]string {
	return [][]string{
		{"add", "--no-sync", "agent-framework-foundry-hosting==1.0.0b260821",
			"azure-ai-agentserver-core==2.3.0b1", "azure-ai-agentserver-responses==2.4.0b1"},
		{"export", "--locked", "--no-dev", "--no-emit-project", "--format", "requirements-txt",
			"--output-file", "requirements.txt"},
	}
}

func snapshotBuildDockerfile(data []byte) ([]byte, error) {
	const install = "RUN uv sync --frozen --no-dev --no-install-project"
	const base = "mcr.microsoft.com/devcontainers/python@sha256:" +
		"2858fb53653b285965a9f808b0e8942ed46f13356a07433380aaa2d83c34ac4b"
	const mountInstruction = "RUN --mount=type=secret,id=python_index,required=true "
	dockerfile := string(data)
	for _, replacement := range [][2]string{
		{"FROM ghcr.io/astral-sh/uv:0.11.7 AS uv", ""},
		{"FROM python:3.13-slim", "FROM " + base},
		{"COPY --from=uv /uv /uvx /bin/", mountInstruction + "PIP_INDEX_URL=\"$(cat /run/secrets/python_index)\" " +
			"python -m pip install --no-cache-dir uv==0.11.7"},
		{install, mountInstruction + "UV_NO_CACHE=1 UV_DEFAULT_INDEX=\"$(cat /run/secrets/python_index)\" " +
			"uv sync --frozen --no-dev --no-install-project"},
	} {
		if strings.Count(dockerfile, replacement[0]) != 1 {
			return nil, errors.New("snapshot baseline requires the supported Dockerfile build instructions")
		}
		dockerfile = strings.Replace(dockerfile, replacement[0], replacement[1], 1)
	}
	return []byte(dockerfile), nil
}

func snapshotBuildProject(manifest []byte) (string, error) {
	var config struct {
		Services map[string]struct {
			Host     string `yaml:"host"`
			Kind     string `yaml:"kind"`
			Language string `yaml:"language"`
			Project  string `yaml:"project"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(manifest, &config); err != nil {
		return "", fmt.Errorf("parse snapshot build manifest: %w", err)
	}
	var project string
	for _, service := range config.Services {
		if service.Host != "azure.ai.agent" {
			continue
		}
		if project != "" {
			return "", errors.New("snapshot image build requires exactly one agent service")
		}
		if service.Kind != "hosted" || (service.Language != "python" && service.Language != "docker") {
			return "", errors.New("snapshot image build requires a hosted Python agent sample")
		}
		if !filepath.IsLocal(service.Project) {
			return "", errors.New("snapshot build project must be a relative directory within the isolated sample")
		}
		project = service.Project
	}
	if project == "" {
		return "", errors.New("no Python agent source project found for snapshot image build")
	}
	return project, nil
}

func updateSnapshotSampleManifest(root *os.Root, image string) error {
	manifest, err := root.ReadFile("azure.yaml")
	if err != nil {
		return err
	}
	manifest, err = enableSampleMemorySnapshot(manifest, image)
	if err != nil {
		return err
	}
	return root.WriteFile("azure.yaml", manifest, 0o600)
}

func enableSampleMemorySnapshot(manifest []byte, image string) ([]byte, error) {
	if err := validateSnapshotImageReference(image); err != nil {
		return nil, err
	}
	var config map[string]any
	if err := yaml.Unmarshal(manifest, &config); err != nil {
		return nil, fmt.Errorf("parse sample manifest: %w", err)
	}
	services, ok := config["services"].(map[string]any)
	if !ok {
		return nil, errors.New("sample manifest must contain services")
	}
	var agentService map[string]any
	for _, value := range services {
		service, ok := value.(map[string]any)
		if !ok || service["host"] != "azure.ai.agent" {
			continue
		}
		if agentService != nil {
			return nil, errors.New("snapshot sample validation requires exactly one agent service")
		}
		if kind, exists := service["kind"]; exists && kind != "hosted" {
			return nil, errors.New("snapshot sample validation requires a hosted agent")
		}
		agentService = service
	}
	if agentService == nil {
		return nil, errors.New("sample manifest has no azure.ai.agent service")
	}
	dockerOptions := map[string]any{}
	if value, exists := agentService["docker"]; exists {
		var ok bool
		dockerOptions, ok = value.(map[string]any)
		if !ok {
			return nil, errors.New("sample docker options must be a mapping")
		}
	}
	dockerOptions["imagePassthrough"] = true
	dockerOptions["remoteBuild"] = false
	agentService["docker"] = dockerOptions
	agentService["image"] = image
	agentService["enableMemorySnapshot"] = true
	return yaml.Marshal(config)
}

func validateSnapshotAgentVersion(data []byte, expectedImage string) error {
	var version struct {
		Status     string `json:"status"`
		Definition struct {
			Kind                   string `json:"kind"`
			EnableMemorySnapshot   *bool  `json:"enable_memory_snapshot"`
			ContainerConfiguration struct {
				Image string `json:"image"`
			} `json:"container_configuration"`
		} `json:"definition"`
	}
	if err := json.Unmarshal(data, &version); err != nil {
		return fmt.Errorf("parse deployed agent version: %w", err)
	}
	if version.Status != "active" {
		return fmt.Errorf("snapshot agent version is not active: %q", version.Status)
	}
	if version.Definition.Kind != "hosted" || version.Definition.EnableMemorySnapshot == nil ||
		!(*version.Definition.EnableMemorySnapshot) {
		return errors.New("service did not confirm definition.enable_memory_snapshot=true for the deployed hosted agent")
	}
	image := version.Definition.ContainerConfiguration.Image
	if err := validateSnapshotImageReference(image); err != nil {
		return err
	}
	if image != expectedImage {
		return errors.New("deployed snapshot image does not match the qualified input digest")
	}
	return nil
}

func validateSnapshotImageReference(image string) error {
	name, digest, found := strings.Cut(image, "@sha256:")
	if !found || len(digest) != 64 || strings.ContainsAny(image, " \t\r\n") || strings.Contains(name, "://") {
		return errors.New("snapshot validation requires a fully qualified container image pinned with @sha256:<digest>")
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return errors.New("snapshot image digest must contain 64 hexadecimal characters")
	}
	parsed, err := url.Parse("https://" + name)
	if err != nil || parsed.User != nil || parsed.Hostname() == "" || len(parsed.Path) < 2 ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("snapshot image must be a fully qualified registry/repository reference without credentials")
	}
	return nil
}

func snapshotImagePython(inspection []byte) (string, error) {
	var images []struct {
		Config struct {
			Entrypoint []string `json:"Entrypoint"`
			Cmd        []string `json:"Cmd"`
		} `json:"Config"`
	}
	if err := json.Unmarshal(inspection, &images); err != nil {
		return "", fmt.Errorf("parse snapshot image interpreter: %w", err)
	}
	if len(images) != 1 {
		return "", errors.New("expected exactly one snapshot image")
	}
	pythonName := regexp.MustCompile(`^python(?:3(?:\.[0-9]+)?)?$`)
	for _, command := range [][]string{images[0].Config.Entrypoint, images[0].Config.Cmd} {
		if len(command) > 0 && pythonName.MatchString(path.Base(command[0])) {
			return command[0], nil
		}
	}
	return "python", nil
}

func snapshotRegistryLoginArgs(registry, subscription string) []string {
	args := []string{"acr", "login", "--name", registry}
	if subscription != "" {
		args = append(args, "--subscription", subscription)
	}
	return args
}

func snapshotImageDependencyCheckArgs(image, python string, hasPip bool) []string {
	checker, checkerArgs := python, []string{"-m", "pip", "check"}
	if !hasPip {
		checker, checkerArgs = "uv", []string{"pip", "check", "--python", python}
	}
	args := []string{"run", "--rm", "--network", "none", "--read-only",
		"--tmpfs", "/tmp:rw,nosuid,nodev,noexec,size=64m", "--env", "UV_NO_CACHE=1",
		"--entrypoint", checker, image}
	return append(args, checkerArgs...)
}

func validateSnapshotImageArchitecture(inspection []byte) error {
	var images []struct {
		OS           string `json:"Os"`
		Architecture string `json:"Architecture"`
	}
	if err := json.Unmarshal(inspection, &images); err != nil {
		return fmt.Errorf("parse snapshot image inspection: %w", err)
	}
	if len(images) != 1 || images[0].OS != "linux" || images[0].Architecture != "amd64" {
		return errors.New("snapshot qualification requires a Linux AMD64 container image")
	}
	return nil
}

func validateSnapshotImageRuntime(inspection, packages []byte) error {
	if err := validateSnapshotImageArchitecture(inspection); err != nil {
		return err
	}
	var versions map[string]string
	if err := json.Unmarshal(packages, &versions); err != nil {
		return fmt.Errorf("parse snapshot runtime package versions: %w", err)
	}
	for _, requirement := range []struct{ name, version string }{
		{"azure-ai-agentserver-core", "2.3.0b1"},
		{"azure-ai-agentserver-responses", "2.4.0b1"},
	} {
		if versions[requirement.name] != requirement.version {
			return fmt.Errorf("snapshot image requires %s==%s; found %q",
				requirement.name, requirement.version, versions[requirement.name])
		}
	}
	return nil
}

func snapshotResponsesRequest(sessionID string) map[string]any {
	return map[string]any{
		"input":            "Hello, what is 2+2?",
		"store":            false,
		"stream":           false,
		"agent_session_id": sessionID,
	}
}

func validateSnapshotResponse(data []byte) error {
	var response struct {
		Status string `json:"status"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return fmt.Errorf("parse stateless Responses result: %w", err)
	}
	if response.Status != "completed" {
		return fmt.Errorf("stateless Responses request did not complete: %q", response.Status)
	}
	var answer strings.Builder
	for _, output := range response.Output {
		if output.Type != "message" {
			continue
		}
		for _, content := range output.Content {
			if content.Type == "output_text" {
				answer.WriteString(content.Text)
				answer.WriteByte('\n')
			}
		}
	}
	if !responseHasExpectedAnswer(answer.String()) {
		return errors.New("stateless Responses output does not contain the expected answer")
	}
	return nil
}
