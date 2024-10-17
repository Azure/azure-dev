package ext

import (
	"context"
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/sdk/azdcore"
	"github.com/azure/azure-dev/cli/sdk/azdcore/azure"
	"github.com/azure/azure-dev/cli/sdk/azdcore/common/ioc"
	"github.com/azure/azure-dev/cli/sdk/azdcore/config"
	"github.com/azure/azure-dev/cli/sdk/azdcore/contracts"
	"github.com/azure/azure-dev/cli/sdk/azdcore/environment"
	"github.com/azure/azure-dev/cli/sdk/azdcore/ext/account"
	"github.com/azure/azure-dev/cli/sdk/azdcore/project"
)

var (
	ErrProjectNotFound     = errors.New("azd project not found in current path")
	ErrEnvironmentNotFound = errors.New("azd environment not found")
	ErrUserConfigNotFound  = errors.New("azd user config not found")
	ErrPrincipalNotFound   = errors.New("azd principal not found")
	ErrNotLoggedIn         = errors.New("azd credential not available")
)

var current *Context

type Context struct {
	container   *ioc.NestedContainer
	project     *project.ProjectConfig
	environment *environment.Environment
	userConfig  config.UserConfig
	credential  azcore.TokenCredential
	principal   *account.Principal
}

func CurrentContext(ctx context.Context) (*Context, error) {
	if current == nil {
		container := ioc.NewNestedContainer(nil)
		registerComponents(ctx, container)

		current = &Context{
			container: container,
		}
	}

	return current, nil
}

func (c *Context) Project(ctx context.Context) (*project.ProjectConfig, error) {
	if c.project == nil {
		var project *project.ProjectConfig
		if err := c.container.Resolve(&project); err != nil {
			return nil, fmt.Errorf("%w, Details: %w", ErrProjectNotFound, err)
		}

		c.project = project
	}

	return c.project, nil
}

func (c *Context) Environment(ctx context.Context) (*environment.Environment, error) {
	if c.environment == nil {
		var env *environment.Environment
		if err := c.container.Resolve(&env); err != nil {
			return nil, fmt.Errorf("%w, Details: %w", ErrEnvironmentNotFound, err)
		}

		c.environment = env
	}

	return c.environment, nil
}

func (c *Context) UserConfig(ctx context.Context) (config.UserConfig, error) {
	if c.userConfig == nil {
		var userConfig config.UserConfig
		if err := c.container.Resolve(&userConfig); err != nil {
			return nil, fmt.Errorf("%w, Details: %w", ErrUserConfigNotFound, err)
		}

		c.userConfig = userConfig
	}

	return c.userConfig, nil
}

func (c *Context) Credential() (azcore.TokenCredential, error) {
	if c.credential == nil {
		azdCredential, err := azidentity.NewAzureDeveloperCLICredential(nil)
		if err != nil {
			return nil, fmt.Errorf("%w, Details: %w", ErrNotLoggedIn, err)
		}

		c.credential = azdCredential
	}

	return c.credential, nil
}

func (c *Context) Principal(ctx context.Context) (*account.Principal, error) {
	if c.principal == nil {
		credential, err := c.Credential()
		if err != nil {
			return nil, fmt.Errorf("%w, Details: %w", ErrPrincipalNotFound, err)
		}

		accessToken, err := credential.GetToken(ctx, policy.TokenRequestOptions{
			Scopes: []string{"https://management.azure.com/.default"},
		})
		if err != nil {
			return nil, err
		}

		claims, err := azure.GetClaimsFromAccessToken(accessToken.Token)
		if err != nil {
			return nil, err
		}

		principal := account.Principal(claims)
		c.principal = &principal
	}

	return c.principal, nil
}

func (c *Context) SaveEnvironment(ctx context.Context, env *environment.Environment) error {
	err := c.container.Invoke(func(envManager environment.Manager) error {
		return envManager.Save(ctx, env)
	})

	return err
}

func (c *Context) SaveUserConfig(ctx context.Context, userConfig config.UserConfig) error {
	err := c.container.Invoke(func(userConfigManager config.UserConfigManager) error {
		return userConfigManager.Save(userConfig)
	})

	return err
}

func registerComponents(ctx context.Context, container *ioc.NestedContainer) error {
	container.MustRegisterSingleton(func() ioc.ServiceLocator {
		return container
	})

	container.MustRegisterSingleton(azdcore.NewContext)
	container.MustRegisterSingleton(environment.NewManager)
	container.MustRegisterSingleton(environment.NewLocalFileDataStore)
	container.MustRegisterSingleton(config.NewFileConfigManager)
	container.MustRegisterSingleton(config.NewManager)
	container.MustRegisterSingleton(config.NewUserConfigManager)

	container.MustRegisterSingleton(func(azdContext *azdcore.Context) (*project.ProjectConfig, error) {
		if azdContext == nil {
			return nil, azdcore.ErrNoProject
		}

		return project.Load(ctx, azdContext.ProjectPath())
	})

	container.MustRegisterSingleton(
		func(azdContext *azdcore.Context, envManager environment.Manager) (*environment.Environment, error) {
			if azdContext == nil {
				return nil, azdcore.ErrNoProject
			}

			envName, err := azdContext.GetDefaultEnvironmentName()
			if err != nil {
				return nil, err
			}

			environment, err := envManager.Get(ctx, envName)
			if err != nil {
				return nil, err
			}

			return environment, nil
		},
	)

	container.MustRegisterSingleton(func(userConfigManager config.UserConfigManager) (config.UserConfig, error) {
		return userConfigManager.Load()
	})

	container.MustRegisterSingleton(
		func(projectConfig *project.ProjectConfig, userConfigManager config.UserConfigManager) (*contracts.RemoteConfig, error) {
			var remoteStateConfig *contracts.RemoteConfig

			userConfig, err := userConfigManager.Load()
			if err != nil {
				return nil, fmt.Errorf("loading user config: %w", err)
			}

			// Lookup remote state config in the following precedence:
			// 1. Project azure.yaml
			// 2. User configuration
			if projectConfig != nil && projectConfig.State != nil && projectConfig.State.Remote != nil {
				remoteStateConfig = projectConfig.State.Remote
			} else {
				if _, err := userConfig.GetSection("state.remote", &remoteStateConfig); err != nil {
					return nil, fmt.Errorf("getting remote state config: %w", err)
				}
			}

			return remoteStateConfig, nil
		},
	)

	return nil
}
