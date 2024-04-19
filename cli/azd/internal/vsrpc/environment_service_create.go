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
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
)

// CreateEnvironmentAsync is the server implementation of:
// ValueTask<bool> CreateEnvironmentAsync(Session, Environment, IObserver<ProgressMessage>, CancellationToken);
func (s *environmentService) CreateEnvironmentAsync(
	ctx context.Context, sessionId Session, newEnv Environment, observer IObserver[ProgressMessage],
) (bool, error) {
	session, err := s.server.validateSession(ctx, sessionId)
	if err != nil {
		return false, err
	}

	envSpec := environment.Spec{
		Name:         newEnv.Name,
		Subscription: newEnv.Properties["Subscription"],
		Location:     newEnv.Properties["Location"],
	}

	var c struct {
		azdContext *azdcontext.AzdContext `container:"type"`
		dotnetCli  dotnet.DotNetCli       `container:"type"`
		envManager environment.Manager    `container:"type"`
	}

	container, err := session.newContainer()
	if err != nil {
		return false, err
	}
	if err := container.Fill(&c); err != nil {
		return false, err
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

	// If an azure.yaml doesn't already exist, we need to create one. Creating an environment implies initializing the
	// azd project if it does not already exist.
	if _, err := os.Stat(c.azdContext.ProjectPath()); errors.Is(err, fs.ErrNotExist) {
		_ = observer.OnNext(ctx, newImportantProgressMessage("Analyzing Aspire Application (this might take a moment...)"))
		// Write an azure.yaml file to the project.
		hosts, err := appdetect.DetectAspireHosts(ctx, c.azdContext.ProjectDirectory(), c.dotnetCli)
		if err != nil {
			return false, fmt.Errorf("failed to discover app host project under %s: %w", c.azdContext.ProjectPath(), err)
		}

		if len(hosts) == 0 {
			return false, fmt.Errorf("no app host projects found under %s", c.azdContext.ProjectPath())
		}

		if len(hosts) > 1 {
			return false, fmt.Errorf("multiple app host projects found under %s", c.azdContext.ProjectPath())
		}

		manifest, err := apphost.ManifestFromAppHost(ctx, hosts[0].Path, c.dotnetCli, dotnetEnv)
		if err != nil {
			return false, fmt.Errorf("reading app host manifest: %w", err)
		}

		files, err := apphost.GenerateProjectArtifacts(
			ctx,
			c.azdContext.ProjectDirectory(),
			filepath.Base(c.azdContext.ProjectDirectory()),
			manifest,
			hosts[0].Path,
		)
		if err != nil {
			return false, fmt.Errorf("generating project artifacts: %w", err)
		}

		file := files["azure.yaml"]
		projectFilePath := filepath.Join(c.azdContext.ProjectDirectory(), "azure.yaml")

		if err := os.WriteFile(projectFilePath, []byte(file.Contents), file.Mode); err != nil {
			return false, fmt.Errorf("writing azure.yaml: %w", err)
		}
	} else if err != nil {
		return false, fmt.Errorf("checking for project: %w", err)
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

	if err := c.azdContext.SetDefaultEnvironmentName(newEnv.Name); err != nil {
		return false, fmt.Errorf("saving default environment: %w", err)
	}

	return true, nil
}
