// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"sort"
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
	// store from one that is genuinely empty. stateErr holds a read that failed,
	// which is a third thing again: empty to a reader, and unsafe to write over.
	state    map[string]string
	stateErr error

	// Where azure.yaml sits, resolved on first use. rootKnown separates "not
	// asked yet" from "asked, and azd reports no project".
	root      string
	rootKnown bool
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

// azdEnvironmentDirName is azd's own directory under the project root. The lock
// guarding privateStatePath lives here because that is what every eval service
// in the environment shares -- their configurations do not.
const azdEnvironmentDirName = ".azure"

// stateEndpointKey records which Foundry project the rest of the section
// describes.
//
// Everything else in it -- fingerprints, versions, resolved ids -- is only true
// of one project, but the section lives in the azd environment, and an
// environment can be pointed at another endpoint (or run against one named
// with --project-endpoint). State left from the previous project then reported
// a dataset unchanged that the new one had never seen, and the configured file
// was never published.
//
// Two leading underscores because every real key is built from
// project.FingerprintKey or the EVAL_SUBSTANCE_ prefix, so nothing can collide
// with it.
const stateEndpointKey = "__endpoint"

// normalizedEndpoint is the endpoint reduced to what identifies the project, so
// a trailing slash or a change of case does not read as a different one.
func normalizedEndpoint(endpoint string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(endpoint), "/"))
}

// newEvalContext resolves the project endpoint and builds the data-plane
// clients. The resolution order is projectctx's, so that every Foundry
// extension answers the same question the same way:
//
//  1. --project-endpoint
//  2. the active azd environment (FOUNDRY_PROJECT_ENDPOINT, then AZURE_AI_PROJECT_ENDPOINT)
//  3. global config: extensions.ai-projects.context.endpoint
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
	fmt.Fprint(warnWriter(ctx), messages.Warning(err))
	log.Printf("[env] could not record %s: %v", key, err)
}

// forget drops the reconciliation state recorded for a resource that is gone.
//
// A delete used to leave its mappings behind. The state is not shown anywhere,
// so nothing said the id, fingerprint and version of a deleted resource were
// still on file -- and the next deploy read them, matched a fingerprint for
// content the service no longer has, and either bound an eval to a deleted id
// or reported an artifact as already published when nothing had been.
//
// Best effort for the same reason remember is: a delete that succeeded
// remotely is not undone by an environment that could not be written.
func (ec *evalContext) forget(ctx context.Context, keys ...string) {
	err := ec.deletePrivate(ctx, keys...)
	if err == nil || errors.Is(err, errNoAzdEnvironment) {
		return
	}
	fmt.Fprint(warnWriter(ctx), messages.Warning(err))
	log.Printf("[env] could not drop %v: %v", keys, err)
}

// forgetDeletedVersion drops the state recorded for a version that is gone.
//
// Only when the recorded version is the one deleted. A dataset carries many
// versions and the state describes one of them, so clearing it on the removal
// of an older version would report the current one as never published and
// publish it again.
func (ec *evalContext) forgetDeletedVersion(ctx context.Context, kind, name, version string) {
	if ec.privateValue(ctx, versionKey(kind, name)) != version {
		return
	}
	ec.forget(ctx, versionKey(kind, name), project.FingerprintKey(kind, name))
}

// deletePrivate removes entries from the reconciliation section.
//
// It takes the same lock and re-reads the same baseline as setPrivate, and for
// the same reason: the section is rewritten whole, so a delete racing a
// sibling service's write would otherwise drop whatever that one had just
// recorded.
func (ec *evalContext) deletePrivate(ctx context.Context, keys ...string) error {
	if ec.azdClient == nil {
		return messages.NoAzdEnvironmentToWrite(strings.Join(keys, ", "))
	}
	state := ec.loadPrivateState(ctx)
	if ec.stateErr != nil {
		return messages.PrivateStateUnreadable(strings.Join(keys, ", "), ec.stateErr)
	}
	// Nothing recorded is nothing to drop, and taking the lock to rewrite an
	// identical section is a round trip for no change.
	present := false
	for _, key := range keys {
		if _, ok := state[key]; ok {
			present = true
			break
		}
	}
	if !present {
		return nil
	}

	unlock, err := ec.lockPrivateState(ctx)
	if err != nil {
		return err
	}
	defer unlock()

	merged, err := ec.readPrivateState(ctx)
	if err != nil {
		return messages.PrivateStateUnreadable(strings.Join(keys, ", "), err)
	}
	for _, key := range keys {
		delete(merged, key)
	}
	if err := ec.setEnvConfig(ctx, privateStatePath, merged); err != nil {
		return messages.WritingEnvValue(strings.Join(keys, ", "), err)
	}
	ec.state = merged
	return nil
}

