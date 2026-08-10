// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/version"

	"github.com/spf13/cobra"
)

const delegatedSchemaVersion = 1

const (
	projectInitSourceAgents   = "azure.ai.agents/init"
	projectInitSourceProjects = "azure.ai.projects/direct"
)

type delegatedProject struct {
	ResourceID string `json:"resourceId,omitempty"`
	Endpoint   string `json:"endpoint,omitempty"`
}

type delegatedInfra struct {
	EjectProvider string `json:"ejectProvider,omitempty"`
}

type delegatedRequirements struct {
	AllowedLocations []string `json:"allowedLocations,omitempty"`
}

// projectInitRequest is the versioned IPC contract for agents.
type projectInitRequest struct {
	SchemaVersion       int                   `json:"schemaVersion"`
	Source              string                `json:"source"`
	SourceVersion       string                `json:"sourceVersion,omitempty"`
	Project             delegatedProject      `json:"project"`
	Infra               delegatedInfra        `json:"infra,omitempty"`
	Requirements        delegatedRequirements `json:"requirements,omitempty"`
	ResolveAzureContext bool                  `json:"resolveAzureContext"`
	Force               bool                  `json:"force"`
}

type delegatedModel struct {
	Name                 string   `json:"name"`
	DeploymentName       string   `json:"deploymentName,omitempty"`
	RequiredCapabilities []string `json:"requiredCapabilities,omitempty"`
	AllowedLocations     []string `json:"allowedLocations,omitempty"`
	ExcludedModelNames   []string `json:"excludedModelNames,omitempty"`
}

// projectDeploymentAddRequest is the managed-model IPC contract.
type projectDeploymentAddRequest struct {
	SchemaVersion int            `json:"schemaVersion"`
	Source        string         `json:"source"`
	SourceVersion string         `json:"sourceVersion,omitempty"`
	Model         delegatedModel `json:"model"`
	SetAsDefault  bool           `json:"setAsDefault"`
	Force         bool           `json:"force"`
}

type deploymentAddRequest = projectDeploymentAddRequest

type projectInitResult struct {
	SchemaVersion   int    `json:"schemaVersion"`
	ProducerVersion string `json:"producerVersion"`
	ServiceName     string `json:"serviceName"`
	Mode            string `json:"mode"`
	Mutation        string `json:"mutation"`
	Endpoint        string `json:"endpoint,omitempty"`
	ResourceID      string `json:"resourceId,omitempty"`
}

type projectDeploymentAddResult struct {
	SchemaVersion   int                  `json:"schemaVersion"`
	ProducerVersion string               `json:"producerVersion"`
	ServiceName     string               `json:"serviceName"`
	DeploymentName  string               `json:"deploymentName"`
	Model           delegatedResultModel `json:"model"`
	SKU             delegatedResultSKU   `json:"sku"`
	Mutation        string               `json:"mutation"`
}

type deploymentAddResult = projectDeploymentAddResult

