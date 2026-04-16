// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk/storage"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/exegraph"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/bicep"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"go.uber.org/multierr"
)

// errPreflightAbortedByUser is a sentinel returned from [provisionSingleLayer]
// when the underlying provider reports [provisioning.PreflightAbortedSkipped].
// The caller translates it to [internal.ErrAbortedByUser] at the action
// boundary so the user sees a friendly "Provisioning was cancelled." message.
var errPreflightAbortedByUser = errors.New("provisioning aborted by user during preflight")

// provisionLayersGraph is the single execution entry point for
// [ProvisionAction]. It dispatches to one of four disjoint paths:
//
//  1. Zero layers: returns an informational result without building a graph.
//  2. Preview mode: bypasses the graph and calls the provider's Preview API
//     directly on the sole targeted layer (preview is single-layer-only).
//  3. Single layer (non-preview): builds a one-node graph that reuses the
//     injected [ProvisionAction.provisionManager].
//  4. Multi-layer: builds one step per layer, wired via precise bicep-level
//     dependency edges, with per-layer env clones so independent layers run
//     in parallel.
//
// UX details — subscription / location banner, per-layer headers, JSON state
// dumps, allSkipped short-circuit, OpenAI / Responsible AI error wrappers —
// are handled here rather than in the step itself, keeping the step focused
// on the provisioning lifecycle.
func (p *ProvisionAction) provisionLayersGraph(
	ctx context.Context,
	layers []provisioning.Options,
	startTime time.Time,
	previewMode bool,
) (*actions.ActionResult, error) {
	// ── no-op: zero layers ───────────────────────────────────────────────
	// Guards both preview and deploy paths from index-out-of-range panics
	// when a project defines no provisioning layers (rare but valid).
	if len(layers) == 0 {
		if previewMode {
			return &actions.ActionResult{
				Message: &actions.ResultMessage{
					Header: "No provisioning layers defined — nothing to preview.",
				},
			}, nil
		}
		return &actions.ActionResult{
			Message: &actions.ResultMessage{
				Header: "No provisioning layers defined — nothing to provision.",
			},
		}, nil
	}

	// ── preview ──────────────────────────────────────────────────────────
	// Preview calls mgr.Preview() (not Deploy) and has completely different
	// UX (no hooks, no env updates, no cache invalidation). It always
	// operates on a single layer.
	if previewMode {
		return p.provisionPreview(ctx, layers[0], startTime)
	}

	g := exegraph.NewGraph()

	// Tracks whether every layer was skipped (deployment-state-unchanged).
	allSkipped := true
	var allSkippedMu sync.Mutex

	quiet := false // multi-layer: show "Provisioning layer: ..." banners

	if len(layers) == 1 {
		// ── single-layer (non-preview) ───────────────────────────────────
		// Build a 1-node graph using the injected provisionManager so that
		// existing tests which mock provisionManager continue to work.
		quiet = true
		layer := layers[0]
		layer.IgnoreDeploymentState = p.flags.ignoreDeploymentState
		layerPath := layer.AbsolutePath(p.projectConfig.Path)

		if err := g.AddStep(&exegraph.Step{
			Name: provisionLayerStepName(layer),
			Action: func(ctx context.Context) error {
				if err := p.provisionManager.Initialize(ctx, p.projectConfig.Path, layer); err != nil {
					return fmt.Errorf("initializing provisioning manager: %w", err)
				}

				p.displayEnvironmentDetails(ctx)

				if layer.Name != "" {
					p.console.Message(ctx, fmt.Sprintf("Layer: %s", output.WithHighLightFormat(layer.Name)))
				}
				p.console.Message(ctx, "")

				if p.alphaFeatureManager.IsEnabled(azapi.FeatureDeploymentStacks) {
					p.console.WarnForFeature(ctx, azapi.FeatureDeploymentStacks)
				}

				projectEventArgs := project.ProjectLifecycleEventArgs{
					Project: p.projectConfig,
					Args: map[string]any{
						"preview": false,
						"layer":   layer.Name,
						"path":    layerPath,
					},
				}

				var deployResult *provisioning.DeployResult
				hookErr := p.runLayerProvisionWithHooks(ctx, layer, layerPath, func() error {
					return p.projectConfig.Invoke(ctx, project.ProjectEventProvision, projectEventArgs, func() error {
						var innerErr error
						deployResult, innerErr = p.provisionManager.Deploy(ctx)
						return innerErr
					})
				})
				// Raw errors only — the outer graph error path runs every step
				// failure through wrapProvisionError exactly once, avoiding
				// double-wrapping ("deployment failed: deployment failed: …")
				// and duplicate JSON state dumps.
				if hookErr != nil {
					return hookErr
				}

				if deployResult.SkippedReason == provisioning.PreflightAbortedSkipped {
					// Return the internal sentinel; wrapProvisionError at the
					// outer boundary emits the "Provisioning was cancelled."
					// UX message and translates to internal.ErrAbortedByUser.
					return errPreflightAbortedByUser
				}

				skipped := deployResult.SkippedReason == provisioning.DeploymentStateSkipped
				if !skipped {
					allSkippedMu.Lock()
					allSkipped = false
					allSkippedMu.Unlock()

					servicesStable, svcErr := p.importManager.ServiceStable(ctx, p.projectConfig)
					if svcErr != nil {
						return svcErr
					}

					for _, svc := range servicesStable {
						eventArgs := project.ServiceLifecycleEventArgs{
							Project:        p.projectConfig,
							Service:        svc,
							ServiceContext: project.NewServiceContext(),
							Args: map[string]any{
								"bicepOutput": deployResult.Deployment.Outputs,
							},
						}

						if err := svc.RaiseEvent(ctx, project.ServiceEventEnvUpdated, eventArgs); err != nil {
							return err
						}
					}
				}

				return nil
			},
		}); err != nil {
			return nil, fmt.Errorf("building provision step: %w", err)
		}
	} else {
		// ── multi-layer ──────────────────────────────────────────────────
		// Each layer gets its own node. Dependencies between layers are
		// expressed as precise producer→consumer edges derived from static
		// bicep analysis so independent layers run concurrently.

		// 1. Up-front environment + feature setup. Running this serially
		//    before any concurrent layer steps ensures the subscription /
		//    location prompts complete exactly once and the values land in
		//    p.env, so each per-layer env clone inherits them (no
		//    interactive races, CI-safe).
		if err := p.provisionManager.Initialize(ctx, p.projectConfig.Path, layers[0]); err != nil {
			return nil, fmt.Errorf("initializing provisioning manager: %w", err)
		}
		p.displayEnvironmentDetails(ctx)
		p.console.Message(ctx, "")
		if p.alphaFeatureManager.IsEnabled(azapi.FeatureDeploymentStacks) {
			p.console.WarnForFeature(ctx, azapi.FeatureDeploymentStacks)
		}

		// 2. Analyze bicep-level layer dependencies to get precise edges.
		//    This enables true parallelism for independent layers instead
		//    of an unnecessary linear chain.
		layerDeps, err := bicep.AnalyzeLayerDependencies(layers, p.projectConfig.Path, p.env)
		if err != nil {
			return nil, fmt.Errorf("analyzing layer dependencies: %w", err)
		}

		// Pre-compute step names so edges can reference layers regardless
		// of iteration order. Unnamed layers get an indexed fallback so
		// multiple unnamed layers don't collide on the sentinel "default"
		// name (which would fail AddStep with "duplicate step name").
		stepNames := make([]string, len(layers))
		for i, layer := range layers {
			if layer.Name != "" {
				stepNames[i] = layer.Name
			} else {
				stepNames[i] = fmt.Sprintf("provision-layer-%d", i)
			}
		}

		for i, layer := range layers {
			layer.IgnoreDeploymentState = p.flags.ignoreDeploymentState

			// Translate bicep-inferred indices into exegraph dependency names.
			var deps []string
			if layerDeps != nil {
				for _, depIdx := range layerDeps.Edges[i] {
					if depIdx < 0 || depIdx >= len(layers) {
						return nil, fmt.Errorf(
							"invalid layer dependency index %d for layer %s (max %d)",
							depIdx, stepNames[i], len(layers)-1,
						)
					}
					deps = append(deps, stepNames[depIdx])
				}
			}

			if err := g.AddStep(&exegraph.Step{
				Name:      stepNames[i],
				DependsOn: deps,
				Action: func(ctx context.Context) error {
					outcome, err := p.provisionSingleLayerWithOutcome(ctx, layer, stepNames[i])
					if err != nil {
						return err
					}
					if outcome != provisionSkipped {
						allSkippedMu.Lock()
						allSkipped = false
						allSkippedMu.Unlock()
					}
					return nil
				},
			}); err != nil {
				return nil, fmt.Errorf("building provision step for layer %s: %w", layer.Name, err)
			}
		}
	}

	// ── execute graph ────────────────────────────────────────────────────
	opts := p.graphRunOptions(ctx, quiet)
	result := exegraph.RunWithResult(ctx, g, opts)
	p.logProvisionGraphTimings(result)

	if result.Error != nil {
		// Peel the scheduler's `step "X" failed:` prefix and run the
		// underlying error through the same wrapping used by the sequential
		// path (OpenAI / Responsible AI translation, preflight-abort →
		// ErrAbortedByUser, state dump on provider failure, etc.).
		return nil, p.wrapProvisionError(ctx, unwrapStepErrors(result))
	}

	// ── shared finalization ──────────────────────────────────────────────
	if allSkipped {
		return &actions.ActionResult{
			Message: &actions.ResultMessage{
				Header: "There are no changes to provision for your application.",
			},
		}, nil
	}

	// JSON state dump (for --output json).
	if p.formatter.Kind() == output.JsonFormat {
		stateResult, err := p.provisionManager.State(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf(
				"deployment succeeded but the deployment result is unavailable: %w",
				err,
			)
		}

		if err := p.formatter.Format(
			provisioning.NewEnvRefreshResultFromState(stateResult.State), p.writer, nil); err != nil {
			return nil, fmt.Errorf(
				"deployment succeeded but the deployment result could not be displayed: %w",
				err,
			)
		}
	}

	// Invalidate cache after successful provisioning so next azd show will refresh.
	if err := p.envManager.InvalidateEnvCache(ctx, p.env.Name()); err != nil {
		log.Printf("warning: failed to invalidate state cache: %v", err)
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: fmt.Sprintf(
				"Your application was provisioned in Azure in %s.", ux.DurationAsText(since(startTime))),
			FollowUp: getResourceGroupFollowUp(
				ctx,
				p.formatter,
				p.portalUrlBase,
				p.projectConfig,
				p.resourceManager,
				p.env,
				false,
			),
		},
	}, nil
}