// loadPrivateState reads the whole section once per command.
//
// A miss is cached as an empty map rather than retried: the caller reads a
// dozen keys, and asking azd for each one turns a local lookup into a dozen
// round trips.
//
// A read that failed is remembered apart from one that found nothing. It still
// answers empty, because the callers are readers and there is nothing else to
// give them, but it must never be written back: setPrivate replaces the whole
// section, so persisting an empty map that came from a failure would delete
// every id and fingerprint the section already held.
func (ec *evalContext) loadPrivateState(ctx context.Context) map[string]string {
	if ec.state != nil {
		return ec.state
	}
	ec.state = map[string]string{}

	if ec.azdClient == nil {
		return ec.state
	}
	fresh, err := ec.readPrivateState(ctx)
	if err != nil {
		ec.stateErr = err
		// What the section holds is unknown, so it is not this function's to
		// discard. The caller already refuses to write over an unread baseline.
		return ec.state
	}
	ec.state = fresh
	return ec.state
}

// readPrivateState reads the section from azd, ignoring anything this command
// already cached.
//
// Separate from loadPrivateState because the write path needs a baseline taken
// under the lock rather than one taken at the start of the command: a sibling
// service's deploy may have added keys since then, and the write replaces the
// whole section.
func (ec *evalContext) readPrivateState(ctx context.Context) (map[string]string, error) {
	stored := map[string]string{}
	found, err := ec.getEnvConfig(ctx, privateStatePath, &stored)
	if err != nil {
		return nil, err
	}
	if !found {
		stored = map[string]string{}
	}

	// Scoped to the project it describes rather than to the environment holding
	// it, so an environment pointed at another project starts from nothing
	// instead of inheriting the previous one's fingerprints.
	if want := normalizedEndpoint(ec.endpoint); stored[stateEndpointKey] != want {
		return map[string]string{stateEndpointKey: want}, nil
	}
	return stored, nil
}

// getEnvConfig reads one section of the environment this command acts on.
//
// azdext's ConfigHelper sends no environment name, so it always reads azd's
// current one. With -e naming another, the reconciliation state was read from
// the default environment and written back there: `-e staging` could take
// production's fingerprints, decide a dataset was unchanged, and record its own
// results over them. The request has carried an EnvName all along.
func (ec *evalContext) getEnvConfig(ctx context.Context, path string, out any) (bool, error) {
	resp, err := ec.azdClient.Environment().GetConfig(ctx, &azdext.GetConfigRequest{
		Path:    path,
		EnvName: ec.envName,
	})
	if err != nil {
		return false, err
	}
	if !resp.GetFound() || len(resp.GetValue()) == 0 {
		return false, nil
	}
	if err := json.Unmarshal(resp.GetValue(), out); err != nil {
		return true, err
	}
	return true, nil
}

// setEnvConfig writes one section back to the same environment it was read from.
func (ec *evalContext) setEnvConfig(ctx context.Context, path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = ec.azdClient.Environment().SetConfig(ctx, &azdext.SetConfigRequest{
		Path:    path,
		Value:   data,
		EnvName: ec.envName,
	})
	return err
}