type delegatedResultModel struct {
	Format  string `json:"format"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

type delegatedResultSKU struct {
	Name     string `json:"name"`
	Capacity int    `json:"capacity"`
}

func (r *projectInitRequest) validate() error {
	if r == nil {
		return contractValidationError("delegated project init request is empty")
	}
	if r.SchemaVersion != delegatedSchemaVersion {
		return contractCompatibilityError(r.SchemaVersion)
	}
	if r.Source != projectInitSourceAgents && r.Source != projectInitSourceProjects {
		return contractValidationError("source must be azure.ai.agents/init or azure.ai.projects/direct")
	}
	if r.Source == projectInitSourceAgents && strings.TrimSpace(r.SourceVersion) == "" {
		return contractValidationError("sourceVersion is required for delegated requests")
	}
	if r.Project.ResourceID != "" && r.Project.Endpoint != "" {
		return contractValidationError("project.resourceId and project.endpoint are mutually exclusive")
	}
	if r.Infra.EjectProvider != "" {
		if _, err := parseInfraProvider(r.Infra.EjectProvider); err != nil {
			return err
		}
	}
	locations, err := normalizeLocations(r.Requirements.AllowedLocations)
	if err != nil {
		return err
	}
	if r.Requirements.AllowedLocations != nil && len(locations) == 0 {
		return contractValidationError("requirements.allowedLocations must contain a location")
	}
	r.Requirements.AllowedLocations = locations
	return nil
}

func validateProjectInitRequest(request projectInitRequest) error {
	return request.validate()
}

func (r *projectDeploymentAddRequest) validate() error {
	if r == nil {
		return contractValidationError("delegated deployment request is empty")
	}
	if r.SchemaVersion != delegatedSchemaVersion {
		return contractCompatibilityError(r.SchemaVersion)
	}
	if r.Source != projectInitSourceAgents && r.Source != projectInitSourceProjects {
		return contractValidationError("source must be azure.ai.agents/init or azure.ai.projects/direct")
	}
	if r.Source == projectInitSourceAgents && strings.TrimSpace(r.SourceVersion) == "" {
		return contractValidationError("sourceVersion is required for delegated requests")
	}
	if strings.TrimSpace(r.Model.Name) == "" {
		return contractValidationError("model.name is required")
	}
	if strings.TrimSpace(r.Model.DeploymentName) == "" && r.Model.DeploymentName != "" {
		return contractValidationError("model.deploymentName must not be whitespace")
	}
	locations, err := normalizeLocations(r.Model.AllowedLocations)
	if err != nil {
		return err
	}
	r.Model.AllowedLocations = locations
	for _, capability := range r.Model.RequiredCapabilities {
		if capability != "agentsV2" {
			return contractValidationError(fmt.Sprintf("unknown required capability %q", capability))
		}
	}
	r.Model.RequiredCapabilities = uniqueStrings(r.Model.RequiredCapabilities, false)
	r.Model.ExcludedModelNames = uniqueStrings(r.Model.ExcludedModelNames, true)
	return nil
}

func validateProjectDeploymentAddRequest(request projectDeploymentAddRequest) error {
	return request.validate()
}

func validateProjectInitResult(result projectInitResult) error {
	if result.SchemaVersion != delegatedSchemaVersion {
		return contractCompatibilityError(result.SchemaVersion)
	}
	if result.ProducerVersion == "" {
		return contractValidationError("producerVersion is required in delegated results")
	}
	if result.ServiceName == "" {
		return contractValidationError("serviceName is required")
	}
	if !slices.Contains([]string{"new", "existing-id", "existing-endpoint"}, result.Mode) {
		return contractValidationError(fmt.Sprintf("invalid project mode %q", result.Mode))
	}
	if !slices.Contains([]string{"created", "updated", "migrated", "unchanged"}, result.Mutation) {
		return contractValidationError(fmt.Sprintf("invalid project mutation %q", result.Mutation))
	}
	if result.Mode == "existing-id" && result.ResourceID == "" {
		return contractValidationError("resourceId is required for existing-id results")
	}
	if result.Mode != "new" && result.Endpoint == "" {
		return contractValidationError("endpoint is required for existing project results")
	}
	return nil
}

func validateProjectDeploymentAddResult(result projectDeploymentAddResult) error {
	if result.SchemaVersion != delegatedSchemaVersion {
		return contractCompatibilityError(result.SchemaVersion)
	}
	if result.ProducerVersion == "" || result.ServiceName == "" ||
		result.DeploymentName == "" || result.Model.Name == "" ||
		result.Model.Format == "" || result.Model.Version == "" ||
		result.SKU.Name == "" || result.SKU.Capacity <= 0 {
		return contractValidationError("deployment result is missing a required value")
	}
	if !slices.Contains([]string{"created", "replaced", "unchanged"}, result.Mutation) {
		return contractValidationError(fmt.Sprintf("invalid deployment mutation %q", result.Mutation))
	}
	return nil
}

func contractCompatibilityError(got int) error {
	return exterrors.Validation(
		"project_contract_incompatible",
		fmt.Sprintf(
			"unsupported delegated contract schemaVersion %d (projects extension supports %d)",
			got, delegatedSchemaVersion,
		),
		"upgrade azure.ai.agents and azure.ai.projects to compatible versions",
	)
}

func contractValidationError(message string) error {
	return exterrors.Validation("project_contract_invalid", message, "check the delegated request fields")
}

func normalizeLocations(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, contractValidationError("allowedLocations cannot contain an empty location")
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out, nil
}

func uniqueStrings(values []string, insensitive bool) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		key := value
		if insensitive {
			key = strings.ToLower(key)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func decodeDelegatedJSON(path string, value any) error {
	if err := validateDelegatedFilePath(path, "request", true); err != nil {
		return err
	}
	file, err := os.Open(path) // #nosec G304 -- path is validated as a delegated file.
	if err != nil {
		return contractValidationError(fmt.Sprintf("read delegated request: %v", err))
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return contractValidationError(fmt.Sprintf("decode delegated request: %v", err))
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return contractValidationError("delegated request must contain exactly one JSON document")
	}
	return nil
}

func decodeDelegatedResultJSON(path string, value any) error {
	if err := validateDelegatedFilePath(path, "result", true); err != nil {
		return err
	}
	file, err := os.Open(path) // #nosec G304 -- path is validated as a delegated file.
	if err != nil {
		return contractValidationError(fmt.Sprintf("read delegated result: %v", err))
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(value); err != nil {
		return contractValidationError(fmt.Sprintf("decode delegated result: %v", err))
	}
	return nil
}

func writeDelegatedResult(path string, value any) error {
	if err := validateDelegatedFilePath(path, "result", false); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".azd-project-result-*")
	if err != nil {
		return fmt.Errorf("create delegated result: %w", err)
	}
	tempName := temp.Name()
	cleanup := func() {
		_ = temp.Close()
		_ = os.Remove(tempName)
	}
	defer cleanup()
	if err := temp.Chmod(0600); err != nil {
		return fmt.Errorf("protect delegated result: %w", err)
	}
	encoder := json.NewEncoder(temp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("encode delegated result: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("flush delegated result: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close delegated result: %w", err)
	}
	if err := os.Rename(tempName, path); err != nil {
		// Windows does not replace an existing file with Rename. The path was
		// validated above and is still in the same private directory.
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("replace delegated result: %w", err)
		}
		if renameErr := os.Rename(tempName, path); renameErr != nil {
			return fmt.Errorf("replace delegated result: %w", renameErr)
		}
	}
	return nil
}

func validateDelegatedFilePath(path, kind string, requireRegular bool) error {
	if path == "" {
		return contractValidationError(kind + " file path is required")
	}
	if !filepath.IsAbs(path) {
		return contractValidationError(kind + " file path must be absolute")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return contractValidationError(kind + " file path must be absolute")
	}
	if err := rejectSymlinkComponents(abs); err != nil {
		return contractValidationError(fmt.Sprintf("%s file path is unsafe: %v", kind, err))
	}
	info, statErr := os.Lstat(abs)
	if statErr != nil {
		if !requireRegular && os.IsNotExist(statErr) {
			return nil
		}
		return contractValidationError(fmt.Sprintf("%s file is not accessible: %v", kind, statErr))
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return contractValidationError(kind + " file must be a regular non-symlink file")
	}
	return nil
}

func validateDelegatedPathPair(requestPath, resultPath string) error {
	if err := validateDelegatedFilePath(requestPath, "request", true); err != nil {
		return err
	}
	if err := validateDelegatedFilePath(resultPath, "result", false); err != nil {
		return err
	}
	requestAbs, _ := filepath.Abs(requestPath)
	resultAbs, _ := filepath.Abs(resultPath)
	if !strings.EqualFold(filepath.Dir(requestAbs), filepath.Dir(resultAbs)) {
		return contractValidationError("request and result files must be siblings in the delegated temporary directory")
	}
	return nil
}

func rejectSymlinkComponents(path string) error {
	clean := filepath.Clean(path)
	volume := filepath.VolumeName(clean)
	rest := strings.TrimPrefix(clean, volume)
	current := volume + string(filepath.Separator)
	for _, part := range strings.FieldsFunc(rest, func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path component %q is a symbolic link", current)
		}
	}
	return nil
}

func delegatedProducerVersion() string {
	return version.Version
}

func registerDelegatedContractFlags(
	cmd *cobra.Command,
	requestFile *string,
	resultFile *string,
) {
	cmd.Flags().StringVar(requestFile, "request-file", "", "Delegated request file")
	cmd.Flags().StringVar(resultFile, "result-file", "", "Delegated result file")
	_ = cmd.Flags().MarkHidden("request-file")
	_ = cmd.Flags().MarkHidden("result-file")
}
