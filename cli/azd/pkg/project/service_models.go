// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// Some endpoints include a discriminator suffix that should be displayed instead of the default 'Endpoint' label.
var endpointPattern = regexp.MustCompile(`(.+):\s(.+)`)

// ServiceContext defines the shared pipeline state across all phases of the Azure Developer CLI (azd) service lifecycle.
// It captures the results of each phase (Restore, Build, Package, Publish, Deploy) in a consistent, extensible format
// that is accessible to both core and external extension providers.
type ServiceContext struct {
	Restore ArtifactCollection `json:"restore"`
	Build   ArtifactCollection `json:"build"`
	Package ArtifactCollection `json:"package"`
	Publish ArtifactCollection `json:"publish"`
	Deploy  ArtifactCollection `json:"deploy"`
}

// NewServiceContext creates a new ServiceContext with initialized maps and slices
func NewServiceContext() *ServiceContext {
	return &ServiceContext{
		Restore: make(ArtifactCollection, 0),
		Build:   make(ArtifactCollection, 0),
		Package: make(ArtifactCollection, 0),
		Publish: make(ArtifactCollection, 0),
		Deploy:  make(ArtifactCollection, 0),
	}
}

// ServiceLifecycleEventArgs are the event arguments available when
// any service lifecycle event has been triggered
type ServiceLifecycleEventArgs struct {
	Project *ProjectConfig
	Service *ServiceConfig
	Args    map[string]any
}

// ServiceProgress represents an incremental progress message
// during a service operation such as restore, build, package & deploy
type ServiceProgress struct {
	Message   string
	Timestamp time.Time
}

// NewServiceProgress is a helper method to create a new
// progress message with a current timestamp
func NewServiceProgress(message string) ServiceProgress {
	return ServiceProgress{
		Message:   message,
		Timestamp: time.Now(),
	}
}

// ServiceRestoreResult is the result of a successful Restore operation
type ServiceRestoreResult struct {
	Artifacts ArtifactCollection `json:"artifacts"`
}

// ServiceBuildResult is the result of a successful Build operation
type ServiceBuildResult struct {
	Artifacts ArtifactCollection `json:"artifacts"`
}

// Supports rendering messages for UX items
func (sbr *ServiceBuildResult) ToString(currentIndentation string) string {
	if len(sbr.Artifacts) == 0 {
		return ""
	}

	// Get location from first artifact
	artifact := sbr.Artifacts[0]
	if artifact.Location != "" {
		return fmt.Sprintf("%s- Build Output: %s", currentIndentation, output.WithLinkFormat(artifact.Location))
	}

	return ""
}

func (sbr *ServiceBuildResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(*sbr)
}

type PackageOptions struct {
	OutputPath string
}

// ServicePackageResult is the result of a successful Package operation
type ServicePackageResult struct {
	Artifacts ArtifactCollection `json:"artifacts"`
}

// Supports rendering messages for UX items
func (spr *ServicePackageResult) ToString(currentIndentation string) string {
	if len(spr.Artifacts) == 0 {
		return ""
	}

	// Get location from first artifact
	artifact := spr.Artifacts[0]
	if artifact.Location != "" {
		return fmt.Sprintf("%s- Package Output: %s", currentIndentation, output.WithLinkFormat(artifact.Location))
	}

	return ""
}

func (spr *ServicePackageResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(*spr)
}

// ServicePublishResult is the result of a successful Publish operation for services.
type ServicePublishResult struct {
	Artifacts ArtifactCollection `json:"artifacts"`
}

// Supports rendering messages for UX items
func (spr *ServicePublishResult) ToString(currentIndentation string) string {
	// Look for container image artifacts to display remote image information
	containerImage, ok := spr.Artifacts.FindFirst(WithKind(ArtifactKindContainer))
	if ok && containerImage.Location != "" {
		return fmt.Sprintf("%s- Remote Image: %s\n", currentIndentation, output.WithLinkFormat(containerImage.Location))
	}

	// Empty since there's no relevant publish information to display
	return ""
}

func (spr *ServicePublishResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(*spr)
}

// ServiceDeployResult is the result of a successful Deploy operation
type ServiceDeployResult struct {
	Artifacts ArtifactCollection `json:"artifacts"`
}

// Supports rendering messages for UX items
func (spr *ServiceDeployResult) ToString(currentIndentation string) string {
	builder := strings.Builder{}

	// Extract endpoints from artifacts
	endpoints := spr.Artifacts.Find(WithKind(ArtifactKindEndpoint))
	if len(endpoints) == 0 {
		builder.WriteString(fmt.Sprintf("%s- No endpoints were found\n", currentIndentation))
	} else {
		for _, endpointArtifact := range endpoints {
			label := "Endpoint"
			url := endpointArtifact.Location

			// When the endpoint pattern is matched used the first sub match as the endpoint label.
			matches := endpointPattern.FindStringSubmatch(url)
			if len(matches) == 3 {
				label = matches[1]
				url = matches[2]
			}

			builder.WriteString(fmt.Sprintf("%s- %s: %s\n", currentIndentation, label, output.WithLinkFormat(url)))
		}
	}

	return builder.String()
}

func (spr *ServiceDeployResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(*spr)
}
