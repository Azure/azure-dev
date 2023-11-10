package project

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
)

type ImportManager struct {
}

func NewImportManager() *ImportManager {
	return &ImportManager{}
}

func (im *ImportManager) HasService(ctx context.Context, projectConfig *ProjectConfig, name string) (bool, error) {
	services, err := im.ServiceStable(ctx, projectConfig)
	if err != nil {
		return false, err
	}

	for _, svc := range services {
		if svc.Name == name {
			return true, nil
		}
	}

	return false, nil
}

// Retrieves the list of services in the project, in a stable ordering that is deterministic.
func (im *ImportManager) ServiceStable(ctx context.Context, projectConfig *ProjectConfig) ([]*ServiceConfig, error) {
	allServices := make(map[string]*ServiceConfig)

	for name, svcConfig := range projectConfig.Services {
		allServices[name] = svcConfig
	}

	// Collect all the services and then sort the resulting list by name. This provides a stable ordering of services.
	allServicesSlice := make([]*ServiceConfig, 0, len(allServices))
	for _, v := range allServices {
		allServicesSlice = append(allServicesSlice, v)
	}

	slices.SortFunc(allServicesSlice, func(x, y *ServiceConfig) int {
		return strings.Compare(x.Name, y.Name)
	})

	return allServicesSlice, nil
}

func (im *ImportManager) ProjectInfrastructure(ctx context.Context, projectConfig *ProjectConfig) (*Infra, error) {
	infraRoot := projectConfig.Infra.Path
	if !filepath.IsAbs(infraRoot) {
		infraRoot = filepath.Join(projectConfig.Path, infraRoot)
	}

	// Allow overriding the infrastructure by placing an `infra` folder in the location that would be expected based
	// on azure.yaml
	if _, err := os.Stat(infraRoot); err == nil {
		log.Printf("using infrastructure from %s directory", infraRoot)
		return &Infra{
			Options: projectConfig.Infra,
		}, nil
	}

	return nil, fmt.Errorf(
		"this project does not contain any infrastructure, have you created an '%s' folder?", filepath.Base(infraRoot))
}

// Infra represents the (possibly temporarily generated) infrastructure. Call [Cleanup] when done with infrastructure,
// which will cause any temporarily generated files to be removed.
type Infra struct {
	Options    provisioning.Options
	cleanupDir string
}

func (i *Infra) Cleanup() error {
	if i.cleanupDir != "" {
		return os.RemoveAll(i.cleanupDir)
	}

	return nil
}