// setPrivate records one entry of reconciliation state.
//
// The whole section is rewritten because azd's config store is addressed by
// path and this extension keeps its state as one object; the alternative is a
// config path per key, which puts the same sprawl in a different file.
//
// Rewriting the whole section is what makes the lock necessary. azd deploys
// services concurrently by default, and each service's deploy builds its own
// context with its own copy of this section, so two of them writing unlocked
// both report success and the later write drops the other's ids and
// fingerprints. The baseline is therefore re-read inside the lock: holding it
// only around the write would still publish a view taken before the sibling
// service started.
func (ec *evalContext) setPrivate(ctx context.Context, key, value string) error {
	if ec.azdClient == nil {
		return messages.NoAzdEnvironmentToWrite(key)
	}
	state := ec.loadPrivateState(ctx)
	if ec.stateErr != nil {
		// What the section already holds is unknown, and this write replaces all
		// of it. Recording one key over an unread baseline would delete every
		// other id and fingerprint in it -- silently, and only on the runs where
		// the read happened to fail.
		return messages.PrivateStateUnreadable(key, ec.stateErr)
	}
	if state[key] == value {
		return nil
	}

	unlock, err := ec.lockPrivateState(ctx)
	if err != nil {
		return err
	}
	defer unlock()

	merged, err := ec.readPrivateState(ctx)
	if err != nil {
		return messages.PrivateStateUnreadable(key, err)
	}
	merged[key] = value
	if err := ec.setEnvConfig(ctx, privateStatePath, merged); err != nil {
		// ec.state is left as it was, so a later read in this same command
		// reports what is still persisted. Recording the key anyway dropped a
		// value the write never touched, and a resource that reads as untracked
		// gets another immutable version published for it.
		return messages.WritingEnvValue(key, err)
	}
	ec.state = merged
	return nil
}

// lockPrivateState takes the cross-process lock on the reconciliation section.
//
// Outside an azd project there is nothing to share the section with and no
// directory to put a lock file in, so the write goes ahead unguarded.
func (ec *evalContext) lockPrivateState(ctx context.Context) (func(), error) {
	root := ec.projectRoot(ctx)
	if root == "" {
		return func() {}, nil
	}
	return project.LockEvalState(ctx, filepath.Join(root, azdEnvironmentDirName))
}

