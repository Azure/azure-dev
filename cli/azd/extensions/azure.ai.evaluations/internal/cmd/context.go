// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"azureaieval/internal/foundry/projectctx"
	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/dataset_api"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
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

	// Resolved on first use. Which command deploys cannot change while one
	// command runs, and asking azd costs a round trip.
	deployCmd string

	// Private reconciliation state, read once per command and written through.
	// state is nil until it has been loaded, which is what tells an unread
	// store from one that is genuinely empty.
	configHelper *azdext.ConfigHelper
	state        map[string]string
}

// privateStatePath is the one environment-config section this extension owns.
//
// This state used to be ordinary azd environment values, so `azd env
// get-values` handed a reader fingerprints, rename indexes and per-object
// caches alongside their own configuration, and every hook received them. It is
// not configuration: it exists so an immutable artifact is not republished, and
// nobody sets it by hand.
//
// Environment config rather than a file of our own: it lives in the
// environment's config.json, so it is scoped per environment, travels with
// azd's remote environment sync, is removed when the environment is, and is not
// returned by `azd env get-values`. A separate file under .azure would sync
// with none of that and would need its own cleanup lifecycle.
const privateStatePath = "eval.state"

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

	cred, err := newAzdTokenCredential()
	if err != nil {
		ec.Close()
		return nil, err
	}
	ec.cred = cred

	ec.evalClient = eval_api.NewEvalClient(ec.endpoint, ec.cred)
	ec.datasetClient = dataset_api.NewDatasetClient(ec.endpoint, ec.cred)

	return ec, nil
}

// newAzdTokenCredential returns the azd credential already wrapped in its
// retry. Handing back the wrapper rather than the raw credential is what keeps
// the retry wired: an earlier version assigned the wrapper to the context and
// then built both clients from the unwrapped one, so nothing retried.
func newAzdTokenCredential() (azcore.TokenCredential, error) {
	cred, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{},
	)
	if err != nil {
		return nil, messages.CreatingCredential(err)
	}
	return azdTokenRetry{inner: cred}, nil
}

// azdTokenRetry retries a failed token request once. azidentity gives the azd
// subprocess a fixed 10 second timeout and discards its stderr, so an azd that
// overruns surfaces as "exit status 1" with no cause; the next call usually
// finds a warm token. Without this a slow token turns into a failed command.
type azdTokenRetry struct{ inner azcore.TokenCredential }

func (c azdTokenRetry) GetToken(
	ctx context.Context,
	opts policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	tok, err := c.inner.GetToken(ctx, opts)
	if err == nil || ctx.Err() != nil {
		return tok, err
	}
	log.Printf("[auth] token request failed (%v); retrying once", err)
	return c.inner.GetToken(ctx, opts)
}

// lookupEndpointFromAzd reads the endpoint from the active azd environment,
// returning empty strings when azd has no current environment.
// azdEnvironmentName is the environment this invocation acts on: the one
// -e/--environment named, or azd's current one when it named none.
//
// Answered here because it was answered independently in five places and
// -e was honoured by none of them. `azd ai eval create -e staging` read its
// endpoint out of the default environment and wrote its eval id back there,
// and `-e a-name-azd-rejects` was accepted in silence.
//
// Empty means there is no environment to act on, which is ordinary: the atomic
// commands work standalone against the data plane.
func azdEnvironmentName(ctx context.Context, azdClient *azdext.AzdClient) string {
	if name := projectctx.SelectedEnvironment(ctx); name != "" {
		return name
	}
	envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil || envResp.GetEnvironment() == nil {
		return ""
	}
	return envResp.Environment.Name
}

func lookupEndpointFromAzd(ctx context.Context, azdClient *azdext.AzdClient) (endpoint, envName string) {
	envName = azdEnvironmentName(ctx, azdClient)
	if envName == "" {
		return "", ""
	}
	val, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envName,
		Key:     projectEndpointEnvKey,
	})
	if err != nil || val == nil || val.Value == "" {
		return "", envName
	}
	return val.Value, envName
}

// errNoAzdEnvironment reports that there is no azd environment to persist into.
//
// The atomic commands are meant to work standalone against the data plane, so
// running outside a project is ordinary rather than a problem worth reporting.
// A write that fails for any other reason still is.
var errNoAzdEnvironment = messages.ErrNoAzdEnvironment

