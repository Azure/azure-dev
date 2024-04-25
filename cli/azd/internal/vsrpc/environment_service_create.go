package vsrpc

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/pkg/apphost"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
)

// CreateEnvironmentAsync is the server implementation of:
// ValueTask<bool> CreateEnvironmentAsync(RequestContext, Environment, IObserver<ProgressMessage>, CancellationToken);
func (s *environmentService) CreateEnvironmentAsync(
	ctx context.Context, rc RequestContext, newEnv Environment, observer IObserver[ProgressMessage],
) (bool, error) {
	session, err := s.server.validateSession(ctx, rc.Session)
	if err != nil {
		return false, err
	}

	envSpec := environment.Spec{
		Name:         newEnv.Name,
		Subscription: newEnv.Properties["Subscription"],
		Location:     newEnv.Properties["Location"],
	}

	// We had thought at one point that we would introduce `ASPIRE_ENVIRONMENT` as a sibling to `ASPNETCORE_ENVIRONMENT` and
	// `DOTNET_ENVIRONMENT` and was aspire specific. We no longer intend to do this (because having both DOTNET and
	// ASPNETCORE versions is already confusing enough). For now, we'll use `ASPIRE_ENVIRONMENT` to seed the initial values of
	// `DOTNET_ENVIRONMENT`, but allow them to be overriden at environment construction time.
	//
	// We only retain `DOTNET_ENVIRONMENT` in the .env file.
	dotnetEnv := newEnv.Properties["ASPIRE_ENVIRONMENT"]

	if v, has := newEnv.Values["DOTNET_ENVIRONMENT"]; has {
		dotnetEnv = v
	}

	cmdRun := exec.NewCommandRunner(&exec.RunnerOptions{})
	dotnetCli := dotnet.NewDotNetCli(cmdRun)

	var errMultipleAppHosts = errors.New("multiple app host projects found")

	// If an azure.yaml doesn't already exist, we need to create one. Creating an environment implies initializing the
	// azd project if it does not already exist.
	initProject := func(dir string) error {
		_ = observer.OnNext(ctx, newImportantProgressMessage("Analyzing Aspire Application (this might take a moment...)"))
		// Write an azure.yaml file to the project.
		hosts, err := appdetect.DetectAspireHosts(ctx, dir, dotnetCli)
		if err != nil {
			return fmt.Errorf("failed to discover app host project under %s: %w", dir, err)
		}

		if len(hosts) == 0 {
			return fmt.Errorf("no app host projects found under %s", dir)
		}

		if len(hosts) > 1 {
			return fmt.Errorf("%w under %s", errMultipleAppHosts, dir)
		}

		manifest, err := apphost.ManifestFromAppHost(ctx, hosts[0].Path, dotnetCli, dotnetEnv)
		if err != nil {
			return fmt.Errorf("reading app host manifest: %w", err)
		}

		files, err := apphost.GenerateProjectArtifacts(
			ctx,
			dir,
			filepath.Base(dir),
			manifest,
			hosts[0].Path,
		)
		if err != nil {
			return fmt.Errorf("generating project artifacts: %w", err)
		}

		file := files["azure.yaml"]
		projectFilePath := filepath.Join(dir, "azure.yaml")

		if err := os.WriteFile(projectFilePath, []byte(file.Contents), file.Mode); err != nil {
			return fmt.Errorf("writing azure.yaml: %w", err)
		}
		return nil
	}

	initializeInAppHostDir := false
	hostProjectDir := filepath.Dir(rc.HostProjectPath)
	// Check if local project has azure.yaml file. If it exists, we're done.
	_, err = os.Stat(filepath.Join(hostProjectDir, "azure.yaml"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("checking for project: %w", err)
	} else if errors.Is(err, fs.ErrNotExist) {
		// Should we create in root, or in app host dir?
		// Try root, then fall back to app host dir.
		_, err = os.Stat(filepath.Join(session.rootPath, "azure.yaml"))
		if err == nil {
			prjConfig, err := project.Load(ctx, filepath.Join(session.rootPath, "azure.yaml"))
			if err != nil {
				return false, err
			}

			for _, svc := range prjConfig.Services {
				// check if the current app host project matches the one in root
				if svc.Language == project.ServiceLanguageDotNet && svc.Host == project.ContainerAppTarget &&
					filepath.Join(session.rootPath, svc.RelativePath) != rc.HostProjectPath {
					initializeInAppHostDir = true
				}

				break
			}
		} else if errors.Is(err, fs.ErrNotExist) {
			if err := initProject(session.rootPath); err != nil && !errors.Is(err, errMultipleAppHosts) {
				return false, err
			} else if errors.Is(err, errMultipleAppHosts) {
				initializeInAppHostDir = true
			}
		} else if err != nil {
			return false, fmt.Errorf("checking for project: %w", err)
		}

		if initializeInAppHostDir {
			if err := initProject(hostProjectDir); err != nil {
				return false, err
			}
		}
	}

	var c struct {
		azdContext *azdcontext.AzdContext `container:"type"`
		dotnetCli  dotnet.DotNetCli       `container:"type"`
		envManager environment.Manager    `container:"type"`
	}

	container, err := session.newContainer(rc)
	if err != nil {
		return false, err
	}
	if err := container.Fill(&c); err != nil {
		return false, err
	}

	azdEnv, err := c.envManager.Create(ctx, envSpec)
	if err != nil {
		return false, fmt.Errorf("creating new environment: %w", err)
	}

	if dotnetEnv != "" {
		azdEnv.DotenvSet("DOTNET_ENVIRONMENT", dotnetEnv)
	}

	for key, value := range newEnv.Values {
		azdEnv.DotenvSet(key, value)
	}

	if err := c.envManager.Save(ctx, azdEnv); err != nil {
		return false, fmt.Errorf("saving new environment: %w", err)
	}

	if err := c.azdContext.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: newEnv.Name}); err != nil {
		return false, fmt.Errorf("saving default environment: %w", err)
	}

	return true, nil
}
