// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"log"
	"strings"

	"azureaieval/internal/foundry/projectctx"
	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/dataset_api"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// projectEndpointEnvKey is the azd environment key holding the Foundry project
// endpoint the data-plane clients target.
const projectEndpointEnvKey = "FOUNDRY_PROJECT_ENDPOINT"

// evalContext carries everything the commands need to reach the data plane.
type evalContext struct {
	azdClient *azdext.AzdClient
	endpoint  string
	envName   string
	cred      azcore.TokenCredential

	evalClient    *eval_api.EvalClient
	datasetClient *dataset_api.DatasetClient

	// Held only once both listings succeed; a partial read is not reusable.
	schemas map[string]*eval_api.EvaluatorSummary
}

// newEvalContext resolves the project endpoint and builds the data-plane
// clients. The resolution order is projectctx's, so that every Foundry
// extension answers the same question the same way:
//
//  1. --project-endpoint
//  2. the active azd environment (FOUNDRY_PROJECT_ENDPOINT, then AZURE_AI_PROJECT_ENDPOINT)
//  3. global config: extensions.ai-agents.project.context.endpoint
//  4. the host environment variables of the same two names
//  5. otherwise an error naming how to set one
func newEvalContext(ctx context.Context, endpointFlag string) (*evalContext, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return nil, messages.ConnectingToAzd(err)
	}

	ec := &evalContext{azdClient: azdClient}

	// The environment name is resolved regardless of where the endpoint comes
	// from: it is what the cached eval and run ids are read from and
	// written to. Deriving it only when the endpoint came from azd meant
	// --project-endpoint silently disabled that cache.
	_, envName := lookupEndpointFromAzd(ctx, azdClient)
	ec.envName = envName

	resolved, err := projectctx.Resolve(ctx, projectctx.ResolveOpts{FlagValue: endpointFlag})
	if err != nil {
		// The caller only defers Close on a context it was handed, so every
		// path that abandons this one has to close it here.
		ec.Close()
		return nil, err
	}
	ec.endpoint = strings.TrimSuffix(resolved.Endpoint, "/")
	log.Printf("[endpoint] resolved from %s", resolved.Source)

	cred, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{},
	)
	if err != nil {
		ec.Close()
		return nil, messages.CreatingCredential(err)
	}
	ec.cred = cred

	ec.evalClient = eval_api.NewEvalClient(ec.endpoint, cred)
	ec.datasetClient = dataset_api.NewDatasetClient(ec.endpoint, cred)

	return ec, nil
}

// lookupEndpointFromAzd reads the endpoint from the active azd environment,
// returning empty strings when azd has no current environment.
func lookupEndpointFromAzd(ctx context.Context, azdClient *azdext.AzdClient) (endpoint, envName string) {
	envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil || envResp == nil || envResp.Environment == nil {
		return "", ""
	}
	val, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envResp.Environment.Name,
		Key:     projectEndpointEnvKey,
	})
	if err != nil || val == nil || val.Value == "" {
		return "", envResp.Environment.Name
	}
	return val.Value, envResp.Environment.Name
}

// errNoAzdEnvironment reports that there is no azd environment to persist into.
//
// The atomic commands are meant to work standalone against the data plane, so
// running outside a project is ordinary rather than a problem worth reporting.
// A write that fails for any other reason still is.
var errNoAzdEnvironment = messages.ErrNoAzdEnvironment

// setEnvValue persists a value into the active azd environment. azd itself
// writes none of these keys — the extension owns them.
func (ec *evalContext) setEnvValue(ctx context.Context, key, value string) error {
	if ec.envName == "" {
		envResp, err := ec.azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
		if err != nil || envResp == nil || envResp.Environment == nil {
			return messages.NoAzdEnvironmentToWrite(key)
		}
		ec.envName = envResp.Environment.Name
	}
	_, err := ec.azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: ec.envName,
		Key:     key,
		Value:   value,
	})
	if err != nil {
		return messages.WritingEnvValue(key, err)
	}
	return nil
}