// provisionPreview handles the `azd provision --preview` path. Preview calls
// mgr.Preview() (not Deploy) and has completely different UX (no hooks, no env
// updates, no cache invalidation). It always operates on a single layer.
func (p *ProvisionAction) provisionPreview(
	ctx context.Context,
	layer provisioning.Options,
	startTime time.Time,
) (*actions.ActionResult, error) {
	layer.IgnoreDeploymentState = p.flags.ignoreDeploymentState
	if err := p.provisionManager.Initialize(ctx, p.projectConfig.Path, layer); err != nil {
		return nil, fmt.Errorf("initializing provisioning manager: %w", err)
	}

	// Display environment details (after manager initialize so env is populated).
	p.displayEnvironmentDetails(ctx)

	if layer.Name != "" {
		p.console.Message(ctx, fmt.Sprintf("Layer: %s", output.WithHighLightFormat(layer.Name)))
	}
	p.console.Message(ctx, "")

	if p.alphaFeatureManager.IsEnabled(azapi.FeatureDeploymentStacks) {
		p.console.WarnForFeature(ctx, azapi.FeatureDeploymentStacks)
	}

	deployPreviewResult, err := p.provisionManager.Preview(ctx)
	if err != nil {
		return nil, p.wrapProvisionError(ctx, err)
	}

	p.console.MessageUxItem(ctx, deployResultToUx(deployPreviewResult))

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: fmt.Sprintf(
				"Generated provisioning preview in %s.", ux.DurationAsText(since(startTime))),
			FollowUp: getResourceGroupFollowUp(
				ctx,
				p.formatter,
				p.portalUrlBase,
				p.projectConfig,
				p.resourceManager,
				p.env,
				true,
			),
		},
	}, nil
}

