// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"text/tabwriter"
	"time"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	projectpkg "azureaiagent/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
)

// agentListPageSize is the page size requested from the agent list APIs. Every
// page is followed, so this only bounds the size of each request.
const agentListPageSize int32 = 100

type agentLister interface {
	ListAgents(context.Context, *agent_api.ListAgentQueryParameters, string) (*agent_api.AgentList, error)
}

type agentVersionLister interface {
	ListAgentVersions(
		context.Context, string, *agent_api.CommonPageQueryParameters, string,
	) (*agent_api.AgentVersionList, error)
}

// agentListResult is the stable JSON shape of `azd ai agent show` without an agent name.
type agentListResult struct {
	ProjectEndpoint string             `json:"project_endpoint"`
	EndpointSource  EndpointSource     `json:"endpoint_source"`
	Data            []agentListSummary `json:"data"`
}

type agentListSummary struct {
	Name          string `json:"name"`
	ID            string `json:"id,omitempty"`
	Kind          string `json:"kind,omitempty"`
	LatestVersion string `json:"latest_version,omitempty"`
	Status        string `json:"status,omitempty"`
	CreatedAt     string `json:"created_at,omitempty"`
}

// agentVersionListResult is the stable JSON shape of `azd ai agent show --versions`.
type agentVersionListResult struct {
	ProjectEndpoint string                `json:"project_endpoint"`
	AgentName       string                `json:"agent_name"`
	Data            []agentVersionSummary `json:"data"`
}