// projectRoot is the directory holding azure.yaml, cached for the command.
//
// Empty when azd does not report a project, which is every standalone
// invocation against the data plane.
func (ec *evalContext) projectRoot(ctx context.Context) string {
	if ec.rootKnown {
		return ec.root
	}
	ec.rootKnown = true
	if ec.azdClient == nil {
		return ""
	}
	resp, err := ec.azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil || resp.GetProject() == nil {
		return ""
	}
	ec.root = resp.GetProject().GetPath()
	return ec.root
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

// remoteAgentName resolves an eval target's name to the name the agent answers
// to, which is not always the azure.yaml service key `init` wrote there.
//
// A project that cannot be read leaves the target as written rather than
// failing the run: outside azd -- which every atomic command supports -- there
// is no azure.yaml to consult, and the configuration is entitled to name a
// remote agent that no local service declares. The service is the one that
// gets to say whether the name exists. Only a project that answers with two
// services claiming the same name is refused, because that is a question about
// which agent to bill, and it has no answer here.
func (ec *evalContext) remoteAgentName(ctx context.Context, targetName string) (string, error) {
	proj, err := ec.azdProject(ctx)
	if err != nil || proj == nil {
		return targetName, nil
	}
	return project.RemoteAgentName(proj, targetName)
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
// azure.yaml to read, which is ordinary rather than a failure. It also answers
// the in-project default, because that default is `evals` under the project
// root and only this level knows where the root is. What remains below is the
// answer for a caller who is not in a project at all, where the caller's own
// directory is the only base there is.
//
// declared outranks recorded. EVAL_CONFIG_PATH is a machine-local absolute
// path in a file that gets committed and shared, so a teammate, a container or
// a rebuilt agent inherits a directory that does not exist on their disk --
// while azure.yaml's `$ref` says where the configuration actually is, in a form
// that travels. The recorded value is still read for a project scaffolded
// before any service entry existed, and a disagreement is reported rather than
// silently resolved.
func evalDirCascade(
	flagValue string,
	recorded func() (string, error),
	declared func() (string, error),
	disagreed func(recorded, declared string),
) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	dir := ""
	if declared != nil {
		var err error
		if dir, err = declared(); err != nil {
			return "", err
		}
	}
	path, err := recorded()
	if err != nil {
		return "", err
	}
	if dir != "" {
		if path != "" && !sameEvalLocation(path, dir) && disagreed != nil {
			disagreed(path, dir)
		}
		return dir, nil
	}
	if path != "" {
		return path, nil
	}
	return project.DefaultEvalDir, nil
}

// sameEvalLocation reports whether two answers name the same configuration.
//
// Compared as absolute paths: one is recorded absolute and the other is
// resolved from a `$ref` relative to azure.yaml, so comparing them as written
// reported every project as disagreeing with itself.
func sameEvalLocation(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return strings.EqualFold(filepath.Clean(absA), filepath.Clean(absB))
}

// projectEvalLocation is where the project puts its evaluation configuration:
// the location azure.yaml's `$ref` points at, or the default beneath the
// project root when nothing declares one.
//
// The service entry is the project's own statement of where its evaluation
// configuration lives, and `azd up` has always deployed from it. The full path
// is returned rather than its directory: the `$ref` names a file, and a project
// declaring `./config/nightly.yaml` means that file, not whatever
// `azure.eval.yaml` happens to sit beside it.
//
// Both answers are resolved against the directory holding azure.yaml, because
// that is what a `$ref` is written relative to and where `azd up` deploys from.
// Returned raw they were opened against the extension's working directory
// instead, so running any command from a subdirectory of the project reported
// the configuration missing and `generate` wrote a second one -- while the
// deploy went on using the first.
//
// The default belongs here and not in the cascade's last line for the same
// reason: inside a project it means <root>/evals, and only outside one does it
// mean `evals` beside the caller.
//
// Returns empty outside an azd project.
//
// A failure to reach azd is not that answer, and is returned. The two used to
// be collapsed into "no project", which sent the whole cascade to its default:
// a denied or faulted Get inside a project silently moved every command onto
// `evals` beside the caller, where `init` would write a second configuration
// and the deploy would go on using the first.
//
// GetServices is a map, so "the first entry that matches" is whichever one Go
// happened to visit first. A project declaring two evaluation services is a
// question this cannot answer, so it says so rather than picking one and
// operating on a configuration the author did not mean.
func projectEvalLocation(ctx context.Context, azdClient *azdext.AzdClient) (string, error) {
	if azdClient == nil {
		return "", nil
	}
	resp, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		// HostedSourceAbsent rather than isNoDefaultEnvironmentError, which
		// excludes an unreachable daemon: there being no azd to ask means the
		// extension was invoked directly, and `evals` beside the caller is the
		// right answer for that, not a wrong directory. What must not pass is a
		// denial, an expired login, or a server fault -- azd failing to say
		// where the project is, reported as though it had said there is none.
		if projectctx.HostedSourceAbsent(err) {
			return "", nil
		}
		return "", err
	}
	if resp.GetProject() == nil {
		return "", nil
	}
	root := resp.GetProject().GetPath()

	seen := map[string]bool{}
	var refs []string
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
		cleaned := filepath.Clean(filepath.FromSlash(ref))
		if seen[cleaned] {
			continue
		}
		seen[cleaned] = true
		refs = append(refs, cleaned)
	}

	switch len(refs) {
	case 0:
		// In a project, but nothing declares the eval host yet -- which is where
		// `init` is about to write, and it writes under the project root.
		return project.UnderRoot(root, project.DefaultEvalDir), nil
	case 1:
		return project.UnderRoot(root, refs[0]), nil
	default:
		// The refs are reported as the author wrote them, not as resolved paths:
		// the reader has to find them in azure.yaml.
		sort.Strings(refs)
		return "", messages.AmbiguousEvalServices(refs)
	}
}

// evalDir is the cascade for a command that already holds an azd connection.
func (ec *evalContext) evalDir(ctx context.Context, flagValue string) (string, error) {
	return evalDirCascade(flagValue, func() (string, error) {
		if ec.envName == "" || ec.azdClient == nil {
			return "", nil
		}
		return readRecordedEvalPath(ctx, ec.azdClient, ec.envName)
	}, func() (string, error) {
		return projectEvalLocation(ctx, ec.azdClient)
	}, func(recorded, declared string) {
		fmt.Fprint(warnWriter(ctx), messages.Warning(
			messages.StaleRecordedEvalPath(recorded, declared, envKeyEvalPath)))
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
	}, func() (string, error) {
		azdClient, err := azdext.NewAzdClient()
		if err != nil {
			return "", nil
		}
		defer azdClient.Close()
		return projectEvalLocation(ctx, azdClient)
	}, func(recorded, declared string) {
		fmt.Fprint(warnWriter(ctx), messages.Warning(
			messages.StaleRecordedEvalPath(recorded, declared, envKeyEvalPath)))
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