// runLayerProvisionWithHooks runs the per-layer pre/post hooks around the
// provisioning `actionFn`. When the layer has no hooks, `actionFn` runs
// directly. Used by the single-layer path (the multi-layer path builds its
// hooks runner inline inside [runProvisionSingleLayer]).
func (p *ProvisionAction) runLayerProvisionWithHooks(
	ctx context.Context,
	layer provisioning.Options,
	layerPath string,
	actionFn ext.InvokeFn,
) error {
	if len(layer.Hooks) == 0 {
		return actionFn()
	}

	hooksManager := ext.NewHooksManager(ext.HooksManagerOptions{
		Cwd: layerPath, ProjectDir: p.projectConfig.Path,
	}, p.commandRunner)
	hooksRunner := ext.NewHooksRunner(
		hooksManager,
		p.commandRunner,
		p.envManager,
		p.console,
		layerPath,
		layer.Hooks,
		p.env,
		p.serviceLocator,
	)

	p.validateAndWarnLayerHooks(ctx, hooksManager, layer.Hooks)

	err := hooksRunner.Invoke(
		ctx, []string{string(project.ProjectEventProvision)}, "layer", actionFn,
	)
	if err != nil {
		if layer.Name == "" {
			return err
		}

		return fmt.Errorf("layer '%s': %w", layer.Name, err)
	}

	return nil
}

