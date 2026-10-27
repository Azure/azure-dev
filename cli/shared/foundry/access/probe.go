// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package access

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

var collectionPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// The provider's specific RBAC message identifies the denied principal and data
// action. Generic Forbidden responses, including network/policy 403s, are unknown.
var deniedReadPattern = regexp.MustCompile(
	`(?i)^Identity\(object id:\s*([0-9a-f-]{36})\) does not have permissions for ` +
		`(Microsoft\.CognitiveServices/accounts/AIServices/[a-z0-9_/]+/read) actions\.`,
)

// CollectionReadProbe constructs a non-mutating public-cloud Foundry collection
// probe. A successful response proves only read access, not write permission.
// Collection must be a single path segment. An empty API version defaults to v1.
// Callers for other clouds or capabilities should provide their own Probe.
func CollectionReadProbe(collection, apiVersion string, options *policy.ClientOptions) (Probe, error) {
	if !collectionPattern.MatchString(collection) {
		return nil, failure(InvalidOptions, "a single Foundry collection name is required", nil)
	}
	if apiVersion == "" {
		apiVersion = "v1"
	}
	query := url.Values{"api-version": []string{apiVersion}}.Encode()
	return func(ctx context.Context, project Project) (State, error) {
		client, err := azcore.NewClient("foundry-shared-access", "v0.1.0", runtime.PipelineOptions{
			PerRetry: []policy.Policy{runtime.NewBearerTokenPolicy(
				project.Credential, []string{"https://ai.azure.com/.default"}, nil,
			)},
		}, options)
		if err != nil {
			return Unknown, failure(ProbeFailed, "creating Foundry access probe client", err)
		}
		request, err := runtime.NewRequest(ctx, http.MethodGet,
			strings.TrimRight(project.Endpoint, "/")+"/"+collection+"?"+query)
		if err != nil {
			return Unknown, failure(ProbeFailed, "constructing Foundry access probe", err)
		}
		response, err := client.Pipeline().Do(request)
		if err != nil {
			return Unknown, failure(ProbeFailed, "performing Foundry access probe", err)
		}
		defer response.Body.Close()
		if response.StatusCode == http.StatusOK {
			return Allowed, nil
		}
		if response.StatusCode != http.StatusForbidden {
			return Unknown, failure(ProbeFailed, "Foundry access probe was not successful", runtime.NewResponseError(response))
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
	}, nil
}
