// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package lifecycle

import (
	"context"
	"errors"
	"sync"

	"azurespringboot/internal/deploy"
	"azurespringboot/internal/exterrors"
	"azurespringboot/internal/postgres"
	"azurespringboot/internal/springmodel"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

type resolvedService struct {
	service  *azdext.ServiceConfig
	mode     springmodel.DatabaseType
	database springmodel.ResolvedDatabase
}

func validationError(code, message, suggestion string) error {
	return exterrors.Validation(code, message, suggestion)
}

func configurationError(message string) error {
	return exterrors.Configuration(message)
}

func nonDisclosureError(err error) error {
	return exterrors.NonDisclosureError(err)
}

// EnvironmentStore returns the azd environment store configured on the Runtime.
func (r *Runtime) EnvironmentStore() springmodel.EnvironmentStore {
	return r.environments
}

func (r *Runtime) beginAutomaticNetwork(ctx context.Context) (func() error, error) {
	binding, present := ctx.Value(deploy.DeploymentBindingKey{}).(*deploy.DeploymentBinding)
	if !present || !binding.Automatic || binding.Values[springmodel.PostgresServerIDEnv] == "" {
		return func() error { return nil }, nil
	}
	if r.prepareNetwork == nil {
		return func() error { return nil }, errors.New("automatic database network verification is unavailable")
	}
	cleanup, err := r.prepareNetwork(ctx, binding)
	closeNetwork := sync.OnceValue(func() error {
		if cleanup == nil {
			return nil
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), postgres.CleanupTimeout)
		defer cancel()
		return cleanup(cleanupCtx)
	})
	return closeNetwork, err
}