func (p *ProvisionAction) validateAndWarnLayerHooks(
	ctx context.Context,
	hooksManager *ext.HooksManager,
	hooks map[string][]*ext.HookConfig,
) {
	validationResult := hooksManager.ValidateHooks(ctx, hooks)

	for _, warning := range validationResult.Warnings {
		p.console.MessageUxItem(ctx, &ux.WarningMessage{
			Description: warning.Message,
		})
		if warning.Suggestion != "" {
			p.console.Message(ctx, warning.Suggestion)
		}
		p.console.Message(ctx, "")
	}
}

// graphRunOptions builds the exegraph RunOptions used by provision graph
// runs. The `quiet` flag suppresses per-layer banner output (used for
// single-node preview/single-layer graphs where a banner would be noise).
// ctx is threaded into lifecycle callbacks so they emit against the caller's
// context (honoring cancellation, tracing, etc.) instead of [context.Background].
func (p *ProvisionAction) graphRunOptions(ctx context.Context, quiet bool) exegraph.RunOptions {
	p.ensureGraphShared()
	safeCon := p.graphSyncConsole

	opts := exegraph.RunOptions{
		ErrorPolicy: exegraph.FailFast,
	}

	if !quiet {
		opts.OnStepStart = func(stepName string) {
			safeCon.Message(ctx, fmt.Sprintf(
				"Provisioning layer: %s", output.WithHighLightFormat(stepName),
			))
		}
		opts.OnStepDone = func(stepName string, err error) {
			switch {
			case err == nil:
				safeCon.Message(ctx, fmt.Sprintf(
					"Layer %s completed", output.WithHighLightFormat(stepName),
				))
			case exegraph.IsStepSkipped(err):
				safeCon.Message(ctx, fmt.Sprintf(
					"Layer %s skipped (dependency failed)", output.WithHighLightFormat(stepName),
				))
			default:
				safeCon.Message(ctx, fmt.Sprintf(
					"Layer %s failed", output.WithHighLightFormat(stepName),
				))
			}
		}
	}

	if v, ok := os.LookupEnv("AZD_PROVISION_CONCURRENCY"); ok {
		if n, parseErr := strconv.Atoi(v); parseErr == nil && n > 0 {
			opts.MaxConcurrency = min(n, 64)
		}
	}

	return opts
}

// ensureGraphShared lazily initializes the shared thread-safe console wrapper
// and mutexes used across concurrent graph layer steps.
func (p *ProvisionAction) ensureGraphShared() {
	p.graphOnce.Do(func() {
		p.graphSyncConsole = &syncConsole{Console: p.console}
		p.graphEnvMu = &sync.Mutex{}
		p.graphHookMu = &sync.Mutex{}
	})
}

// provisionLayerStepName returns a stable step name for a provisioning layer
// in the exegraph. Unnamed layers fall back to a sentinel "default".
func provisionLayerStepName(layer provisioning.Options) string {
	if layer.Name != "" {
		return layer.Name
	}
	return "default"
}

// provisionOutcome captures the resulting disposition of a single-layer
// provisioning run so the caller can aggregate allSkipped across layers.
type provisionOutcome int

const (
	provisionDeployed provisionOutcome = iota
	provisionSkipped
)

// provisionSingleLayerWithOutcome provisions a single layer using a per-layer
// environment clone and returns the outcome for allSkipped accounting. Used
// by the multi-layer graph path. `stepName` is the graph step identifier
// used for error framing so failures from multiple unnamed layers remain
// disambiguated (matching the name used in [exegraph.Step.Name]).
func (p *ProvisionAction) provisionSingleLayerWithOutcome(
	ctx context.Context,
	layer provisioning.Options,
	stepName string,
) (provisionOutcome, error) {
	p.ensureGraphShared()

	deps := &provisionLayerDeps{
		env:                 p.env,
		envManager:          p.envManager,
		serviceLocator:      p.serviceLocator,
		defaultProvider:     p.defaultProvider,
		alphaFeatureManager: p.alphaFeatureManager,
		fileShareService:    p.fileShareService,
		cloud:               p.cloud,
		projectPath:         p.projectConfig.Path,
		projectConfig:       p.projectConfig,
		commandRunner:       p.commandRunner,
		importManager:       p.importManager,
		hookMu:              p.graphHookMu,
	}

	result, err := runProvisionSingleLayer(
		ctx, deps, layer, stepName, p.graphSyncConsole, p.graphEnvMu,
	)
	if err != nil {
		return provisionDeployed, err
	}
	if result != nil && result.SkippedReason == provisioning.DeploymentStateSkipped {
		return provisionSkipped, nil
	}
	return provisionDeployed, nil
}

