// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// ArtifactKind represents well-known artifact types in the Azure Developer CLI
type ArtifactKind string

const (
	// Build and compilation artifacts
	ArtifactKindDirectory ArtifactKind = "directory" // Directory containing project or build artifacts
	ArtifactKindConfig    ArtifactKind = "config"    // Configuration

	// Package artifacts
	ArtifactKindArchive   ArtifactKind = "archive"   // Zip/archive package
	ArtifactKindContainer ArtifactKind = "container" // Docker/container image

	// Service and deployment artifacts
	ArtifactKindEndpoint   ArtifactKind = "endpoint"   // Service endpoint URL
	ArtifactKindDeployment ArtifactKind = "deployment" // Deployment result or endpoint
	ArtifactKindResource   ArtifactKind = "resource"   // Azure Resource
	ArtifactKindOutput     ArtifactKind = "output"     // Output from deploy command
)

// LocationKind represents the type of location for an artifact
type LocationKind string

const (
	LocationKindLocal  LocationKind = "local"  // Local file system path
	LocationKindRemote LocationKind = "remote" // Remote reference (URL, registry, etc.)
)

// validLocationKinds contains all known valid location kinds for validation
var validLocationKinds = []LocationKind{
	LocationKindLocal,
	LocationKindRemote,
}

// validArtifactKinds contains all known valid artifact kinds for validation
var validArtifactKinds = []ArtifactKind{
	// Build and compilation artifacts
	ArtifactKindDirectory,
	ArtifactKindConfig,
	// Package artifacts
	ArtifactKindArchive,
	ArtifactKindContainer,
	// Service and deployment artifacts
	ArtifactKindEndpoint,
	ArtifactKindDeployment,
	ArtifactKindResource,
	ArtifactKindOutput,
}

// endpointPattern matches endpoints with discriminator suffix that should be displayed
// instead of the default 'Endpoint' label.
// Pattern: "label: url" where label becomes the display name
var endpointPattern = regexp.MustCompile(`(.+):\s(.+)`)

// Artifact represents a build, package, or deployment artifact with its location and metadata.
type Artifact struct {
	Kind         ArtifactKind      `json:"kind"`                   // Required: artifact type
	Location     string            `json:"location,omitempty"`     // Optional: location of the artifact
	LocationKind LocationKind      `json:"locationKind,omitempty"` // Optional: local, remote, or other
	Metadata     map[string]string `json:"metadata,omitempty"`     // Optional: arbitrary key/value pairs
}

// ToString implements the UxItem interface for display output
func (a *Artifact) ToString(currentIndentation string) string {
	if a.Location == "" {
		return ""
	}

	// Format output based on artifact kind
	switch a.Kind {
	case ArtifactKindEndpoint:
		// Handle endpoint with optional discriminator suffix pattern
		label := "Endpoint"
		url := a.Location

		// When the endpoint pattern is matched, use the first sub match as the endpoint label.
		matches := endpointPattern.FindStringSubmatch(url)
		if len(matches) == 3 {
			label = matches[1]
			url = matches[2]
		}

		return fmt.Sprintf("%s- %s: %s", currentIndentation, label, output.WithLinkFormat(url))

	case ArtifactKindContainer:
		if a.LocationKind == LocationKindRemote {
			return fmt.Sprintf("%s- Remote Image: %s", currentIndentation, output.WithLinkFormat(a.Location))
		}
		return fmt.Sprintf("%s- Container: %s", currentIndentation, output.WithLinkFormat(a.Location))

	case ArtifactKindArchive:
		return fmt.Sprintf("%s- Package Output: %s", currentIndentation, output.WithLinkFormat(a.Location))

	case ArtifactKindDirectory:
		return fmt.Sprintf("%s- Build Output: %s", currentIndentation, output.WithLinkFormat(a.Location))

	// Ignore other artifact kinds for now
	default:
		return ""
	}
}

// MarshalJSON implements the UxItem interface JSON marshaling
func (a *Artifact) MarshalJSON() ([]byte, error) {
	return json.Marshal(*a)
}

