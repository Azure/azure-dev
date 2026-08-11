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
	"strings"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/version"

	"github.com/spf13/cobra"
)

const delegatedSchemaVersion = 1

const (
	projectInitSourceAgents = "azure.ai.agents/init"
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

type projectInitOutput struct {
	SchemaVersion   int    `json:"schemaVersion"`
	ProducerVersion string `json:"producerVersion"`
	ServiceName     string `json:"serviceName"`
	Mode            string `json:"mode"`
	Mutation        string `json:"mutation"`
	Endpoint        string `json:"endpoint,omitempty"`
	ResourceID      string `json:"resourceId,omitempty"`
}

type projectDeploymentAddOutput struct {
	SchemaVersion   int                   `json:"schemaVersion"`
	ProducerVersion string                `json:"producerVersion"`
	ServiceName     string                `json:"serviceName"`
	DeploymentName  string                `json:"deploymentName"`
	Model           deploymentOutputModel `json:"model"`
	SKU             deploymentOutputSKU   `json:"sku"`
	Mutation        string                `json:"mutation"`
}

type deploymentOutputModel struct {
	Format  string `json:"format"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

type deploymentOutputSKU struct {
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
	if r.Source != projectInitSourceAgents {
		return contractValidationError("source must be azure.ai.agents/init")
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
	if r.Source != projectInitSourceAgents {
		return contractValidationError("source must be azure.ai.agents/init")
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

func contractCompatibilityError(got int) error {
	return exterrors.Compatibility(
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
	info, statErr := os.Lstat(abs)
	if statErr != nil {
		if !requireRegular && os.IsNotExist(statErr) {
			return nil
		}
		return contractValidationError(fmt.Sprintf("%s file is not accessible: %v", kind, statErr))
	}
	// Parent directories may use OS-provided aliases such as macOS /var.
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return contractValidationError(kind + " file must be a regular non-symlink file")
	}
	return nil
}

func delegatedProducerVersion() string {
	return version.Version
}

func registerDelegatedContractFlags(
	cmd *cobra.Command,
	requestFile *string,
) {
	cmd.Flags().StringVar(requestFile, "request-file", "", "Delegated request file")
	_ = cmd.Flags().MarkHidden("request-file")
}