// logProvisionGraphTimings emits per-step and total timings from a provision
// graph run to the debug log.
func (p *ProvisionAction) logProvisionGraphTimings(result *exegraph.RunResult) {
	if result == nil {
		return
	}
	for _, st := range result.Steps {
		log.Printf("provision step %q status=%v duration=%v", st.Name, st.Status, st.Duration)
	}
	log.Printf("provision graph total duration: %v", result.TotalDuration)
}

// wrapProvisionError replicates the sequential path's error-wrapping logic
// (provision.go:382-435): JSON state dump on failure, OpenAI access wrapper,
// Responsible AI wrapper, and the preflight-aborted translation.
func (p *ProvisionAction) wrapProvisionError(ctx context.Context, err error) error {
	// Preflight-aborted → ErrAbortedByUser with success message.
	if errors.Is(err, errPreflightAbortedByUser) {
		p.console.MessageUxItem(ctx, &ux.ActionResult{
			SuccessMessage: "Provisioning was cancelled.",
		})
		return internal.ErrAbortedByUser
	}

	// JSON state dump on failure.
	if p.formatter.Kind() == output.JsonFormat {
		stateResult, stateErr := p.provisionManager.State(ctx, nil)
		if stateErr != nil {
			return fmt.Errorf(
				"deployment failed and the deployment result is unavailable: %w",
				multierr.Combine(stateErr, err),
			)
		}
		if fmtErr := p.formatter.Format(
			provisioning.NewEnvRefreshResultFromState(stateResult.State),
			p.writer, nil,
		); fmtErr != nil {
			return fmt.Errorf(
				"deployment failed and the deployment result could not be displayed: %w",
				multierr.Combine(fmtErr, err),
			)
		}
	}

	errorMsg := err.Error()

	// OpenAI access denied.
	if strings.Contains(errorMsg, specialFeatureOrQuotaIdRequired) &&
		strings.Contains(errorMsg, "OpenAI") {
		requestAccessLink := "https://go.microsoft.com/fwlink/?linkid=2259205&clcid=0x409"
		return &internal.ErrorWithSuggestion{
			Err: err,
			Suggestion: "\nSuggested Action: The selected subscription does not have access to" +
				" Azure OpenAI Services. Please visit " + output.WithLinkFormat("%s", requestAccessLink) +
				" to request access.",
		}
	}

	// AI service not enabled / no quota.
	if strings.Contains(errorMsg, AINotValid) &&
		strings.Contains(errorMsg, openAIsubscriptionNoQuotaId) {
		return &internal.ErrorWithSuggestion{
			Suggestion: "\nSuggested Action: The selected " +
				"subscription has not been enabled for use of Azure AI service and does not have quota for " +
				"any pricing tiers. Please visit " + output.WithLinkFormat("%s", p.portalUrlBase) +
				" and select 'Create' on specific services to request access.",
			Err: err,
		}
	}

	// Responsible AI terms.
	if strings.Contains(errorMsg, responsibleAITerms) {
		return &internal.ErrorWithSuggestion{
			Suggestion: "\nSuggested Action: Please visit azure portal in " +
				output.WithLinkFormat("%s", p.portalUrlBase) + ". Create the resource in azure portal " +
				"to go through Responsible AI terms, and then delete it. " +
				"After that, run 'azd provision' again",
			Err: err,
		}
	}

	return fmt.Errorf("deployment failed: %w", err)
}

// displayEnvironmentDetails emits the EnvironmentDetails UX element once at
// the top of a provision run. Called up-front before any provisioning step
// runs so subscription/location display happens exactly once.
func (p *ProvisionAction) displayEnvironmentDetails(ctx context.Context) {
	if p.subManager == nil {
		return
	}
	subscription, err := p.subManager.GetSubscription(ctx, p.env.GetSubscriptionId())
	if err != nil {
		log.Printf("failed getting subscriptions. Skip displaying sub and location: %v", err)
		return
	}

	location, locErr := p.subManager.GetLocation(ctx, p.env.GetSubscriptionId(), p.env.GetLocation())
	var locationDisplay string
	if locErr != nil {
		log.Printf("failed getting location: %v", locErr)
	} else {
		locationDisplay = location.DisplayName
	}

	var subscriptionDisplay string
	if v, parseErr := strconv.ParseBool(os.Getenv("AZD_DEMO_MODE")); parseErr == nil && v {
		subscriptionDisplay = subscription.Name
	} else {
		subscriptionDisplay = fmt.Sprintf("%s (%s)", subscription.Name, subscription.Id)
	}

	p.console.MessageUxItem(ctx, &ux.EnvironmentDetails{
		Subscription: subscriptionDisplay,
		Location:     locationDisplay,
	})
}