// ArtifactCollection provides typed operations on a collection of artifacts
type ArtifactCollection []Artifact

// Add appends an artifact to the collection with validation
func (ac *ArtifactCollection) Add(artifacts ...Artifact) error {
	for _, artifact := range artifacts {
		// Validate required fields
		if err := validateArtifact(artifact); err != nil {
			return fmt.Errorf("invalid artifact: %w", err)
		}
	}

	*ac = append(*ac, artifacts...)

	return nil
}

// validateArtifact ensures artifact has valid required fields
func validateArtifact(artifact Artifact) error {
	// Validate Kind is not empty
	if strings.TrimSpace(string(artifact.Kind)) == "" {
		return fmt.Errorf("kind is required and cannot be empty")
	}

	if strings.TrimSpace(artifact.Location) == "" {
		return fmt.Errorf("location is required and cannot be empty")
	}

	// Validate Kind is a known value
	if !slices.Contains(validArtifactKinds, artifact.Kind) {
		return fmt.Errorf("kind '%s' is not a recognized artifact kind", artifact.Kind)
	}

	// Validate LocationKind is not empty
	if strings.TrimSpace(string(artifact.LocationKind)) == "" {
		return fmt.Errorf("locationKind is required and cannot be empty")
	}

	// Validate LocationKind is a known value
	if !slices.Contains(validLocationKinds, artifact.LocationKind) {
		return fmt.Errorf("locationKind must be either '%s' or '%s', got '%s'",
			LocationKindLocal, LocationKindRemote, artifact.LocationKind)
	}

	return nil
}

// FindOpts represents functional options for filtering artifacts
type FindOpts func(*findFilter)

// findFilter holds the search criteria for artifact filtering
type findFilter struct {
	kind         *ArtifactKind
	locationKind *LocationKind
}

// WithKind filters artifacts by the specified kind
func WithKind(kind ArtifactKind) FindOpts {
	return func(c *findFilter) {
		c.kind = &kind
	}
}

// WithLocationKind filters artifacts by the specified location kind
func WithLocationKind(locationKind LocationKind) FindOpts {
	return func(c *findFilter) {
		c.locationKind = &locationKind
	}
}

// matches checks if an artifact matches the given criteria (all criteria must match)
func (c *findFilter) matches(artifact Artifact) bool {
	if c.kind != nil && artifact.Kind != *c.kind {
		return false
	}
	if c.locationKind != nil && artifact.LocationKind != *c.locationKind {
		return false
	}
	return true
}

// Find returns all artifacts matching the specified criteria
func (ac ArtifactCollection) Find(opts ...FindOpts) []Artifact {
	criteria := &findFilter{}
	for _, opt := range opts {
		opt(criteria)
	}

	var result []Artifact
	for _, artifact := range ac {
		if criteria.matches(artifact) {
			result = append(result, artifact)
		}
	}
	return result
}

// FindFirst returns the first artifact matching the specified criteria
func (ac ArtifactCollection) FindFirst(opts ...FindOpts) (Artifact, bool) {
	criteria := &findFilter{}
	for _, opt := range opts {
		opt(criteria)
	}

	for _, artifact := range ac {
		if criteria.matches(artifact) {
			return artifact, true
		}
	}
	return Artifact{}, false
}

// ToString implements the UxItem interface for ArtifactCollection display output
func (ac ArtifactCollection) ToString(currentIndentation string) string {
	if len(ac) == 0 {
		return fmt.Sprintf("%s- No artifacts were found\n", currentIndentation)
	}

	var builder strings.Builder
	for _, artifact := range ac {
		if output := artifact.ToString(currentIndentation); output != "" {
			builder.WriteString(output)
			if !strings.HasSuffix(output, "\n") {
				builder.WriteString("\n")
			}
		}
	}

	return builder.String()
}

// MarshalJSON implements the UxItem interface JSON marshaling for ArtifactCollection
func (ac ArtifactCollection) MarshalJSON() ([]byte, error) {
	return json.Marshal([]Artifact(ac))
}
