// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
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
)

// UpGraphAction is the single implementation of `azd up`'s default path: it
// builds a unified execution graph covering project-level command hooks
// (`preprovision`, `postprovision`, `predeploy`, `postdeploy`), every
// provision layer, every service's package → publish → deploy chain, and
// the project-level deploy lifecycle events.
//
// The graph structure for `n` provision layers and `m` services is:
//
//	cmdhook-preprovision ──▶ provision-<L0..Ln> ──▶ cmdhook-postprovision
//	   ──▶ cmdhook-predeploy ──▶ event-predeploy ──▶ publish-<svc>
//	   ──▶ deploy-<svc> ──▶ event-postdeploy ──▶ cmdhook-postdeploy
//	                                                ▲
//	  cmdhook-prepackage ──▶ event-prepackage ──▶ package-<svc>
//	     ──▶ event-postpackage ──▶ cmdhook-postpackage
//	         (cmdhook-postpackage gates event-predeploy so prepackage→
//	          postpackage→predeploy ordering matches the old workflow)
//
// Hook semantics:
//   - Middleware fires `preup`/`postup` around this action. The graph never
//     duplicates those.
//   - `cmdhook-*` nodes fire the project-level shell hooks that the cobra
//     hooks middleware fires for stand-alone `azd package` / `azd provision`
//     / `azd deploy` invocations. They are no-ops (~µs) when the project
//     declares no such hooks — structurally identical to the existing
//     `event-*` nodes.
//   - `event-prepackage`/`event-postpackage` fire [project.ProjectEventPackage]
//     pre/post handlers (the Go event dispatcher path that stand-alone
//     `azd package` invokes via projectConfig.Invoke).
//   - `event-predeploy`/`event-postdeploy` fire [project.ProjectEventDeploy]
//     pre/post handlers (the Go event dispatcher path used by e.g. the .NET
//     Aspire publisher).
//   - `provisionSingleLayer` continues to fire layer hooks + per-layer
//     `ProjectEventProvision` events internally.
type UpGraphAction struct {
	projectConfig       *project.ProjectConfig
	env                 *environment.Environment
	envManager          environment.Manager
	console             input.Console
	alphaFeatureManager *alpha.FeatureManager
	importManager       *project.ImportManager
	serviceManager      project.ServiceManager
	projectManager      project.ProjectManager
	serviceLocator      ioc.ServiceLocator
	defaultProvider     provisioning.DefaultProviderResolver
	fileShareService    storage.FileShareService
	cloud               *cloud.Cloud
	commandRunner       exec.CommandRunner
	formatter           output.Formatter
	writer              io.Writer
	portalUrlBase       string
	provisionManager    *provisioning.Manager
}

// NewUpGraphAction creates a new UpGraphAction. Dependencies are resolved via
// the IoC container.
func NewUpGraphAction(
	projectConfig *project.ProjectConfig,
	env *environment.Environment,
	envManager environment.Manager,
	console input.Console,
	alphaFeatureManager *alpha.FeatureManager,
	importManager *project.ImportManager,
	serviceManager project.ServiceManager,
	projectManager project.ProjectManager,
	serviceLocator ioc.ServiceLocator,
	defaultProvider provisioning.DefaultProviderResolver,
	fileShareService storage.FileShareService,
	cloud *cloud.Cloud,
	commandRunner exec.CommandRunner,
	formatter output.Formatter,
	writer io.Writer,
	provisionManager *provisioning.Manager,
) *UpGraphAction {
	return &UpGraphAction{
		projectConfig:       projectConfig,
		env:                 env,
		envManager:          envManager,
		console:             console,
		alphaFeatureManager: alphaFeatureManager,
		importManager:       importManager,
		serviceManager:      serviceManager,
		projectManager:      projectManager,
		serviceLocator:      serviceLocator,
		defaultProvider:     defaultProvider,
		fileShareService:    fileShareService,
		cloud:               cloud,
		commandRunner:       commandRunner,
		formatter:           formatter,
		writer:              writer,
		portalUrlBase:       cloud.PortalUrlBase,
		provisionManager:    provisionManager,
	}
}