// provisionLayerDeps bundles the dependencies shared between provisioning
// callers ([ProvisionAction] and [UpGraphAction]) so that a single
// [provisionSingleLayer] implementation can serve both code paths.
type provisionLayerDeps struct {
	env                 *environment.Environment
	envManager          environment.Manager
	serviceLocator      ioc.ServiceLocator
	defaultProvider     provisioning.DefaultProviderResolver
	alphaFeatureManager *alpha.FeatureManager
	fileShareService    storage.FileShareService
	cloud               *cloud.Cloud
	projectPath         string
	// Hook support: wired so the graph-driven path fires the same lifecycle hooks
	// as the sequential path (layer hooks + project events + service env updates).
	projectConfig *project.ProjectConfig
	commandRunner exec.CommandRunner
	importManager *project.ImportManager
	// hookMu serializes project event handler execution across concurrent
	// layers. Handlers like AKS's setK8sContext are not goroutine-safe.
	hookMu *sync.Mutex
}

// provisionSingleLayer is the [UpGraphAction]-facing entry point. It delegates
// to the shared implementation and discards the result payload (the up graph
// doesn't need per-layer deploy results for aggregation).
func provisionSingleLayer(
	ctx context.Context,
	deps *provisionLayerDeps,
	layer provisioning.Options,
	stepName string,
	console input.Console,
	envMu *sync.Mutex,
) error {
	_, err := runProvisionSingleLayer(ctx, deps, layer, stepName, console, envMu)
	return err
}

