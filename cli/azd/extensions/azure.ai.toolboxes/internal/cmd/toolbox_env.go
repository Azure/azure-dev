// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"

	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/foundry/envkey"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// setToolboxEndpointEnvFunc is a seam so the create/delete cores can be
// unit-tested without a live azd gRPC server.
var setToolboxEndpointEnvFunc = setToolboxEndpointEnv

// setToolboxEndpointEnv writes value under the toolbox's MCP-endpoint key. An
// empty value clears it (there is no RPC to delete a .env key, so delete blanks
// instead). Best-effort: when no azd environment is in play (no daemon, or no
// default environment selected) the write is skipped rather than failing the
// toolbox operation.
func setToolboxEndpointEnv(ctx context.Context, toolboxName, value, projectScope string) error {
	commitKey := envkey.ToolboxMCPEndpoint(toolboxName)

	return withAzdClient(func(c *azdext.AzdClient) error {
		envResp, err := c.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
		if err != nil {
			if isNoAzdEnvironment(err) {
				log.Printf("toolbox env sync: no azd environment, skipping %s", commitKey)
				return nil
			}
			return exterrors.Internal(
				exterrors.CodeAzdClientFailed,
				fmt.Sprintf("failed to read current azd environment: %s", err),
			)
		}

		envName := envResp.Environment.Name
		setValue := func(key, envValue string) error {
			if _, err := c.Environment().SetValue(ctx, &azdext.SetEnvRequest{
				EnvName: envName, Key: key, Value: envValue,
			}); err != nil {
				return exterrors.Internal(exterrors.CodeAzdClientFailed,
					fmt.Sprintf("failed to set %s in the azd environment: %s", key, err))
			}
			return nil
		}
		if value == "" {
			projectKey := envkey.ToolboxProjectEndpoint(toolboxName)
			projectResp, err := c.Environment().GetValue(ctx, &azdext.GetEnvRequest{
				EnvName: envName, Key: projectKey,
			})
			if err != nil {
				return exterrors.Internal(exterrors.CodeAzdClientFailed,
					fmt.Sprintf("failed to read %s from the azd environment: %s", projectKey, err))
			}
			endpointResp, err := c.Environment().GetValue(ctx, &azdext.GetEnvRequest{
				EnvName: envName, Key: commitKey,
			})
			if err != nil {
				return exterrors.Internal(exterrors.CodeAzdClientFailed,
					fmt.Sprintf("failed to read %s from the azd environment: %s", commitKey, err))
			}
			if !shouldClearToolboxMarkers(projectResp.Value, endpointResp.Value, projectScope) {
				log.Printf("toolbox marker cleanup skipped: %s is empty or scoped to another project", projectKey)
				return nil
			}
			return clearToolboxMarkers(toolboxName, setValue)
		}
		if err := setValue(commitKey, ""); err != nil {
			return err
		}
		projectEndpoint := strings.TrimRight(strings.TrimSpace(projectScope), "/")
		if projectEndpoint == "" {
			projectEndpoint = toolboxProjectEndpoint(value)
		}
		if err := setValue(envkey.ToolboxProjectEndpoint(toolboxName), projectEndpoint); err != nil {
			return err
		}
		return setValue(commitKey, value)
	})
}

func clearToolboxMarkers(toolboxName string, setValue func(string, string) error) error {
	if err := setValue(envkey.ToolboxMCPEndpoint(toolboxName), ""); err != nil {
		return err
	}
	return setValue(envkey.ToolboxProjectEndpoint(toolboxName), "")
}

func toolboxProjectEndpoint(endpoint string) string {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return ""
	}
	const segment = "/toolboxes/"
	index := strings.Index(parsed.Path, segment)
	if index < 0 {
		return ""
	}
	parsed.Path = parsed.Path[:index]
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/")
}

func shouldClearToolboxMarkers(_ string, markerEndpoint, deletionProject string) bool {
	deletionProject = strings.TrimRight(strings.TrimSpace(deletionProject), "/")
	resourceProject := toolboxProjectEndpoint(markerEndpoint)
	return deletionProject != "" && strings.EqualFold(resourceProject, deletionProject)
}

// isNoAzdEnvironment reports whether err from GetCurrent means there is no azd
// environment to write to: the daemon is unreachable (codes.Unavailable), no
// default environment is selected, or no azd project exists. The host returns
// the latter two as status-less sentinels that gRPC surfaces as codes.Unknown,
// so we match on the message text (as azure.ai.agents does).
func isNoAzdEnvironment(err error) bool {
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	if st.Code() == codes.Unavailable {
		return true
	}
	msg := strings.ToLower(st.Message())
	return strings.Contains(msg, "default environment not found") ||
		strings.Contains(msg, "no project exists")
}