// remember persists a value that the extension can recover without, so a
// failure to store it must not fail the work that produced it.
//
// Running outside a project is ordinary -- the atomic commands are meant to
// work standalone against the data plane -- so having nowhere to write is not
// worth a word. Anything else is: these keys are how a later deploy recognizes
// what it already published, and losing one silently means the next `azd up`
// creates a second immutable version of something it had already created.
//
// Written to stderr, not through log: the standard logger is pointed at
// io.Discard unless --debug, so logging this would be the same silence with a
// more reassuring name. stderr keeps `-o json` on stdout parseable. azd does
// not surface an extension's stderr, so under `azd up` this reaches the debug
// log and no further -- direct invocations are where it shows.
func (ec *evalContext) remember(ctx context.Context, key, value string) {
	err := ec.setPrivate(ctx, key, value)
	if err == nil || errors.Is(err, errNoAzdEnvironment) {
		return
	}
	fmt.Fprint(os.Stderr, messages.Warning(err))
	log.Printf("[env] could not record %s: %v", key, err)
}

// loadPrivateState reads the whole section once per command.
//
// A miss is cached as an empty map rather than retried: the caller reads a
// dozen keys, and asking azd for each one turns a local lookup into a dozen
// round trips.
func (ec *evalContext) loadPrivateState(ctx context.Context) map[string]string {
	if ec.state != nil {
		return ec.state
	}
	ec.state = map[string]string{}

	helper, err := ec.config()
	if err != nil {
		return ec.state
	}
	stored := map[string]string{}
	if found, err := helper.GetEnvJSON(ctx, privateStatePath, &stored); err == nil && found {
		ec.state = stored
	}
	return ec.state
}

// config builds the environment-config accessor once.
func (ec *evalContext) config() (*azdext.ConfigHelper, error) {
	if ec.configHelper != nil {
		return ec.configHelper, nil
	}
	if ec.azdClient == nil {
		return nil, errNoAzdEnvironment
	}
	helper, err := azdext.NewConfigHelper(ec.azdClient)
	if err != nil {
		return nil, err
	}
	ec.configHelper = helper
	return helper, nil
}

// setPrivate records one entry of reconciliation state.
//
// The whole section is rewritten because azd's config store is addressed by
// path and this extension keeps its state as one object; the alternative is a
// config path per key, which puts the same sprawl in a different file.
func (ec *evalContext) setPrivate(ctx context.Context, key, value string) error {
	helper, err := ec.config()
	if err != nil {
		return messages.NoAzdEnvironmentToWrite(key)
	}
	state := ec.loadPrivateState(ctx)
	if state[key] == value {
		return nil
	}
	previous, had := state[key]
	state[key] = value
	if err := helper.SetEnvJSON(ctx, privateStatePath, state); err != nil {
		// The in-memory copy goes back to what it was, so a later read in this
		// same command reports what is still persisted. Deleting the key
		// instead dropped a value the write never touched, and a resource that
		// reads as untracked gets another immutable version published for it.
		if had {
			state[key] = previous
		} else {
			delete(state, key)
		}
		return messages.WritingEnvValue(key, err)
	}
	return nil
}

// privateValue reads one entry of reconciliation state.
func (ec *evalContext) privateValue(ctx context.Context, key string) string {
	return ec.loadPrivateState(ctx)[key]
}

// confirmedNoAzdEnvironment reports that azd answered, and the answer was that
// there is no current environment.
//
// ec.envName being empty is not that answer. It is left empty by any failure to
// reach azd as well as by there being no environment, so reading it as "there
// is none" turns a transient gRPC hiccup into advice to create an environment
// the user already has.
//
// The environment name is recovered here when there turns out to be one, so a
// caller whose earlier lookup came up empty because of a hiccup can retry it.
func (ec *evalContext) confirmedNoAzdEnvironment(ctx context.Context) bool {
	if ec.envName != "" {
		return false
	}
	if ec.azdClient == nil {
		// No azd to ask: running standalone against the data plane, where
		// there is genuinely nowhere to have recorded an id.
		return true
	}
	envResp, err := ec.azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return isNoDefaultEnvironmentError(err)
	}
	if envResp == nil || envResp.Environment == nil || envResp.Environment.Name == "" {
		return true
	}
	ec.envName = envResp.Environment.Name
	return false
}