// runProvisionSingleLayer provisions a single infrastructure layer. It creates
// an isolated environment clone so that parallel layers don't interfere with
// each other's parameter resolution, then merges outputs back into the shared
// env.
//
// The lifecycle matches the sequential path in [ProvisionAction]:
//
//  1. Layer pre-hooks (HooksRunner — per-layer hooks from azure.yaml)
//  2. Project pre-provision event (EventDispatcher — for service targets like AKS)
//  3. mgr.Deploy (actual ARM/Bicep deployment — runs in parallel across layers)
//  4. Env merge (merge outputs into shared environment)
//  5. ServiceEventEnvUpdated (per service — e.g., .NET appsettings)
//  6. Project post-provision event (EventDispatcher)
//  7. Layer post-hooks (HooksRunner)
//
// Steps 1-2 and 5-7 are serialized via hookMu to protect non-threadsafe handlers.
//
// Returns the raw [provisioning.DeployResult] so callers can record skip
// semantics; on [provisioning.PreflightAbortedSkipped] it returns
// [errPreflightAbortedByUser] so [ProvisionAction] can translate it to
// [internal.ErrAbortedByUser].
func runProvisionSingleLayer(
	ctx context.Context,
	deps *provisionLayerDeps,
	layer provisioning.Options,
	stepName string,
	console input.Console,
	envMu *sync.Mutex,
) (*provisioning.DeployResult, error) {
	// Snapshot the shared environment so this layer resolves parameters
	// from current values (including outputs from prior phases).
	envMu.Lock()
	layerEnv := environment.NewWithValues(
		deps.env.Name(), deps.env.Dotenv(),
	)
	envMu.Unlock()

	// Use a noop-save env manager for the per-layer manager. Saves happen
	// against the shared environment after outputs are merged.
	noopMgr := &noopSaveEnvManager{Manager: deps.envManager}

	mgr := provisioning.NewManager(
		deps.serviceLocator,
		deps.defaultProvider,
		noopMgr,
		layerEnv,
		console,
		deps.alphaFeatureManager,
		deps.fileShareService,
		deps.cloud,
	)

	if err := mgr.Initialize(ctx, deps.projectPath, layer); err != nil {
		return nil, fmt.Errorf("initializing layer %s: %w", stepName, err)
	}

	// Resolve layer path for hooks (matches sequential runLayerProvisionWithHooks).
	// Use layer.AbsolutePath so absolute `infra.layers[].path` values are
	// preserved instead of being clobbered by filepath.Join.
	layerPath := layer.AbsolutePath(deps.projectPath)

	// Build hooks runner for layer-specific hooks (if any).
	var hooksRunner *ext.HooksRunner
	if len(layer.Hooks) > 0 {
		hooksManager := ext.NewHooksManager(ext.HooksManagerOptions{
			Cwd: layerPath, ProjectDir: deps.projectPath,
		}, deps.commandRunner)
		hooksRunner = ext.NewHooksRunner(
			hooksManager, deps.commandRunner, noopMgr, console,
			layerPath, layer.Hooks, layerEnv, deps.serviceLocator,
		)

		// Validate layer hooks and warn about issues (mirrors sequential path).
		validationResult := hooksManager.ValidateHooks(ctx, layer.Hooks)
		for _, warning := range validationResult.Warnings {
			console.MessageUxItem(ctx, &ux.WarningMessage{Description: warning.Message})
			if warning.Suggestion != "" {
				console.Message(ctx, warning.Suggestion)
			}
			console.Message(ctx, "")
		}
	}

	// Project lifecycle event args (matches sequential projectEventArgs).
	projectEventArgs := project.ProjectLifecycleEventArgs{
		Project: deps.projectConfig,
		Args: map[string]any{
			"layer": layer.Name,
			"path":  layerPath,
		},
	}
	provisionEvent := string(project.ProjectEventProvision)
	preProvisionEvent := ext.Event("pre" + provisionEvent)
	postProvisionEvent := ext.Event("post" + provisionEvent)

	// ── Step 1: Layer pre-hooks ──
	if hooksRunner != nil {
		if err := func() error {
			deps.hookMu.Lock()
			defer deps.hookMu.Unlock()
			return hooksRunner.RunHooks(
				ctx, ext.HookTypePre, "layer", nil, provisionEvent,
			)
		}(); err != nil {
			return nil, fmt.Errorf("layer %s pre-hooks: %w", stepName, err)
		}
	}

	// ── Step 2: Project pre-provision event ──
	if err := func() error {
		deps.hookMu.Lock()
		defer deps.hookMu.Unlock()
		return deps.projectConfig.RaiseEvent(ctx, preProvisionEvent, projectEventArgs)
	}(); err != nil {
		return nil, fmt.Errorf("layer %s pre-provision event: %w", stepName, err)
	}

	// ── Step 3: Deploy (parallel — not under any lock) ──
	deployResult, err := mgr.Deploy(ctx)
	if err != nil {
		return nil, fmt.Errorf("deploying layer %s: %w", stepName, err)
	}

	if deployResult.SkippedReason == provisioning.PreflightAbortedSkipped {
		return deployResult, errPreflightAbortedByUser
	}

	// ── Step 4: Env merge ──
	if deployResult.SkippedReason == provisioning.DeploymentStateSkipped {
		if deployResult.Deployment != nil && len(deployResult.Deployment.Outputs) > 0 {
			if err := func() error {
				envMu.Lock()
				defer envMu.Unlock()
				return provisioning.UpdateEnvironment(
					ctx,
					deployResult.Deployment.Outputs,
					deps.env,
					deps.envManager,
				)
			}(); err != nil {
				return deployResult, fmt.Errorf(
					"updating environment for skipped layer %s: %w", stepName, err,
				)
			}
		}
		// Skipped layers still fire post-events so handlers (e.g., AKS)
		// can react to cached outputs.
	} else {
		if err := func() error {
			envMu.Lock()
			defer envMu.Unlock()
			// Warn when parallel layers overwrite the same output key.
			currentEnv := deps.env.Dotenv()
			for key, param := range deployResult.Deployment.Outputs {
				newValue := resolveOutputString(param)
				if existing, ok := currentEnv[key]; ok && existing != newValue {
					log.Printf("warning: layer %q overwrites env output %q", stepName, key)
				}
			}
			return provisioning.UpdateEnvironment(
				ctx,
				deployResult.Deployment.Outputs,
				deps.env,
				deps.envManager,
			)
		}(); err != nil {
			return deployResult, fmt.Errorf(
				"updating environment for layer %s: %w", stepName, err,
			)
		}
	}

	// ── Step 5: ServiceEventEnvUpdated (per service) ──
	// Matches sequential path (provision.go:472-489) — .NET framework uses this
	// to update appsettings with provisioning outputs.
	if deps.importManager != nil && deployResult.Deployment != nil &&
		len(deployResult.Deployment.Outputs) > 0 {
		servicesStable, svcErr := deps.importManager.ServiceStable(ctx, deps.projectConfig)
		if svcErr != nil {
			return deployResult, fmt.Errorf(
				"enumerating services for env update in layer %s: %w", stepName, svcErr,
			)
		}

		if err := func() error {
			deps.hookMu.Lock()
			defer deps.hookMu.Unlock()
			for _, svc := range servicesStable {
				eventArgs := project.ServiceLifecycleEventArgs{
					Project:        deps.projectConfig,
					Service:        svc,
					ServiceContext: project.NewServiceContext(),
					Args: map[string]any{
						"bicepOutput": deployResult.Deployment.Outputs,
					},
				}
				if err := svc.RaiseEvent(ctx, project.ServiceEventEnvUpdated, eventArgs); err != nil {
					return fmt.Errorf(
						"service env update event in layer %s: %w", stepName, err,
					)
				}
			}
			return nil
		}(); err != nil {
			return deployResult, err
		}
	}

	// ── Step 6: Project post-provision event ──
	if err := func() error {
		deps.hookMu.Lock()
		defer deps.hookMu.Unlock()
		return deps.projectConfig.RaiseEvent(ctx, postProvisionEvent, projectEventArgs)
	}(); err != nil {
		return deployResult, fmt.Errorf("layer %s post-provision event: %w", stepName, err)
	}

	// ── Step 7: Layer post-hooks ──
	if hooksRunner != nil {
		if err := func() error {
			deps.hookMu.Lock()
			defer deps.hookMu.Unlock()
			return hooksRunner.RunHooks(
				ctx, ext.HookTypePost, "layer", nil, provisionEvent,
			)
		}(); err != nil {
			return deployResult, fmt.Errorf("layer %s post-hooks: %w", stepName, err)
		}
	}

	return deployResult, nil
}

