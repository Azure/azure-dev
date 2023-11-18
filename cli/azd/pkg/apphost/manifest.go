package apphost

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
)

type Manifest struct {
	Schema    string               `json:"$schema"`
	Resources map[string]*Resource `json:"resources"`
}

type Resource struct {
	// Type is present on all resource types
	Type string `json:"type"`

	// Path is present on a project.v0 resource and is the full path to the project file.
	Path *string `json:"path,omitempty"`

	// Parent is present on a resource which is a child of another. It is the name of the parent resource. For example, a
	// postgres.database.v0 is a child of a postgres.server.v0, and so it would have a parent of which is the name of
	// the server resource.
	Parent *string `json:"parent,omitempty"`

	// Image is present on a container.v0 resource and is the image to use for the container.
	Image *string `json:"image,omitempty"`

	// Bindings is present on container.v0 and project.v0 resources, and is a map of binding names to binding details.
	Bindings map[string]*Binding `json:"bindings,omitempty"`

	// Env is present on a project.v0 and container.v0 resource, and is a map of environment variable names to value
	// expressions. The value expressions are simple expressions like "{redis.connectionString}" or "{postgres.port}" to
	// allow referencing properties of other resources. The set of properties supported in these expressions
	// depends on the type of resource you are referencing.
	Env map[string]string `json:"env,omitempty"`

	// Queues is optionally present on a azure.servicebus.v0 resource, and is a list of queue names to create.
	Queues *[]string `json:"queues,omitempty"`

	// Topics is optionally present on a azure.servicebus.v0 resource, and is a list of topic names to create.
	Topics *[]string `json:"topics,omitempty"`

	// Some resources just represent connections to existing resources that need not be provisioned.  These resources have
	// a "connectionString" property which is the connection string that should be used during binding.
	ConnectionString *string `json:"connectionString,omitempty"`
}

type Reference struct {
	Bindings []string `json:"bindings,omitempty"`
}

type Binding struct {
	ContainerPort *int   `json:"containerPort,omitempty"`
	Scheme        string `json:"scheme"`
	Protocol      string `json:"protocol"`
	Transport     string `json:"transport"`
	External      bool   `json:"external"`
}

// ManifestFromAppHost returns the Manifest from the given app host.
func ManifestFromAppHost(ctx context.Context, appHostProject string, dotnetCli dotnet.DotNetCli) (*Manifest, error) {
	tempDir, err := os.MkdirTemp("", "azd-provision")
	if err != nil {
		return nil, fmt.Errorf("creating temp directory for apphost-manifest.json: %w", err)
	}
	defer os.RemoveAll(tempDir)

	manifestPath := filepath.Join(tempDir, "apphost-manifest.json")

	if err := dotnetCli.PublishAppHostManifest(ctx, appHostProject, manifestPath); err != nil {
		return nil, fmt.Errorf("generating app host manifest: %w", err)
	}

	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}

	var manifest Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return nil, fmt.Errorf("unmarshalling manifest: %w", err)
	}

	// Make all paths absolute, to simplify logic for consumers.
	manifestDir := filepath.Dir(manifestPath)

	// The manifest writer writes paths relative to the manifest file. When we use a fixed manifest, the manifest is
	// located SxS with the appHostProject.
	if enabled, err := strconv.ParseBool(os.Getenv("AZD_DEBUG_DOTNET_APPHOST_USE_FIXED_MANIFEST")); err == nil && enabled {
		manifestDir = filepath.Dir(appHostProject)
	}

	for _, res := range manifest.Resources {
		if res.Path != nil {
			if !filepath.IsAbs(*res.Path) {
				*res.Path = filepath.Join(manifestDir, *res.Path)
			}
		}
	}

	return &manifest, nil
}