// isNoDefaultEnvironmentError picks azd's "there is no environment to record
// anything in" out of every other reason the call could have failed.
//
// The distinction is the whole point: a transport failure must not be reported
// as a missing environment, or a gRPC hiccup tells the user to create one they
// already have.
//
// Written as the cascade's own rule minus the one case the two disagree on,
// rather than as a second list of azd's sentinels. Keeping a second list is how
// `no project exists` came to be handled in the cascade and missed here, which
// told anyone running outside a project to publish an eval that already exists.
func isNoDefaultEnvironmentError(err error) bool {
	if err == nil {
		return false
	}
	return projectctx.HostedSourceAbsent(err) && !projectctx.DaemonUnreachable(err)
}

// getEnvValue reads a value from the active azd environment, returning empty
// when it is unset.
func (ec *evalContext) getEnvValue(ctx context.Context, key string) string {
	if ec.envName == "" || ec.azdClient == nil {
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

// deployCommand names the command that publishes this project's evals.
//
// `azd up` provisions before it deploys, so it only works where there is
// infrastructure to provision. Evals are data-plane only, so a project that
// ships none fails compiling a missing infra/main.bicep and never reaches
// them -- naming `azd up` there hands the reader a failure instead of a fix.
func (ec *evalContext) deployCommand(ctx context.Context) string {
	if ec.deployCmd != "" {
		return ec.deployCmd
	}

	proj, err := ec.azdProject(ctx)
	name := deployCommandName(proj)
	if err != nil {
		// A project we could not read is not a project without infrastructure.
		// Answer for this call, but do not cache what a transport failure said:
		// one hiccup would otherwise downgrade the advice for the whole process.
		return name
	}
	ec.deployCmd = name
	return name
}

// azdProject reads the project azd is running against. A nil project with no
// error means azd answered and there is none; an error means it did not answer.
func (ec *evalContext) azdProject(ctx context.Context) (*azdext.ProjectConfig, error) {
	if ec.azdClient == nil {
		return nil, nil
	}
	resp, err := ec.azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return nil, err
	}
	return resp.GetProject(), nil
}

// deployCommandName is projectCanProvision phrased as the command to run.
//
// Without infrastructure the answer is this extension's own command rather than
// `azd deploy`. Deploy refuses with "infrastructure has not been provisioned"
// in an environment that has never provisioned one, which is exactly the
// scratch project `azd init --minimal` produces and an eval gets scaffolded
// into. `azd ai eval create` reconciles the same configuration needing nothing
// but an endpoint.
func deployCommandName(proj *azdext.ProjectConfig) string {
	if projectCanProvision(proj) {
		return azdUpCommand
	}
	return "azd ai eval create"
}

// azdUpCommand provisions before it deploys. It is named rather than repeated
// because callers have to be able to tell it apart from this extension's own
// commands -- it takes none of their flags.
const azdUpCommand = "azd up"

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
	// envKeyEvalPath records where `init` put the configuration, so the
	// commands that read it afterwards do not each need --path repeated.
	envKeyEvalPath = "EVAL_CONFIG_PATH"
)

// evalDirCascade is the one rule for where the configuration lives:
//
//  1. --path
//  2. the path `init` recorded in the azd environment
//  3. the `$ref` on the `azure.ai.eval` service in azure.yaml
//  4. ./evals
//
// The middle levels are what stop `--path` from having to be repeated on every
// later command. Without the recorded one, `init --path ./quality` wrote a
// configuration that `run` then looked for under ./evals and reported as
// missing -- while azure.yaml's $ref pointed at it correctly the whole time.
//
// That $ref is now read rather than only written, which is what makes the rule
// survive a fresh clone. The recorded path lives in the azd environment, and an
// azd environment is not in the repository: check the project out somewhere
// else and level 2 is empty, so a configuration the project declares perfectly
// well under ./config was reported missing by every command while `azd up`
// deployed it. Reading the declaration is also what keeps one answer to "where
// is the configuration" instead of one for deploy and one for everything else.
//
// This is the whole rule, and every command that reads the configuration goes
// through it. Stating it here and applying it on only some paths is how
// `create` came to report the configuration missing and `generate` came to
// write a second one under ./evals, both in a project where init had recorded
// where it put the first.
//
// recorded tells absence apart from failure, and the two get different
// answers. A project with no azd environment has genuinely recorded nothing,
// so the next level is right. An azd that could not be asked has said nothing
// at all, and defaulting on that would write the second configuration all over
// again -- this time for a reason nobody could reproduce.
//
// declared is best-effort by contrast: outside an azd project there is no
// azure.yaml to read, which is ordinary rather than a failure.
func evalDirCascade(
	flagValue string,
	recorded func() (string, error),
	declared func() string,
) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	path, err := recorded()
	if err != nil {
		return "", err
	}
	if path != "" {
		return path, nil
	}
	if declared != nil {
		if dir := declared(); dir != "" {
			return dir, nil
		}
	}
	return project.DefaultEvalDir, nil
}