// resolveOutputString converts a provisioning output parameter to its string
// representation, matching the serialization logic used by
// [provisioning.UpdateEnvironment]. This lets the conflict-detection loop
// compare incoming values against what already exists in the environment.
func resolveOutputString(param provisioning.OutputParameter) string {
	if param.Type == provisioning.ParameterTypeArray ||
		param.Type == provisioning.ParameterTypeObject {
		bytes, err := json.Marshal(param.Value)
		if err != nil {
			// Fall back to Sprintf — same as simple types.
			return fmt.Sprintf("%v", param.Value)
		}
		return string(bytes)
	}
	return fmt.Sprintf("%v", param.Value)
}

// syncConsole wraps an [input.Console] with a mutex to serialize terminal
// output during parallel provisioning. Only the output methods that are
// called from parallel provisioning code paths are wrapped. Interactive
// methods (Confirm, Select, Prompt, PromptDialog) are intentionally NOT
// wrapped because parallel provisioning must run in no-prompt mode;
// calling them concurrently would be a bug in the caller, not a missing
// wrapper here. Read-only queries like IsNoPromptMode are safe to call
// concurrently without the mutex.
//
// Methods not listed here delegate without synchronization. Do not use
// GetWriter() for concurrent writes.
type syncConsole struct {
	input.Console
	mu sync.Mutex
}

func (c *syncConsole) Message(
	ctx context.Context, message string,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Console.Message(ctx, message)
}

func (c *syncConsole) MessageUxItem(
	ctx context.Context, item ux.UxItem,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Console.MessageUxItem(ctx, item)
}

func (c *syncConsole) ShowSpinner(
	ctx context.Context,
	title string,
	format input.SpinnerUxType,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Console.ShowSpinner(ctx, title, format)
}

func (c *syncConsole) StopSpinner(
	ctx context.Context,
	lastMessage string,
	format input.SpinnerUxType,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Console.StopSpinner(ctx, lastMessage, format)
}

func (c *syncConsole) WarnForFeature(
	ctx context.Context, id alpha.FeatureId,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Console.WarnForFeature(ctx, id)
}

func (c *syncConsole) EnsureBlankLine(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Console.EnsureBlankLine(ctx)
}

// noopSaveEnvManager wraps an [environment.Manager], suppressing Save and
// SaveWithOptions. Per-layer managers use this to avoid partial environment
// writes; the authoritative save happens through the shared environment.
type noopSaveEnvManager struct {
	environment.Manager
}

func (*noopSaveEnvManager) Save(
	_ context.Context, _ *environment.Environment,
) error {
	return nil
}

func (*noopSaveEnvManager) SaveWithOptions(
	_ context.Context,
	_ *environment.Environment,
	_ *environment.SaveOptions,
) error {
	return nil
}