// getEnvValue reads a value from the active azd environment, returning empty
// when it is unset.
func (ec *evalContext) getEnvValue(ctx context.Context, key string) string {
	if ec.envName == "" {
		return ""
	}
	val, err := ec.azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: ec.envName,
		Key:     key,
	})
	if err != nil || val == nil {
		return ""
	}
	return val.Value
}

// appInsightsEnvKey is where a connected Application Insights resource lands in
// the azd environment. azd's own provisioning writes it, and the agents
// extension reads the same key to pass tracing configuration to a running
// agent, so its presence is the project's answer to "are traces being
// collected?".
const appInsightsEnvKey = "APPLICATIONINSIGHTS_CONNECTION_STRING"

// defaultGenerationSource picks what `dataset generate` sends when --from was
// not given, from the Application Insights connection string the project has
// (or has not) been given.
//
// Traces are the better dataset when they exist, being real conversations
// rather than synthesized ones, so they win whenever the project is wired to
// collect them. Outside a project, or in one with no Application Insights,
// there are no traces to ask for and the agent's own definition is all that is
// left.
func defaultGenerationSource(appInsightsConnection string) []string {
	if appInsightsConnection != "" {
		return []string{project.GenerateFromTraces}
	}
	return []string{project.GenerateFromAgent}
}

func (ec *evalContext) Close() {
	if ec.azdClient != nil {
		ec.azdClient.Close()
	}
}

// projectARMIDEnvKey holds the project's ARM resource ID, which is what the
// Foundry portal addresses a project by. azd provisioning writes it, and the
// agents extension reads the same key to build the same links.
const projectARMIDEnvKey = "AZURE_AI_PROJECT_ID"

// portalPrefix builds the Foundry portal prefix for this project, or nil when
// the project cannot be addressed.
//
// Best effort by design: a portal link is a convenience on top of a command
// that already did its work, so a missing or unparseable resource ID drops the
// line rather than failing the command that earned it.
func (ec *evalContext) portalPrefix(ctx context.Context) *eval_api.PortalPrefix {
	armID := ec.getEnvValue(ctx, projectARMIDEnvKey)
	if armID == "" {
		return nil
	}
	prefix, err := eval_api.NewPortalPrefix(armID)
	if err != nil {
		log.Printf("[portal] %s is not a project resource ID: %v", projectARMIDEnvKey, err)
		return nil
	}
	return prefix
}

// withPortalLink stamps a run with its portal URL, so the terminal and `-o json`
// answer with the same link from one place.
func (ec *evalContext) withPortalLink(
	ctx context.Context,
	evalID string,
	run *eval_api.OpenAIEvalRun,
) *eval_api.OpenAIEvalRun {
	if run == nil || evalID == "" || run.ID == "" {
		return run
	}
	if prefix := ec.portalPrefix(ctx); prefix != nil {
		run.PortalURL = prefix.EvalRunURL(evalID, run.ID)
	}
	return run
}

// azd environment keys written by this extension.
const (
	envKeyEvalID            = "EVAL_ID"
	envKeyEvalRunID         = "EVAL_RUN_ID"
	envKeyDatasetVersion    = "EVAL_DATASET_VERSION"
	envKeyFingerprintPrefix = "EVAL_FINGERPRINT_"
	// envKeyEvalPath records where `init` put the configuration, so the
	// commands that read it afterwards do not each need --path repeated.
	envKeyEvalPath = "EVAL_CONFIG_PATH"
)

// evalDir resolves where the configuration lives:
//
//  1. --path
//  2. the path `init` recorded in the azd environment
//  3. ./evals
//
// The middle level is what stops `--path` from having to be repeated on every
// later command. Without it, `init --path ./quality` wrote a configuration that
// `run` then looked for under ./evals and reported as missing -- while
// azure.yaml's $ref pointed at it correctly the whole time.
func (ec *evalContext) evalDir(ctx context.Context, flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if recorded := ec.getEnvValue(ctx, envKeyEvalPath); recorded != "" {
		return recorded
	}
	return project.DefaultEvalDir
}