// Run builds and executes the unified `azd up` graph. `layers` may be empty,
// in which case a zero-layer graph (cmdhook-* + service steps + deploy
// events) is built and executed. `deployFlags` is consulted by
// [resolveDeployTimeout] so that `AZD_DEPLOY_TIMEOUT` and `--timeout`
// behave identically to stand-alone `azd deploy`.
func (u *UpGraphAction) Run(
	ctx context.Context,
	layers []provisioning.Options,
	deployFlags *DeployFlags,
	startTime time.Time,
) (*actions.ActionResult, error) {
	// 1. Analyze provision layer dependencies. Empty layers → empty graph.
	var layerDeps *bicep.LayerDependencies
	if len(layers) > 0 {
		var err error
		layerDeps, err = bicep.AnalyzeLayerDependencies(layers, u.projectConfig.Path)
		if err != nil {
			return nil, fmt.Errorf("analyzing layer dependencies: %w", err)
		}
	}

	// 2. Initialize project and enumerate services.
	stableServices, err := u.initializeServices(ctx)
	if err != nil {
		return nil, err
	}

	// 3. Resolve deploy timeout (honors --timeout flag and AZD_DEPLOY_TIMEOUT
	// env var for parity with stand-alone `azd deploy`).
	deployTimeout, err := resolveDeployTimeout(deployFlags)
	if err != nil {
		return nil, err
	}

	// 4. Build the unified execution graph.
	g := exegraph.NewGraph()
	safeCon := &syncConsole{Console: u.console}
	var envMu sync.Mutex

	hookDeps := &projectCommandHookDeps{
		projectConfig:  u.projectConfig,
		env:            u.env,
		envManager:     u.envManager,
		console:        safeCon,
		commandRunner:  u.commandRunner,
		serviceLocator: u.serviceLocator,
	}

	// Deploy results and service contexts collected during graph execution.
	deployResults := map[string]*project.ServiceDeployResult{}
	var resultsMu sync.Mutex
	serviceContexts := make(map[string]*project.ServiceContext, len(stableServices))
	var svcCtxMu sync.Mutex

	// ── cmdhook-preprovision ── no deps; first.
	const (
		preProvisionHookStep  = "cmdhook-preprovision"
		postProvisionHookStep = "cmdhook-postprovision"
		preDeployHookStep     = "cmdhook-predeploy"
		postDeployHookStep    = "cmdhook-postdeploy"
		preDeployEventStep    = "event-predeploy"
		postDeployEventStep   = "event-postdeploy"
		prePackageHookStep    = "cmdhook-prepackage"
		postPackageHookStep   = "cmdhook-postpackage"
		prePackageEventStep   = "event-prepackage"
		postPackageEventStep  = "event-postpackage"
	)

	if err := g.AddStep(&exegraph.Step{
		Name: preProvisionHookStep,
		Tags: []string{"cmdhook"},
		Action: func(ctx context.Context) error {
			return runProjectCommandHook(
				ctx, hookDeps, ext.HookTypePre, string(project.ProjectEventProvision),
			)
		},
	}); err != nil {
		return nil, fmt.Errorf("building %s step: %w", preProvisionHookStep, err)
	}

	// ── provision layers ── depend on cmdhook-preprovision + bicep-inferred
	// predecessors.
	provisionSinks, err := u.addProvisionSteps(
		g, layers, layerDeps, preProvisionHookStep, safeCon, &envMu,
	)
	if err != nil {
		return nil, err
	}

	// ── cmdhook-postprovision ── fans in from all provision sinks (or from
	// cmdhook-preprovision directly when there are no layers).
	postProvisionDeps := provisionSinks
	if len(postProvisionDeps) == 0 {
		postProvisionDeps = []string{preProvisionHookStep}
	}
	if err := g.AddStep(&exegraph.Step{
		Name:      postProvisionHookStep,
		DependsOn: postProvisionDeps,
		Tags:      []string{"cmdhook"},
		Action: func(ctx context.Context) error {
			return runProjectCommandHook(
				ctx, hookDeps, ext.HookTypePost, string(project.ProjectEventProvision),
			)
		},
	}); err != nil {
		return nil, fmt.Errorf("building %s step: %w", postProvisionHookStep, err)
	}

	// ── cmdhook-predeploy ── chained after cmdhook-postprovision.
	if err := g.AddStep(&exegraph.Step{
		Name:      preDeployHookStep,
		DependsOn: []string{postProvisionHookStep},
		Tags:      []string{"cmdhook"},
		Action: func(ctx context.Context) error {
			return runProjectCommandHook(
				ctx, hookDeps, ext.HookTypePre, string(project.ProjectEventDeploy),
			)
		},
	}); err != nil {
		return nil, fmt.Errorf("building %s step: %w", preDeployHookStep, err)
	}

	// ── service steps (package / publish / deploy) + deploy events ──
	projectEventArgs := project.ProjectLifecycleEventArgs{
		Project: u.projectConfig,
	}

	// ── cmdhook-prepackage ── runs the project-level `prepackage` shell
	// hook (parity with stand-alone `azd package`). No deps so packaging
	// can start as early as possible — package steps overlap with
	// provision intentionally.
	if err := g.AddStep(&exegraph.Step{
		Name: prePackageHookStep,
		Tags: []string{"cmdhook"},
		Action: func(ctx context.Context) error {
			return runProjectCommandHook(
				ctx, hookDeps, ext.HookTypePre, string(project.ProjectEventPackage),
			)
		},
	}); err != nil {
		return nil, fmt.Errorf("building %s step: %w", prePackageHookStep, err)
	}

	// ── event-prepackage ── fires ProjectEventPackage pre-handlers
	// (parity with the projectConfig.Invoke wrap in cmd/package.go). All
	// per-service package steps gate on this via packageExtraDeps.
	if err := g.AddStep(&exegraph.Step{
		Name:      prePackageEventStep,
		DependsOn: []string{prePackageHookStep},
		Tags:      []string{"event"},
		Action: func(ctx context.Context) error {
			return u.projectConfig.RaiseEvent(
				ctx,
				ext.Event("pre"+string(project.ProjectEventPackage)),
				projectEventArgs,
			)
		},
	}); err != nil {
		return nil, fmt.Errorf("building %s step: %w", prePackageEventStep, err)
	}

	handles, err := addServiceStepsToGraph(g, serviceGraphOptions{
		services:       stableServices,
		serviceManager: u.serviceManager,
		deployTimeout:  deployTimeout,
		// `azd up` never takes a --from-package flag; leave empty.
		fromPackage:      "",
		packageExtraDeps: []string{prePackageEventStep},
		publishExtraDeps: []string{preDeployEventStep},
		serviceContexts:  serviceContexts,
		svcCtxMu:         &svcCtxMu,
		deployResults:    deployResults,
		resultsMu:        &resultsMu,
		onDeployTimeout: func(cbCtx context.Context, svc *project.ServiceConfig) {
			safeCon.MessageUxItem(cbCtx, deployTimeoutWarning(svc.Name))
		},
		buildGateKey: aspireBuildGateKey,
	})
	if err != nil {
		return nil, err
	}

	// ── event-postpackage ── fans in from all package steps. Mirrors the
	// way the projectConfig.Invoke wrapper in cmd/package.go fires the
	// post-handler after the per-service loop completes. For zero-service
	// projects, gate on event-prepackage so the pre→post ordering holds.
	postPackageEventDeps := handles.PackageSteps
	if len(postPackageEventDeps) == 0 {
		postPackageEventDeps = []string{prePackageEventStep}
	}
	if err := g.AddStep(&exegraph.Step{
		Name:      postPackageEventStep,
		DependsOn: postPackageEventDeps,
		Tags:      []string{"event"},
		Action: func(ctx context.Context) error {
			return u.projectConfig.RaiseEvent(
				ctx,
				ext.Event("post"+string(project.ProjectEventPackage)),
				projectEventArgs,
			)
		},
	}); err != nil {
		return nil, fmt.Errorf("building %s step: %w", postPackageEventStep, err)
	}

	// ── cmdhook-postpackage ── shell hook after the post-event. Gates
	// event-predeploy so the old workflow ordering (postpackage runs
	// before predeploy) is preserved even though package and provision
	// overlap in the new graph.
	if err := g.AddStep(&exegraph.Step{
		Name:      postPackageHookStep,
		DependsOn: []string{postPackageEventStep},
		Tags:      []string{"cmdhook"},
		Action: func(ctx context.Context) error {
			return runProjectCommandHook(
				ctx, hookDeps, ext.HookTypePost, string(project.ProjectEventPackage),
			)
		},
	}); err != nil {
		return nil, fmt.Errorf("building %s step: %w", postPackageHookStep, err)
	}

	// ── event-predeploy ── depends on cmdhook-predeploy + cmdhook-postpackage
	// + all package steps. Provision readiness is transitively guaranteed
	// via cmdhook-predeploy → cmdhook-postprovision → provision sinks. The
	// cmdhook-postpackage edge preserves the old workflow's ordering of
	// postpackage before predeploy.
	preDeployEventDeps := make([]string, 0, 2+len(handles.PackageSteps))
	preDeployEventDeps = append(preDeployEventDeps, preDeployHookStep, postPackageHookStep)
	preDeployEventDeps = append(preDeployEventDeps, handles.PackageSteps...)
	if err := g.AddStep(&exegraph.Step{
		Name:      preDeployEventStep,
		DependsOn: preDeployEventDeps,
		Tags:      []string{"event"},
		Action: func(ctx context.Context) error {
			return u.projectConfig.RaiseEvent(
				ctx,
				ext.Event("pre"+string(project.ProjectEventDeploy)),
				projectEventArgs,
			)
		},
	}); err != nil {
		return nil, fmt.Errorf("building %s step: %w", preDeployEventStep, err)
	}

	// ── event-postdeploy ── depends on all deploy steps.
	postDeployEventDeps := handles.DeploySteps
	if len(postDeployEventDeps) == 0 {
		// Zero-service projects: still fire post-event after cmdhook-predeploy
		// so the event ordering (pre → post) is preserved.
		postDeployEventDeps = []string{preDeployEventStep}
	}
	if err := g.AddStep(&exegraph.Step{
		Name:      postDeployEventStep,
		DependsOn: postDeployEventDeps,
		Tags:      []string{"event"},
		Action: func(ctx context.Context) error {
			return u.projectConfig.RaiseEvent(
				ctx,
				ext.Event("post"+string(project.ProjectEventDeploy)),
				projectEventArgs,
			)
		},
	}); err != nil {
		return nil, fmt.Errorf("building %s step: %w", postDeployEventStep, err)
	}

	// ── cmdhook-postdeploy ── last; depends on event-postdeploy.
	if err := g.AddStep(&exegraph.Step{
		Name:      postDeployHookStep,
		DependsOn: []string{postDeployEventStep},
		Tags:      []string{"cmdhook"},
		Action: func(ctx context.Context) error {
			return runProjectCommandHook(
				ctx, hookDeps, ext.HookTypePost, string(project.ProjectEventDeploy),
			)
		},
	}); err != nil {
		return nil, fmt.Errorf("building %s step: %w", postDeployHookStep, err)
	}

	// 5. Execute the unified graph.
	u.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     "Provisioning and deploying (azd up)",
		TitleNote: "Packaging overlaps with provisioning for faster execution",
	})

	result := exegraph.RunWithResult(ctx, g, u.runOptions())

	// Clean up temporary package artifacts regardless of success/failure.
	// Runs before error check so temp files don't leak on failed runs.
	// Safe: RunWithResult guarantees all goroutines have exited.
	for _, sc := range serviceContexts {
		for _, artifact := range sc.Package {
			if artifact.Kind == project.ArtifactKindArchive &&
				strings.HasPrefix(artifact.Location, os.TempDir()) {
				if rmErr := os.RemoveAll(artifact.Location); rmErr != nil {
					log.Printf("failed to remove temporary package: %s : %s", artifact.Location, rmErr)
				}
			}
		}
	}

	// Log per-step timing for diagnostics and benchmarking.
	for _, st := range result.Steps {
		log.Printf("up-graph step %-30s  %s  %s", st.Name, st.Status, st.Duration.Round(time.Millisecond))
	}
	log.Printf("up-graph total: %s (%d steps)", result.TotalDuration.Round(time.Millisecond), len(result.Steps))

	if result.Error != nil {
		return nil, wrapProvisionError(ctx, result.Error, provisionErrorDeps{
			console:          u.console,
			formatter:        u.formatter,
			writer:           u.writer,
			provisionManager: u.provisionManager,
			portalUrlBase:    u.portalUrlBase,
		})
	}

	// Display service endpoint artifacts collected during deploy steps.
	for _, svc := range stableServices {
		if dr, ok := deployResults[svc.Name]; ok && dr != nil && dr.Artifacts != nil {
			u.console.MessageUxItem(ctx, dr.Artifacts)
		}
	}

	// 6. Finalize: invalidate env cache.
	if cacheErr := u.envManager.InvalidateEnvCache(ctx, u.env.Name()); cacheErr != nil {
		log.Printf("warning: failed to invalidate state cache: %v", cacheErr)
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: fmt.Sprintf(
				"Your application was provisioned and deployed to Azure in %s.",
				ux.DurationAsText(since(startTime)),
			),
		},
	}, nil
}

