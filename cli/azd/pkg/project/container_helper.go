package project

import (
	"context"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/benbjohnson/clock"
)

type ContainerHelper struct {
	env   *environment.Environment
	clock clock.Clock
}

func NewContainerHelper(env *environment.Environment, clock clock.Clock) *ContainerHelper {
	return &ContainerHelper{
		env:   env,
		clock: clock,
	}
}

func (ch *ContainerHelper) LoginServer(ctx context.Context) (string, error) {
	loginServer, has := ch.env.Values[environment.ContainerRegistryEndpointEnvVarName]
	if !has {
		return "", fmt.Errorf(
			"could not determine container registry endpoint, ensure %s is set as an output of your infrastructure",
			environment.ContainerRegistryEndpointEnvVarName,
		)
	}

	return loginServer, nil
}

func (ch *ContainerHelper) RemoteImageTag(ctx context.Context, serviceConfig *ServiceConfig) (string, error) {
	loginServer, err := ch.LoginServer(ctx)
	if err != nil {
		return "", err
	}

	localTag, err := ch.LocalImageTag(ctx, serviceConfig)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(
		"%s/%s",
		loginServer,
		localTag,
	), nil
}

func (ch *ContainerHelper) LocalImageTag(ctx context.Context, serviceConfig *ServiceConfig) (string, error) {
	configuredTag, err := serviceConfig.Docker.Tag.Envsubst(ch.env.Getenv)
	if err != nil {
		return "", err
	}

	if configuredTag != "" {
		return configuredTag, nil
	}

	return fmt.Sprintf("%s/%s-%s:azd-deploy-%d",
		strings.ToLower(serviceConfig.Project.Name),
		strings.ToLower(serviceConfig.Name),
		strings.ToLower(ch.env.GetEnvName()),
		ch.clock.Now().Unix(),
	), nil
}
