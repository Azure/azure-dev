// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package accesshost adapts the azd host contracts to the independently versioned
// Foundry access library. Command policy and warning rendering remain in the extension.
package accesshost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/shared/foundry/access"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// PrepareOptions selects the target and documented automatic-grant command policy.
type PrepareOptions struct {
	Endpoint, EnvironmentName, Collection, APIVersion string
	AutoGrant                                         bool
	Warn                                              func(string)
}

type ensurer interface {
	Ensure(context.Context, access.Request) (access.Result, error)
}

// Prepare preserves warning-only access checks, cancellation, and the existing
// developer skip setting. It never interprets --no-prompt as a grant policy.
func Prepare(ctx context.Context, options PrepareOptions) (azcore.TokenCredential, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if skip, _ := strconv.ParseBool(os.Getenv("AZD_AGENT_SKIP_ROLE_ASSIGNMENTS")); skip {
		return nil, nil
	}
	client, err := azdext.NewAzdClient()
	if err != nil {
		return nil, ctx.Err()
	}
	defer client.Close()
	return prepare(ctx, environment{client.Environment()}, options, func() (ensurer, error) {
		backend, err := access.NewAzureBackend(identity{client.Account()}, nil)
		if err != nil {
			return nil, err
		}
		return access.NewService(backend, nil)
	})
}

func prepare(
	ctx context.Context, environment access.Environment, options PrepareOptions, newEnsurer func() (ensurer, error),
) (azcore.TokenCredential, error) {
	warn := func(err error) (azcore.TokenCredential, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Translate portable errors at the extension boundary; never render raw causes.
		diagnostic := translate(err)
		message := "Foundry project access could not be ensured; continuing with the requested operation. " +
			"If access is denied, ask an administrator to grant Foundry User on the target project."
		if local, ok := errors.AsType[*azdext.LocalError](diagnostic); ok {
			message = local.Message + ". " + local.Suggestion + "; continuing with the requested operation."
		}
		if options.Warn != nil {
			options.Warn(message)
		}
		return nil, nil
	}
	probe, err := access.CollectionReadProbe(options.Collection, options.APIVersion, nil)
	if err != nil {
		return nil, translate(err)
	}
	target, err := access.ResolveEnvironment(ctx, environment, access.ContextOptions{
		Endpoint: options.Endpoint, EnvironmentName: options.EnvironmentName, SkipKey: "AZD_AGENT_SKIP_ROLE_ASSIGNMENTS",
	})
	if err != nil {
		return warn(err)
	}
	if target == nil {
		return nil, nil
	}
	service, err := newEnsurer()
	if err != nil {
		return warn(err)
	}
	policy := access.CheckOnly
	if options.AutoGrant {
		policy = access.GrantFoundryUser
	}
	result, err := service.Ensure(ctx, access.Request{
		Target: *target, Operation: options.Collection + ".read", Policy: policy, Probe: probe,
	})
	if err != nil || result.State != access.Allowed {
		return warn(err)
	}
	return result.Project.Credential, nil
}

type environment struct {
	client azdext.EnvironmentServiceClient
}

func (e environment) CurrentName(ctx context.Context) (string, error) {
	response, err := e.client.GetCurrent(ctx, &azdext.EmptyRequest{})
	if status.Code(err) == codes.NotFound || status.Code(err) == codes.Unavailable {
		return "", nil // standalone endpoint operation without a selected azd environment
	}
	if err != nil {
		return "", err
	}
	return response.GetEnvironment().GetName(), nil
}

func (e environment) Value(ctx context.Context, name, key string) (string, error) {
	response, err := e.client.GetValue(ctx, &azdext.GetEnvRequest{EnvName: name, Key: key})
	if err != nil {
		return "", err
	}
	return response.GetValue(), nil
}

type identity struct{ client azdext.AccountServiceClient }

func (i identity) ResolveTenant(ctx context.Context, subscription string) (string, error) {
	response, err := i.client.LookupTenant(ctx, &azdext.LookupTenantRequest{SubscriptionId: subscription})
	if err != nil {
		return "", err
	}
	return response.GetTenantId(), nil
}

func (i identity) ResolvePrincipal(ctx context.Context, subscription string) (access.Principal, error) {
	response, err := i.client.GetCurrentPrincipal(ctx, &azdext.GetCurrentPrincipalRequest{SubscriptionId: subscription})
	if err != nil {
		return access.Principal{}, err
	}
	principal := access.Principal{ObjectID: response.GetObjectId()}
	switch response.GetPrincipalType() {
	case azdext.PrincipalType_PRINCIPAL_TYPE_USER:
		principal.Type = "User"
	case azdext.PrincipalType_PRINCIPAL_TYPE_SERVICE_PRINCIPAL:
		principal.Type = "ServicePrincipal"
	default:
		return access.Principal{}, fmt.Errorf("host returned an unsupported principal type")
	}
	return principal, nil
}

func translate(err error) error {
	shared, ok := errors.AsType[*access.Error](err)
	if !ok {
		return err
	}
	category := azdext.LocalErrorCategoryUser
	if shared.Kind == access.Remote {
		category = azdext.LocalErrorCategoryInternal
	}
	return &azdext.LocalError{
		Message: shared.Message, Code: string(shared.Code), Category: category, Suggestion: shared.Suggestion,
	}
}