// initializeServices enumerates services, initializes the project, and ensures
// that required service target tools are available.
func (u *UpGraphAction) initializeServices(ctx context.Context) ([]*project.ServiceConfig, error) {
	stableServices, err := u.importManager.ServiceStableFiltered(ctx, u.projectConfig, "", u.env.Getenv)
	if err != nil {
		return nil, fmt.Errorf("enumerating services: %w", err)
	}

	if err := u.projectManager.Initialize(ctx, u.projectConfig); err != nil {
		return nil, fmt.Errorf("initializing project: %w", err)
	}

	if err := u.projectManager.EnsureServiceTargetTools(
		ctx, u.projectConfig, func(_ *project.ServiceConfig) bool { return true },
	); err != nil {
		return nil, fmt.Errorf("ensuring service tools: %w", err)
	}

	return stableServices, nil
}

// addProvisionSteps adds one step per provision layer, each depending on
// `preProvisionHookStep` plus its bicep-inferred predecessors. Returns the
// names of sink nodes (steps with no successors in the provision sub-graph)
// so downstream hooks can fan in after all infrastructure is provisioned.
// Returns only `[preProvisionHookStep]` when there are no layers (so
// downstream nodes still have a deterministic predecessor).
func (u *UpGraphAction) addProvisionSteps(
	g *exegraph.Graph,
	layers []provisioning.Options,
	layerDeps *bicep.LayerDependencies,
	preProvisionHookStep string,
	safeCon *syncConsole,
	envMu *sync.Mutex,
) (provisionSinks []string, err error) {
	if len(layers) == 0 {
		return []string{preProvisionHookStep}, nil
	}

	provDeps := &provisionLayerDeps{
		env:                 u.env,
		envManager:          u.envManager,
		serviceLocator:      u.serviceLocator,
		defaultProvider:     u.defaultProvider,
		alphaFeatureManager: u.alphaFeatureManager,
		fileShareService:    u.fileShareService,
		cloud:               u.cloud,
		projectPath:         u.projectConfig.Path,
		projectConfig:       u.projectConfig,
		commandRunner:       u.commandRunner,
		importManager:       u.importManager,
		hookMu:              &sync.Mutex{},
	}

	// Compute all step names first so that dependency wiring can reference any
	// layer regardless of iteration order.
	stepNames := make([]string, len(layers))
	for i, layer := range layers {
		if layer.Name != "" {
			stepNames[i] = "provision-" + layer.Name
		} else {
			stepNames[i] = fmt.Sprintf("provision-layer-%d", i)
		}
	}

	for i := range layers {
		// Precise producer→consumer edges: each provision layer depends on the
		// layers that produce its required inputs. Every layer also depends on
		// `cmdhook-preprovision` so the shell hook runs before any provision.
		// Defensive: treat a nil layerDeps as "no inter-layer edges" so a
		// future caller that forgets to run AnalyzeLayerDependencies cannot
		// crash with a nil-map panic — the graph still wires preprovision and
		// the caller gets sequential-by-declaration-order semantics (safe
		// fallback) rather than a silent edge-less graph.
		deps := []string{preProvisionHookStep}
		var edges []int
		if layerDeps != nil {
			edges = layerDeps.Edges[i]
		}
		for _, depIdx := range edges {
			if depIdx < 0 || depIdx >= len(layers) {
				return nil, fmt.Errorf(
					"invalid layer dependency index %d for layer %s (max %d)",
					depIdx, stepNames[i], len(layers)-1,
				)
			}
			deps = append(deps, stepNames[depIdx])
		}

		layerIdx := i
		if err := g.AddStep(&exegraph.Step{
			Name:      stepNames[i],
			DependsOn: deps,
			Tags:      []string{"provision"},
			Action: func(ctx context.Context) error {
				return provisionSingleLayer(
					ctx, provDeps, layers[layerIdx],
					stepNames[layerIdx], safeCon, envMu,
				)
			},
		}); err != nil {
			return nil, fmt.Errorf("building provision step %s: %w", stepNames[i], err)
		}
	}

	// Compute provision sink nodes — steps with no successors.
	hasSuccessor := make(map[int]bool, len(layers))
	if layerDeps != nil {
		for _, depIdxs := range layerDeps.Edges {
			for _, depIdx := range depIdxs {
				hasSuccessor[depIdx] = true
			}
		}
	}
	for i := range layers {
		if !hasSuccessor[i] {
			provisionSinks = append(provisionSinks, stepNames[i])
		}
	}

	return provisionSinks, nil
}

// runOptions returns the execution options for the unified graph, including
// error policy, optional concurrency limit, and step lifecycle callbacks.
func (u *UpGraphAction) runOptions() exegraph.RunOptions {
	opts := exegraph.RunOptions{
		ErrorPolicy: exegraph.FailFast,
	}

	// Optional concurrency limit from environment.
	if v, ok := os.LookupEnv("AZD_UP_CONCURRENCY"); ok {
		if n, parseErr := strconv.Atoi(v); parseErr == nil && n > 0 {
			opts.MaxConcurrency = min(n, 64)
		}
	}

	opts.OnStepStart = func(stepName string) {
		log.Printf("up-graph: starting %s", stepName)
	}
	opts.OnStepDone = func(stepName string, err error) {
		switch {
		case err == nil:
			log.Printf("up-graph: %s completed", stepName)
		case exegraph.IsStepSkipped(err):
			log.Printf("up-graph: %s skipped (dependency failed)", stepName)
		default:
			log.Printf("up-graph: %s failed: %v", stepName, err)
		}
	}

	return opts
}