type agentVersionSummary struct {
	Version     string `json:"version"`
	ID          string `json:"id,omitempty"`
	Status      string `json:"status,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	Description string `json:"description,omitempty"`
}

// listAllAgents follows every page so results are never silently truncated.
func listAllAgents(ctx context.Context, client agentLister, projectEndpoint string) ([]agent_api.AgentObject, error) {
	var all []agent_api.AgentObject
	var after *string
	for {
		page, err := client.ListAgents(ctx, &agent_api.ListAgentQueryParameters{
			Limit: new(agentListPageSize),
			After: after,
		}, DefaultAgentAPIVersion)
		if err != nil {
			return nil, classifyListAgentsError(err, projectEndpoint)
		}
		all = append(all, page.Data...)
		next, err := nextAgentListCursor(page.CommonListObjectProperties, after)
		if err != nil || next == nil {
			return all, err
		}
		after = next
	}
}

// listAllAgentVersions follows every page of an agent's version history.
func listAllAgentVersions(
	ctx context.Context, client agentVersionLister, target *AgentContext, apiVersion string,
) ([]agent_api.AgentVersionObject, error) {
	var all []agent_api.AgentVersionObject
	var after *string
	for {
		page, err := client.ListAgentVersions(ctx, target.Name, &agent_api.CommonPageQueryParameters{
			Limit: new(agentListPageSize),
			After: after,
		}, apiVersion)
		if err != nil {
			return nil, classifyListAgentVersionsError(err, target)
		}
		all = append(all, page.Data...)
		next, err := nextAgentListCursor(page.CommonListObjectProperties, after)
		if err != nil || next == nil {
			return all, err
		}
		after = next
	}
}

// nextAgentListCursor returns the cursor for the next page, nil when the list is
// complete, or an error when the service reports more data without a new cursor.
func nextAgentListCursor(page agent_api.CommonListObjectProperties, previous *string) (*string, error) {
	if !page.HasMore {
		return nil, nil
	}
	if page.LastID == "" || (previous != nil && *previous == page.LastID) {
		return nil, exterrors.Internal(
			exterrors.CodeInvalidServiceResponse,
			"the service reported more results without a usable continuation cursor; "+
				"the list is incomplete",
		)
	}
	return &page.LastID, nil
}

func classifyListAgentsError(err error, projectEndpoint string) error {
	if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
		switch respErr.StatusCode {
		case http.StatusNotFound:
			return exterrors.Validation(
				exterrors.CodeProjectNotFound,
				fmt.Sprintf("Foundry project %s was not found", projectEndpoint),
				"check the project endpoint; it has the form "+
					"https://<account>.services.ai.azure.com/api/projects/<project>",
			)
		case http.StatusUnauthorized, http.StatusForbidden:
			return withServiceSuggestion(exterrors.ServiceFromAzure(err, exterrors.OpListAgents),
				"confirm your signed-in account can read agents in this Foundry project "+
					"(for example, the Azure AI User role)")
		}
	}
	return exterrors.ServiceFromAzure(err, exterrors.OpListAgents)
}

func classifyListAgentVersionsError(err error, target *AgentContext) error {
	if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
		switch respErr.StatusCode {
		case http.StatusNotFound:
			return classifyDirectAgentError(err, target)
		case http.StatusUnauthorized, http.StatusForbidden:
			return withServiceSuggestion(exterrors.ServiceFromAzure(err, exterrors.OpListAgentVersions),
				"confirm your signed-in account can read agents in this Foundry project "+
					"(for example, the Azure AI User role)")
		}
	}
	return exterrors.ServiceFromAzure(err, exterrors.OpListAgentVersions)
}

func formatUnixTime(seconds int64) string {
	if seconds == 0 {
		return ""
	}
	return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
}

func agentDefinitionKind(version agent_api.AgentVersionObject) string {
	definition, _ := version.Definition.(map[string]any)
	kind, _ := definition["kind"].(string)
	return kind
}

func newAgentListResult(
	projectEndpoint string, source EndpointSource, agents []agent_api.AgentObject,
) *agentListResult {
	result := &agentListResult{
		ProjectEndpoint: projectEndpoint,
		EndpointSource:  source,
		Data:            make([]agentListSummary, 0, len(agents)),
	}
	for _, agent := range agents {
		latest := agent.Versions.Latest
		result.Data = append(result.Data, agentListSummary{
			Name:          agent.Name,
			ID:            agent.ID,
			Kind:          agentDefinitionKind(latest),
			LatestVersion: latest.Version,
			Status:        latest.Status,
			CreatedAt:     formatUnixTime(latest.CreatedAt),
		})
	}
	return result
}

func newAgentVersionListResult(
	target *AgentContext, versions []agent_api.AgentVersionObject,
) *agentVersionListResult {
	result := &agentVersionListResult{
		ProjectEndpoint: target.ProjectEndpoint,
		AgentName:       target.Name,
		Data:            make([]agentVersionSummary, 0, len(versions)),
	}
	for _, version := range versions {
		summary := agentVersionSummary{
			Version:   version.Version,
			ID:        version.ID,
			Status:    version.Status,
			CreatedAt: formatUnixTime(version.CreatedAt),
		}
		if version.Description != nil {
			summary.Description = *version.Description
		}
		result.Data = append(result.Data, summary)
	}
	return result
}

// runListAgents lists every agent deployed in a Foundry project.
func runListAgents(
	ctx context.Context, client agentLister, projectEndpoint string, source EndpointSource,
	output string, out, errOut io.Writer,
) error {
	agents, err := listAllAgents(ctx, client, projectEndpoint)
	if err != nil {
		if source != SourceFlag {
			err = withListSourceSuggestion(err, fmt.Sprintf(
				"the project endpoint came from %s; pass --project-endpoint to list another project",
				endpointSourceLabel(source)))
		}
		return err
	}
	return printAgentList(newAgentListResult(projectEndpoint, source, agents), output, out, errOut)
}

// withListSourceSuggestion tells the user where an implicitly resolved project
// endpoint came from when the error has no more specific suggestion.
func withListSourceSuggestion(err error, suggestion string) error {
	if localErr, ok := errors.AsType[*azdext.LocalError](err); ok {
		if localErr.Suggestion == "" {
			localErr.Suggestion = suggestion
		}
		return err
	}
	return withServiceSuggestion(err, suggestion)
}

// runListAgentVersions lists every version of one deployed agent.
func runListAgentVersions(
	ctx context.Context, client agentVersionLister, target *AgentContext, apiVersion string,
	output string, out, errOut io.Writer,
) error {
	versions, err := listAllAgentVersions(ctx, client, target, apiVersion)
	if err != nil {
		return err
	}
	return printAgentVersionList(newAgentVersionListResult(target, versions), output, out, errOut)
}

func endpointSourceLabel(source EndpointSource) string {
	switch source {
	case SourceFlag:
		return "--project-endpoint"
	case SourceAzdEnv:
		return "azd environment FOUNDRY_PROJECT_ENDPOINT"
	case SourceGlobalConfig:
		return "azd ai project set"
	case SourceFoundryEnv:
		return "FOUNDRY_PROJECT_ENDPOINT environment variable"
	default:
		return string(source)
	}
}

func printAgentList(result *agentListResult, output string, out, errOut io.Writer) error {
	scope := fmt.Sprintf("Project: %s (from %s)", result.ProjectEndpoint, endpointSourceLabel(result.EndpointSource))
	switch output {
	case "json":
		if len(result.Data) == 0 {
			if _, err := fmt.Fprintf(errOut, "No agents found in project %s.\n", result.ProjectEndpoint); err != nil {
				return err
			}
		}
		return writeIndentedJSON(out, result)
	case "", "table":
		if _, err := fmt.Fprintf(out, "%s\n\n", scope); err != nil {
			return err
		}
		if len(result.Data) == 0 {
			_, err := fmt.Fprintln(out, "No agents found.")
			return err
		}
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tKIND\tLATEST VERSION\tSTATUS\tCREATED")
		fmt.Fprintln(w, "----\t----\t--------------\t------\t-------")
		for _, agent := range result.Data {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				agent.Name, agent.Kind, agent.LatestVersion, agent.Status, agent.CreatedAt)
		}
		return w.Flush()
	default:
		return fmt.Errorf("unsupported output format %q", output)
	}
}

func printAgentVersionList(result *agentVersionListResult, output string, out, errOut io.Writer) error {
	switch output {
	case "json":
		if len(result.Data) == 0 {
			if _, err := fmt.Fprintf(errOut, "No versions found for agent %q.\n", result.AgentName); err != nil {
				return err
			}
		}
		return writeIndentedJSON(out, result)
	case "", "table":
		if _, err := fmt.Fprintf(out, "Agent: %s\nProject: %s\n\n", result.AgentName, result.ProjectEndpoint); err != nil {
			return err
		}
		if len(result.Data) == 0 {
			_, err := fmt.Fprintln(out, "No versions found.")
			return err
		}
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "VERSION\tSTATUS\tCREATED\tDESCRIPTION")
		fmt.Fprintln(w, "-------\t------\t-------\t-----------")
		for _, version := range result.Data {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", version.Version, version.Status, version.CreatedAt, version.Description)
		}
		return w.Flush()
	default:
		return fmt.Errorf("unsupported output format %q", output)
	}
}

func writeIndentedJSON(out io.Writer, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal result to JSON: %w", err)
	}
	_, err = fmt.Fprintln(out, string(data))
	return err
}

// resolveExplicitAgentListEndpoint returns the validated --project-endpoint value when
// `show` is asked to list agents (no [name] argument), or "" when listing was not
// requested explicitly. All checks are local so bad input fails before authentication.
func resolveExplicitAgentListEndpoint(cmd *cobra.Command, flags *showFlags, nameSupplied bool) (string, error) {
	if nameSupplied || !cmd.Flags().Changed(directProjectEndpointFlag) ||
		cmd.Flags().Changed(directAgentEndpointFlag) {
		// resolveDirectAgentTarget reports conflicts between the endpoint flags.
		return "", nil
	}
	if flags.versions {
		return "", exterrors.Validation(
			exterrors.CodeInvalidAgentName,
			"--versions requires the deployed agent name",
			"supply the Foundry agent name as the [name] argument, or omit --versions to list every agent",
		)
	}
	if cmd.Flags().Changed("environment") {
		return "", exterrors.Validation(
			exterrors.CodeConflictingArguments,
			"--environment cannot be combined with --project-endpoint",
			"--project-endpoint does not read azd environment values; remove --environment",
		)
	}
	return validateDirectProjectEndpoint(flags.direct.projectEndpoint)
}

type projectGetter func(
	context.Context, *azdext.EmptyRequest, ...grpc.CallOption,
) (*azdext.GetProjectResponse, error)

func resolveOutsideProjectVersionTarget(
	ctx context.Context, getProject projectGetter, name string,
) (*AgentContext, error) {
	response, err := getProject(ctx, &azdext.EmptyRequest{})
	if err != nil && !isMissingAzdProjectError(err) {
		return nil, err
	}
	if err == nil && response.GetProject() != nil {
		return nil, nil
	}
	if err := agent_yaml.ValidateAgentName(name); err != nil {
		return nil, exterrors.Validation(exterrors.CodeInvalidAgentName,
			fmt.Sprintf("agent name %q is invalid", name), "supply a deployed Foundry agent name")
	}
	scope, err := resolveProjectEndpoint(ctx, resolveProjectEndpointOpts{})
	if err != nil {
		return nil, err
	}
	return &AgentContext{ProjectEndpoint: scope.Endpoint, Name: name}, nil
}

// resolveNamelessShowListScope decides what `show` without a name does when no
// endpoint flag is set. Inside an azd project it returns nil so the existing
// azure.yaml service resolution runs unchanged. Outside one it resolves the
// Foundry project from the azd environment, global config, or
// FOUNDRY_PROJECT_ENDPOINT so show lists that project's agents.
func resolveNamelessShowListScope(
	ctx context.Context, getProject projectGetter, versions bool,
) (*resolvedEndpoint, error) {
	response, err := getProject(ctx, &azdext.EmptyRequest{})
	if err != nil && !isMissingAzdProjectError(err) {
		// Let the project-based path report its own error.
		return nil, nil
	}
	if err == nil && response.GetProject() != nil {
		return nil, nil
	}

	if versions {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAgentName,
			"--versions requires the agent name outside an azd project",
			"supply the Foundry agent name as the [name] argument",
		)
	}
	scope, err := resolveProjectEndpoint(ctx, resolveProjectEndpointOpts{})
	if err != nil {
		if localErr, ok := errors.AsType[*azdext.LocalError](err); ok &&
			localErr.Code == exterrors.CodeMissingProjectEndpoint {
			return nil, exterrors.Dependency(
				exterrors.CodeMissingProjectEndpoint,
				"no Foundry project endpoint is configured",
				"pass --project-endpoint <project-endpoint> to list its agents, "+
					"set a default with `azd ai project set <project-endpoint>`, "+
					"or run from a directory containing azure.yaml",
			)
		}
		return nil, err
	}
	return scope, nil
}

func runShowAgentList(ctx context.Context, scope *resolvedEndpoint, output string) error {
	credential, err := newAgentCredential()
	if err != nil {
		return err
	}
	client := agent_api.NewAgentClient(scope.Endpoint, credential)
	return runListAgents(ctx, client, scope.Endpoint, scope.Source, output, os.Stdout, os.Stderr)
}

func runShowVersions(ctx context.Context, target *AgentContext, output string) error {
	client, err := target.NewClient()
	if err != nil {
		return err
	}
	return runListAgentVersions(ctx, client, target, DefaultAgentAPIVersion, output, os.Stdout, os.Stderr)
}

func runPromptShowVersions(ctx context.Context, flags *showFlags, pctx *promptServiceContext) error {
	client, err := pctx.newClient(ctx)
	if err != nil {
		return err
	}
	target := &AgentContext{ProjectEndpoint: promptAgentEndpoint(pctx.Settings), Name: pctx.AgentName()}
	return runListAgentVersions(
		ctx, client, target, projectpkg.ProjectEndpointAPIVersion, flags.output, os.Stdout, os.Stderr)
}