// declaredEvalConfig reads the location azure.yaml's `$ref` points at.
//
// The service entry is the project's own statement of where its evaluation
// configuration lives, and `azd up` has always deployed from it. The full path
// is returned rather than its directory: the `$ref` names a file, and a project
// declaring `./config/nightly.yaml` means that file, not whatever
// `azure.eval.yaml` happens to sit beside it.
//
// Returns empty outside an azd project, or when nothing declares the eval host.
func declaredEvalConfig(ctx context.Context, azdClient *azdext.AzdClient) string {
	if azdClient == nil {
		return ""
	}
	resp, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil || resp.GetProject() == nil {
		return ""
	}
	for _, svc := range resp.GetProject().GetServices() {
		if svc.GetHost() != project.EvalHost {
			continue
		}
		props := svc.GetAdditionalProperties()
		if props == nil {
			continue
		}
		ref, _ := props.AsMap()["$ref"].(string)
		if ref == "" {
			continue
		}
		return filepath.Clean(filepath.FromSlash(ref))
	}
	return ""
}

// evalDir is the cascade for a command that already holds an azd connection.
func (ec *evalContext) evalDir(ctx context.Context, flagValue string) (string, error) {
	return evalDirCascade(flagValue, func() (string, error) {
		if ec.envName == "" || ec.azdClient == nil {
			return "", nil
		}
		return readRecordedEvalPath(ctx, ec.azdClient, ec.envName)
	}, func() string {
		return declaredEvalConfig(ctx, ec.azdClient)
	})
}

// resolveEvalDir is the cascade for a command that has not built an
// evalContext yet.
//
// `create`, `generate` and `init` all have to find the configuration before
// they resolve a Foundry endpoint, so that a project with no configuration is
// told to run `init` rather than told to set an endpoint. The extra azd
// connection is local and short-lived, and is what buys that ordering.
func resolveEvalDir(ctx context.Context, flagValue string) (string, error) {
	return evalDirCascade(flagValue, func() (string, error) {
		azdClient, err := azdext.NewAzdClient()
		if err != nil {
			// Nothing to ask. This extension is spawned by azd, so the case
			// that reaches here is a test or a direct invocation, neither of
			// which has an environment holding a recorded path.
			return "", nil
		}
		defer azdClient.Close()

		if name := projectctx.SelectedEnvironment(ctx); name != "" {
			return readRecordedEvalPath(ctx, azdClient, name)
		}

		env, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
		if err != nil {
			// The same rule the endpoint cascade uses: azd saying "there is no
			// project or environment" is an answer, and anything else is not.
			if isNoDefaultEnvironmentError(err) {
				return "", nil
			}
			return "", err
		}
		if env.GetEnvironment() == nil {
			return "", nil
		}
		return readRecordedEvalPath(ctx, azdClient, env.GetEnvironment().GetName())
	}, func() string {
		azdClient, err := azdext.NewAzdClient()
		if err != nil {
			return ""
		}
		defer azdClient.Close()
		return declaredEvalConfig(ctx, azdClient)
	})
}

// readRecordedEvalPath reads back what recordEvalPath wrote, distinguishing a
// key that was never set from a read that failed.
func readRecordedEvalPath(
	ctx context.Context,
	client *azdext.AzdClient,
	envName string,
) (string, error) {
	val, err := client.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envName,
		Key:     envKeyEvalPath,
	})
	if err != nil {
		// An unset key is an answer; init records the path best effort and
		// succeeds without an azd environment to record it in.
		if isNoDefaultEnvironmentError(err) {
			return "", nil
		}
		return "", err
	}
	if val == nil {
		return "", nil
	}
	return val.Value, nil
}
