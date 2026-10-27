// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundryaccess

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/grpc"
)

// PrepareOptions describes an extension's read-access prerequisite. The intended
// cloud operation still performs its own authorization; read access is not write access.
type PrepareOptions struct {
	Endpoint string
	// EnvironmentName preserves an explicit -e selection. Empty uses the current environment.
	EnvironmentName string
	// Collection is a single Foundry collection segment, such as "toolboxes" or "skills".
	Collection string
	// APIVersion selects the collection's contract. Empty defaults to v1.
	APIVersion string
	// AutoGrant preserves the agent extension's automatic-grant experience for
	// deploy/mutation commands. Callers must document this behavior. Reads, previews,
	// local authoring, and diagnostics must leave it false or omit Prepare entirely.
	AutoGrant bool
	// Warn receives best-effort access/grant failures; no console output is produced here.
	Warn func(string)
}

type environmentClient interface {
	GetCurrent(context.Context, *azdext.EmptyRequest, ...grpc.CallOption) (*azdext.EnvironmentResponse, error)
	GetValue(context.Context, *azdext.GetEnvRequest, ...grpc.CallOption) (*azdext.KeyValueResponse, error)
}

type accessEnsurer interface {
	Ensure(context.Context, Request) (Result, error)
}

// Prepare checks a verified environment-bound project's read-access prerequisite
// and returns its tenant-scoped credential when available. It is warning-only
// except for cancellation: unknown access does not prevent an extension's original
// operation, and never authorizes a grant. Endpoint-only commands retain their
// existing credential behavior. The caller must pass the resolved endpoint, not
// assume the active environment owns an explicit --project-endpoint value.
// AZD_AGENT_SKIP_ROLE_ASSIGNMENTS=true (azd or process environment) bypasses checks.
func Prepare(ctx context.Context, options PrepareOptions) (azcore.TokenCredential, error) {
	if options.Collection == "" || !collectionPattern.MatchString(options.Collection) {
		return nil, fmt.Errorf("a single Foundry collection name is required")
	}
	if skip, _ := strconv.ParseBool(os.Getenv("AZD_AGENT_SKIP_ROLE_ASSIGNMENTS")); skip {
		return nil, nil
	}
	client, err := azdext.NewAzdClient()
	if err != nil {
		return nil, ctx.Err() // standalone operation without an azd host
	}
	defer client.Close()
	return prepare(ctx, client.Environment(), options, func() (accessEnsurer, error) {
		backend, err := NewAzureBackend(client.Account(), nil)
		if err != nil {
			return nil, err
		}
		return NewService(backend, nil)
	})
}

var collectionPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func prepare(
	ctx context.Context, environment environmentClient, options PrepareOptions, newEnsurer func() (accessEnsurer, error),
) (azcore.TokenCredential, error) {
	warn := func(_ error) (azcore.TokenCredential, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if options.Warn != nil {
			// Do not expose raw Azure errors, tokens, or credential-bearing URLs.
			options.Warn("Foundry project access could not be ensured; continuing with the requested operation. " +
				"If access is denied, ask an administrator to grant Foundry User on the target project.")
		}
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	envName := options.EnvironmentName
	if envName == "" {
		current, err := environment.GetCurrent(ctx, &azdext.EmptyRequest{})
		if err != nil || current.GetEnvironment().GetName() == "" {
			return nil, ctx.Err() // endpoint-only commands require no azd environment
		}
		envName = current.GetEnvironment().GetName()
	}
	read := func(key string) (string, error) {
		value, err := environment.GetValue(ctx, &azdext.GetEnvRequest{EnvName: envName, Key: key})
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(value.GetValue()), nil
	}
	skip, err := read("AZD_AGENT_SKIP_ROLE_ASSIGNMENTS")
	if err != nil {
		return warn(err)
	}
	if enabled, _ := strconv.ParseBool(skip); enabled {
		return nil, nil
	}
	projectID, err := read("AZURE_AI_PROJECT_ID")
	if err != nil {
		return warn(err)
	}
	if projectID == "" {
		return nil, nil
	}
	// Do not bind flag/global endpoints to unrelated environment resource IDs.
	endpoint, err := read("FOUNDRY_PROJECT_ENDPOINT")
	if err != nil {
		return warn(err)
	}
	if endpoint == "" {
		endpoint, err = read("AZURE_AI_PROJECT_ENDPOINT")
		if err != nil {
			return warn(err)
		}
	}
	if endpoint == "" || !strings.EqualFold(strings.TrimRight(endpoint, "/"), strings.TrimRight(options.Endpoint, "/")) {
		return nil, nil
	}
	ensurer, err := newEnsurer()
	if err != nil {
		return warn(err)
	}
	policy := CheckOnly
	if options.AutoGrant {
		policy = GrantFoundryUser
	}
	result, err := ensurer.Ensure(ctx, Request{
		Target:    Target{ProjectID: projectID, Endpoint: options.Endpoint},
		Operation: options.Collection + ".read", Policy: policy,
		Probe: collectionReadProbeWithOptions(options.Collection, options.APIVersion, nil),
	})
	if err != nil || result.State != Allowed {
		return warn(err)
	}
	return result.Project.Credential, nil
}

// The Foundry authorization error identifies the denied principal and data action.
// Only this specific RBAC message counts as a denial; arbitrary 403s do not.
var deniedReadPattern = regexp.MustCompile(
	`(?i)^Identity\(object id:\s*([0-9a-f-]{36})\) does not have permissions for ` +
		`(Microsoft\.CognitiveServices/accounts/AIServices/[a-z0-9_/]+/read) actions\.`,
)

func collectionReadProbeWithOptions(collection, apiVersion string, options *policy.ClientOptions) Probe {
	if apiVersion == "" {
		apiVersion = "v1"
	}
	query := url.Values{"api-version": []string{apiVersion}}.Encode()
	return func(ctx context.Context, project Project) (State, error) {
		client, err := azcore.NewClient("azd-foundry-access", "v1.0.0", runtime.PipelineOptions{
			PerRetry: []policy.Policy{runtime.NewBearerTokenPolicy(
				project.Credential, []string{"https://ai.azure.com/.default"}, nil,
			)},
		}, options)
		if err != nil {
			return Unknown, err
		}
		request, err := runtime.NewRequest(ctx, http.MethodGet,
			strings.TrimRight(project.Endpoint, "/")+"/"+collection+"?"+query)
		if err != nil {
			return Unknown, err
		}
		response, err := client.Pipeline().Do(request)
		if err != nil {
			return Unknown, err
		}
		defer response.Body.Close()
		if response.StatusCode == http.StatusOK {
			return Allowed, nil
		}
		if response.StatusCode != http.StatusForbidden {
			return Unknown, runtime.NewResponseError(response)
		}
		var body struct {
			Error struct {
				Code, Message string
				InnerError    struct{ Code string }
			}
		}
		if err := json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(&body); err != nil {
			return Unknown, nil
		}
		matches := deniedReadPattern.FindStringSubmatch(body.Error.Message)
		if body.Error.Code == "UserError" && body.Error.InnerError.Code == "ForbiddenError" &&
			len(matches) == 3 && strings.EqualFold(matches[1], project.PrincipalID) {
			return Denied, nil
		}
		return Unknown, nil
	}
}
