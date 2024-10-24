// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/psanford/memfs"
)

// Generates the in-memory contents of an `infra` directory.
func infraFs(_ context.Context, prjConfig *ProjectConfig) (fs.FS, error) {
	t, err := scaffold.Load()
	if err != nil {
		return nil, fmt.Errorf("loading scaffold templates: %w", err)
	}

	infraSpec, err := infraSpec(prjConfig)
	if err != nil {
		return nil, fmt.Errorf("generating infrastructure spec: %w", err)
	}

	files, err := scaffold.ExecInfraFs(t, *infraSpec)
	if err != nil {
		return nil, fmt.Errorf("executing scaffold templates: %w", err)
	}

	return files, nil
}

// Returns the infrastructure configuration that points to a temporary, generated `infra` directory on the filesystem.
func tempInfra(
	ctx context.Context,
	prjConfig *ProjectConfig) (*Infra, error) {
	tmpDir, err := os.MkdirTemp("", "azd-infra")
	if err != nil {
		return nil, fmt.Errorf("creating temporary directory: %w", err)
	}

	files, err := infraFs(ctx, prjConfig)
	if err != nil {
		return nil, err
	}

	err = fs.WalkDir(files, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		target := filepath.Join(tmpDir, path)
		if err := os.MkdirAll(filepath.Dir(target), osutil.PermissionDirectoryOwnerOnly); err != nil {
			return err
		}

		contents, err := fs.ReadFile(files, path)
		if err != nil {
			return err
		}

		return os.WriteFile(target, contents, d.Type().Perm())
	})
	if err != nil {
		return nil, fmt.Errorf("writing infrastructure: %w", err)
	}

	return &Infra{
		Options: provisioning.Options{
			Provider: provisioning.Bicep,
			Path:     tmpDir,
			Module:   DefaultModule,
		},
		cleanupDir: tmpDir,
	}, nil
}

// Generates the filesystem of all infrastructure files to be placed, rooted at the project directory.
// The content only includes `./infra` currently.
func infraFsForProject(ctx context.Context, prjConfig *ProjectConfig) (fs.FS, error) {
	infraFS, err := infraFs(ctx, prjConfig)
	if err != nil {
		return nil, err
	}

	infraPathPrefix := DefaultPath
	if prjConfig.Infra.Path != "" {
		infraPathPrefix = prjConfig.Infra.Path
	}

	// root the generated content at the project directory
	generatedFS := memfs.New()
	err = fs.WalkDir(infraFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		err = generatedFS.MkdirAll(filepath.Join(infraPathPrefix, filepath.Dir(path)), osutil.PermissionDirectoryOwnerOnly)
		if err != nil {
			return err
		}

		contents, err := fs.ReadFile(infraFS, path)
		if err != nil {
			return err
		}

		return generatedFS.WriteFile(filepath.Join(infraPathPrefix, path), contents, d.Type().Perm())
	})
	if err != nil {
		return nil, err
	}

	return generatedFS, nil
}

func infraSpec(projectConfig *ProjectConfig) (*scaffold.InfraSpec, error) {
	infraSpec := scaffold.InfraSpec{}
	// backends -> frontends
	backendMapping := map[string]string{}

	for _, res := range projectConfig.Resources {
		switch res.Type {
		case ResourceTypeDbRedis:
			infraSpec.DbRedis = &scaffold.DatabaseRedis{}
		case ResourceTypeDbMongo:
			infraSpec.DbCosmosMongo = &scaffold.DatabaseCosmosMongo{
				DatabaseName: res.Name,
			}
		case ResourceTypeDbPostgres:
			infraSpec.DbPostgres = &scaffold.DatabasePostgres{
				DatabaseName: res.Name,
				DatabaseUser: "pgadmin",
			}
		case ResourceTypeHostContainerApp:
			svcSpec := scaffold.ServiceSpec{
				Name: res.Name,
				Port: -1,
			}

			err := mapContainerApp(res, &svcSpec)
			if err != nil {
				return nil, err
			}

			err = mapHostUses(res, &svcSpec, backendMapping, projectConfig)
			if err != nil {
				return nil, err
			}

			infraSpec.Services = append(infraSpec.Services, svcSpec)
		}
	}

	// create reverse frontends -> backends mapping
	for _, svc := range infraSpec.Services {
		if front, ok := backendMapping[svc.Name]; ok {
			if svc.Backend == nil {
				svc.Backend = &scaffold.Backend{}
			}

			svc.Backend.Frontends = append(svc.Backend.Frontends, scaffold.ServiceReference{Name: front})
		}
	}

	slices.SortFunc(infraSpec.Services, func(a, b scaffold.ServiceSpec) int {
		return strings.Compare(a.Name, b.Name)
	})

	return &infraSpec, nil
}

func mapContainerApp(res *ResourceConfig, svcSpec *scaffold.ServiceSpec) error {
	props := res.Props.(ContainerAppProps)
	for _, envVar := range props.Env {
		isSecret := len(envVar.Secret) > 0
		value := envVar.Value
		if isSecret {
			// TODO: handle secrets
			continue
		}

		svcSpec.Env[envVar.Name] = value
	}

	port := props.Port
	if port < 1 || port > 65535 {
		return fmt.Errorf("port value %d for host %s must be between 1 and 65535", port, res.Name)
	}

	svcSpec.Port = port
	return nil
}

func mapHostUses(
	res *ResourceConfig,
	svcSpec *scaffold.ServiceSpec,
	backendMapping map[string]string,
	prj *ProjectConfig) error {
	for _, use := range res.Uses {
		useRes, ok := prj.Resources[use]
		if !ok {
			return fmt.Errorf("resource %s uses %s, which does not exist", res.Name, use)
		}

		switch useRes.Type {
		case ResourceTypeDbMongo:
			svcSpec.DbCosmosMongo = &scaffold.DatabaseReference{DatabaseName: useRes.Name}
		case ResourceTypeDbPostgres:
			svcSpec.DbPostgres = &scaffold.DatabaseReference{DatabaseName: useRes.Name}
		case ResourceTypeDbRedis:
			svcSpec.DbRedis = &scaffold.DatabaseReference{DatabaseName: useRes.Name}
		case ResourceTypeHostContainerApp:
			if svcSpec.Frontend == nil {
				svcSpec.Frontend = &scaffold.Frontend{}
			}

			svcSpec.Frontend.Backends = append(svcSpec.Frontend.Backends,
				scaffold.ServiceReference{Name: use})
			backendMapping[use] = res.Name // record the backend -> frontend mapping
		}
	}

	return nil
}
