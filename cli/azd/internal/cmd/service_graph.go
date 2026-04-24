// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/exegraph"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

// deployGraphState consolidates the shared mutable state produced during
// service graph execution. Package steps store ServiceContexts (consumed by
// publish/deploy steps); deploy steps store ServiceDeployResults (consumed by
// the caller for artifact display and JSON output). Using this struct instead
// of passing raw maps+mutexes keeps the action layers thin:
//
//	create state → build graph → execute → consume results.
type deployGraphState struct {
	ctxMu    sync.Mutex
	contexts map[string]*project.ServiceContext

	resMu   sync.Mutex
	results map[string]*project.ServiceDeployResult
}

// newDeployGraphState creates a state container pre-sized for the given services.
func newDeployGraphState(services []*project.ServiceConfig) *deployGraphState {
	return &deployGraphState{
		contexts: make(map[string]*project.ServiceContext, len(services)),
		results:  make(map[string]*project.ServiceDeployResult, len(services)),
	}
}

// StoreContext records the ServiceContext produced by a package step.
func (s *deployGraphState) StoreContext(name string, ctx *project.ServiceContext) {
	s.ctxMu.Lock()
	s.contexts[name] = ctx
	s.ctxMu.Unlock()
}

// LoadContext retrieves the ServiceContext for a service (nil if not yet stored).
func (s *deployGraphState) LoadContext(name string) *project.ServiceContext {
	s.ctxMu.Lock()
	defer s.ctxMu.Unlock()
	return s.contexts[name]
}

// StoreResult records the ServiceDeployResult produced by a deploy step.
func (s *deployGraphState) StoreResult(name string, result *project.ServiceDeployResult) {
	s.resMu.Lock()
	s.results[name] = result
	s.resMu.Unlock()
}

// GetResult retrieves the ServiceDeployResult for a service (nil if not yet stored).
func (s *deployGraphState) GetResult(name string) *project.ServiceDeployResult {
	s.resMu.Lock()
	defer s.resMu.Unlock()
	return s.results[name]
}

// ResultsSnapshot returns a shallow copy of the results map, safe to iterate
// without holding the lock.
func (s *deployGraphState) ResultsSnapshot() map[string]*project.ServiceDeployResult {
	s.resMu.Lock()
	defer s.resMu.Unlock()
	snap := make(map[string]*project.ServiceDeployResult, len(s.results))
	maps.Copy(snap, s.results)
	return snap
}

// CleanupTempArtifacts removes temporary package archives created during graph
// execution. This must be called after the graph finishes because steps run in
// parallel and may still hold file locks during execution.
func (s *deployGraphState) CleanupTempArtifacts() {
	s.ctxMu.Lock()
	defer s.ctxMu.Unlock()
	for _, sc := range s.contexts {
		for _, artifact := range sc.Package {
			if artifact.Kind == project.ArtifactKindArchive &&
				strings.HasPrefix(artifact.Location, os.TempDir()) {
				if rmErr := os.RemoveAll(artifact.Location); rmErr != nil {
					log.Printf("failed to remove temporary package: %s : %s", artifact.Location, rmErr)
				}
			}
		}
	}
}

// serviceGraphOptions configures how the package → publish → deploy service
// sub-graph is built. Both stand-alone `azd deploy` and the unified `azd up`
// default path delegate to [addServiceStepsToGraph] so that the step
// topology — including Aspire build-gate serialization — stays in one place.
type serviceGraphOptions struct {
	// services is the list of services, in stable order, for which to add
	// package/publish/deploy steps.
	services []*project.ServiceConfig

	// serviceManager runs the package/publish/deploy operations.
	serviceManager project.ServiceManager

	// deployTimeout bounds each individual deploy step. Must be > 0.
	deployTimeout time.Duration

	// fromPackage — when non-empty — creates a synthetic package artifact at
	// the supplied path instead of invoking the service packager. Only the
	// stand-alone `azd deploy` path sets this (the `--from-package` flag);
	// the `azd up` path always leaves this empty.
	fromPackage string

	// publishExtraDeps augments every publish step's DependsOn with these
	// step names. Used by `azd up` to wire publish steps behind the synthetic
	// `event-predeploy` node (which itself waits for provision sinks + all
	// package steps). Deploy's stand-alone path leaves this empty.
	publishExtraDeps []string

	// packageExtraDeps augments every package step's DependsOn with these
	// step names. Used by `azd up` to wire package steps behind the synthetic
	// `event-prepackage` node (which itself fans in from the
	// `cmdhook-prepackage` shell-hook step) so that user-defined
	// prepackage hooks and the project-level `prepackage` event fire
	// before any service is packaged — preserving parity with the
	// stand-alone `azd package` cobra command. Deploy's stand-alone path
	// leaves this empty (package steps then have no DependsOn and overlap
	// freely with anything upstream of publish).
	packageExtraDeps []string

	// deployExtraDeps augments every deploy step's DependsOn with these step
	// names (beyond the Aspire build gate). Reserved for future use; both
	// current call sites leave this empty.
	deployExtraDeps []string

	// state is the shared mutable store for per-service ServiceContexts
	// (produced by package, consumed by publish/deploy) and
	// ServiceDeployResults (produced by deploy, consumed by the caller
	// for artifact display). The caller owns the state and may call
	// CleanupTempArtifacts() after graph execution.
	state *deployGraphState

	// onDeployTimeout is invoked when a deploy step's context deadline is
	// exceeded. It lets the caller emit a richer UX (e.g., a warning box
	// with hints) before the error bubbles up. Optional.
	onDeployTimeout func(ctx context.Context, svc *project.ServiceConfig)

	// buildGateKey groups services that must share a single sequential
	// "first-wins" build lane: for each non-empty key, the first service
	// encountered in slice order acts as the gate, and every later service
	// returning the same key declares an edge to that first deploy step so
	// their deploys wait for it. An empty key means "no gate" (full
	// parallelism). Keys are opaque strings, so multiple independent gates
	// can coexist (e.g. one group per shared build toolchain).
	//
	// The graph builder itself is gate-agnostic: callers (including future
	// extensions) inject the policy. `azd deploy` and `azd up` supply a
	// callback that returns "aspire" for services owned by a .NET AppHost
	// manifest, so the first Aspire deploy triggers the shared AppHost
	// build and the rest wait — avoiding concurrent builds of the same
	// AppHost. If nil, no gating is applied.
	buildGateKey func(svc *project.ServiceConfig) string

	// onPhaseProgress, if non-nil, is invoked with intra-phase progress
	// messages emitted by ServiceManager.Package/Publish/Deploy (e.g.
	// "Compiling…", "Pushing image…"). The phase argument identifies which
	// step is reporting (phasePackaging / phasePublish / phaseDeploying).
	// Callers wire this to a deployProgressTracker so the table's "Detail"
	// column reflects what each step is doing in real time, not just which
	// phase it is in. When nil, progress messages are silently drained.
	onPhaseProgress func(serviceName string, phase deployPhase, detail string)
}

// serviceGraphHandles exposes the names of the steps that addServiceStepsToGraph
// added, in service order, so the caller can wire additional synthetic nodes
// (e.g. pre/post-deploy-event sinks) against them.
type serviceGraphHandles struct {
	PackageSteps []string
	PublishSteps []string
	DeploySteps  []string
}

// addServiceStepsToGraph appends the shared package → publish → deploy
// topology — plus optional per-service build-gate serialization — to g for
// each service in opts.services. It is the single source of truth for
// service-step wiring used by both `azd deploy` (stand-alone) and the
// unified `azd up` default path.
//
// Topology per service `<svc>`:
//
//	opts.packageExtraDeps ──▶ package-<svc> ──▶ opts.publishExtraDeps ──▶ publish-<svc> ──▶ deploy-<svc>
//	                                                                                            │
//	                                                                     opts.buildGateKey:
//	                                                             first service per non-empty key
//	                                                           runs first; later services with the
//	                                                            same key wait on that first step.
//
// Deploy ordering: when no service declares a `uses:` edge targeting
// another service in this graph, deploy steps chain sequentially in slice
// order for backward compatibility with templates that relied on implicit
// ordering (e.g. api deploys before web). When at least one service
// declares `uses:`, the graph uses explicit edges and services without
// mutual `uses:` edges deploy in parallel. Package and publish steps
// always run in parallel regardless.
//
// When opts.packageExtraDeps is empty (stand-alone `azd deploy`), package
// steps have no DependsOn and packaging can overlap with whatever provision
// steps the caller added upstream of publish.
//
// The graph builder is intentionally agnostic to why a gate exists; it
// only understands the opaque-string grouping produced by
// [serviceGraphOptions.buildGateKey]. That keeps Aspire-specific (or any
// other ecosystem-specific) sequencing policy out of the DAG layer and
// makes it extensible without changing this file.
func addServiceStepsToGraph(g *exegraph.Graph, opts serviceGraphOptions) (*serviceGraphHandles, error) {
	if g == nil {
		return nil, fmt.Errorf("graph is nil")
	}
	if opts.serviceManager == nil {
		return nil, fmt.Errorf("serviceManager is nil")
	}
	if opts.deployTimeout <= 0 {
		return nil, fmt.Errorf("deployTimeout must be > 0, got %s", opts.deployTimeout)
	}
	if opts.state == nil {
		return nil, fmt.Errorf("state must be provided")
	}

	handles := &serviceGraphHandles{
		PackageSteps: make([]string, 0, len(opts.services)),
		PublishSteps: make([]string, 0, len(opts.services)),
		DeploySteps:  make([]string, 0, len(opts.services)),
	}

	// firstByGate records, per non-empty gate key produced by
	// opts.buildGateKey, the first deploy step seen in iteration order.
	// Later services that return the same key take a dependency on that
	// first step. Services whose gate key is "" (or when buildGateKey is
	// nil) are unconstrained and run in full parallelism.
	firstByGate := make(map[string]string)

	// serviceNames is the set of service names in this graph, used to
	// resolve service-to-service edges declared via `services.<name>.uses`
	// in azure.yaml. Resource-valued `uses:` entries (targeting entries
	// under `resources:` rather than `services:`) are ignored here because
	// the provision layer already owns their lifecycle.
	serviceNames := make(map[string]struct{}, len(opts.services))
	for _, svc := range opts.services {
		serviceNames[svc.Name] = struct{}{}
	}

	// hasServiceDeps is true when at least one service declares a `uses:`
	// entry targeting another service in this graph. When false, no service
	// has declared explicit deploy ordering, so we fall back to sequential
	// deployment in azure.yaml slice order to preserve backward
	// compatibility with templates that relied on implicit ordering.
	hasServiceDeps := false
	for _, svc := range opts.services {
		for _, dep := range svc.Uses {
			if dep != svc.Name {
				if _, ok := serviceNames[dep]; ok {
					hasServiceDeps = true
					break
				}
			}
		}
		if hasServiceDeps {
			break
		}
	}

	if !hasServiceDeps && len(opts.services) > 1 {
		log.Printf(
			"deploying %d services sequentially (no uses: edges declared; "+
				"add uses: to azure.yaml to enable parallel deployment)",
			len(opts.services),
		)
	}

	// phaseProgress bundles an async.Progress with a wait function that
	// blocks until the drain goroutine has finished. Callers MUST call
	// Done() to close the channel, then Wait() before returning to ensure
	// no late onPhaseProgress callback fires after the step completes.
	type phaseProgress struct {
		*async.Progress[project.ServiceProgress]
		Wait func()
	}

	// newPhaseProgress returns a phaseProgress whose channel is drained
	// by a background goroutine that forwards each ServiceProgress.Message
	// to opts.onPhaseProgress (if non-nil). Callers MUST call Done() —
	// typically deferred — to terminate the goroutine, then call Wait()
	// to block until the goroutine exits. When onPhaseProgress is nil the
	// channel is still drained (matching the NewNoopProgress contract) so
	// ServiceManager goroutines never block on an unbuffered SetProgress
	// send.
	newPhaseProgress := func(serviceName string, phase deployPhase) phaseProgress {
		p := async.NewProgress[project.ServiceProgress]()
		done := make(chan struct{})
		go func() {
			defer close(done)
			for sp := range p.Progress() {
				if opts.onPhaseProgress != nil && sp.Message != "" {
					opts.onPhaseProgress(serviceName, phase, sp.Message)
				}
			}
		}()
		return phaseProgress{
			Progress: p,
			Wait:     func() { <-done },
		}
	}

	for _, svc := range opts.services {
		pkgStepName := "package-" + svc.Name
		publishStepName := "publish-" + svc.Name
		deployStepName := "deploy-" + svc.Name

		handles.PackageSteps = append(handles.PackageSteps, pkgStepName)
		handles.PublishSteps = append(handles.PublishSteps, publishStepName)
		handles.DeploySteps = append(handles.DeploySteps, deployStepName)

		// ── package-<svc> ── opts.packageExtraDeps (empty for stand-alone
		// deploy → no deps → packaging overlaps with anything upstream).
		pkgSvc := svc
		if err := g.AddStep(&exegraph.Step{
			Name:      pkgStepName,
			DependsOn: opts.packageExtraDeps,
			Tags:      []string{"package"},
			Action: func(ctx context.Context) error {
				sc := project.NewServiceContext()

				if opts.fromPackage != "" {
					// --from-package bypasses the packager and wraps the
					// user-supplied artifact directly.
					if pkgErr := sc.Package.Add(&project.Artifact{
						Kind:         determineArtifactKind(opts.fromPackage),
						Location:     opts.fromPackage,
						LocationKind: project.LocationKindLocal,
					}); pkgErr != nil {
						return fmt.Errorf("packaging service %s: %w", pkgSvc.Name, pkgErr)
					}
				} else {
					progress := newPhaseProgress(pkgSvc.Name, phasePackaging)
					defer progress.Wait()
					defer progress.Done()
					if _, pkgErr := opts.serviceManager.Package(
						ctx, pkgSvc, sc, progress.Progress, nil,
					); pkgErr != nil {
						return fmt.Errorf("packaging service %s: %w", pkgSvc.Name, pkgErr)
					}
				}

				opts.state.StoreContext(pkgSvc.Name, sc)
				return nil
			},
		}); err != nil {
			return nil, fmt.Errorf("building package step %s: %w", pkgStepName, err)
		}

		// ── publish-<svc> ── package + any caller-supplied fan-in (e.g.
		// event-predeploy for up).
		publishDeps := make([]string, 0, 1+len(opts.publishExtraDeps))
		publishDeps = append(publishDeps, pkgStepName)
		publishDeps = append(publishDeps, opts.publishExtraDeps...)

		pubSvc := svc
		if err := g.AddStep(&exegraph.Step{
			Name:      publishStepName,
			DependsOn: publishDeps,
			Tags:      []string{"publish"},
			Action: func(ctx context.Context) error {
				sc := opts.state.LoadContext(pubSvc.Name)

				if sc == nil {
					return fmt.Errorf(
						"service context for %s not found — package step may have failed",
						pubSvc.Name,
					)
				}

				progress := newPhaseProgress(pubSvc.Name, phasePublish)
				defer progress.Wait()
				defer progress.Done()
				if _, pubErr := opts.serviceManager.Publish(
					ctx, pubSvc, sc, progress.Progress, nil,
				); pubErr != nil {
					return fmt.Errorf("publishing service %s: %w", pubSvc.Name, pubErr)
				}
				return nil
			},
		}); err != nil {
			return nil, fmt.Errorf("building publish step %s: %w", publishStepName, err)
		}

		// ── deploy-<svc> ── publish + build gate (if any) + declared
		// service-to-service `uses:` edges + any caller-supplied fan-in.
		deployDeps := make([]string, 0, 1+1+len(svc.Uses)+len(opts.deployExtraDeps))
		deployDeps = append(deployDeps, publishStepName)
		if opts.buildGateKey != nil {
			if key := opts.buildGateKey(svc); key != "" {
				if first, ok := firstByGate[key]; ok {
					deployDeps = append(deployDeps, first)
				} else {
					firstByGate[key] = deployStepName
				}
			}
		}
		// Translate `services.<name>.uses: [depSvc]` into a deploy-step
		// edge so hooks that pass values between services (e.g. api's
		// postdeploy writes an env var that web's predeploy reads) retain
		// the deploy ordering they had under the old sequential loop.
		// Entries that don't match another service's name target a
		// resource and are left to the provision layer. Duplicates are
		// filtered so the build-gate and `uses:` edges collapse when they
		// name the same predecessor.
		for _, dep := range svc.Uses {
			if dep == svc.Name {
				continue
			}
			if _, ok := serviceNames[dep]; !ok {
				continue
			}
			depStep := "deploy-" + dep
			if !slices.Contains(deployDeps, depStep) {
				deployDeps = append(deployDeps, depStep)
			}
		}
		// Sequential fallback: when no service in this graph declares a
		// `uses:` edge to another service, chain deploy steps in slice
		// order so that templates relying on implicit sequential ordering
		// (e.g., api deploys before web) continue to work. This preserves
		// backward compatibility with existing templates while still
		// allowing parallel deployment for templates that opt in via
		// `uses:`. Package and publish steps remain parallel regardless.
		if !hasServiceDeps && len(handles.DeploySteps) >= 2 {
			prevDeploy := handles.DeploySteps[len(handles.DeploySteps)-2]
			if !slices.Contains(deployDeps, prevDeploy) {
				deployDeps = append(deployDeps, prevDeploy)
			}
		}
		deployDeps = append(deployDeps, opts.deployExtraDeps...)

		depSvc := svc
		if err := g.AddStep(&exegraph.Step{
			Name:      deployStepName,
			DependsOn: deployDeps,
			Tags:      []string{"deploy"},
			Action: func(stepCtx context.Context) error {
				sc := opts.state.LoadContext(depSvc.Name)

				if sc == nil {
					return fmt.Errorf(
						"service context for %s not found — publish step may have failed",
						depSvc.Name,
					)
				}

				deployCtx, deployCancel := context.WithTimeout(stepCtx, opts.deployTimeout)
				defer deployCancel()

				progress := newPhaseProgress(depSvc.Name, phaseDeploying)
				defer progress.Wait()
				defer progress.Done()

				result, depErr := opts.serviceManager.Deploy(deployCtx, depSvc, sc, progress.Progress)
				if depErr != nil {
					if errors.Is(deployCtx.Err(), context.DeadlineExceeded) {
						if opts.onDeployTimeout != nil {
							opts.onDeployTimeout(stepCtx, depSvc)
						}
						return fmt.Errorf(
							"deployment of service '%s' timed out after %d seconds."+
								" To increase, use --timeout flag or AZD_DEPLOY_TIMEOUT env var."+
								" Note: azd has stopped waiting, but the deployment may still be"+
								" running in Azure. Check the Azure Portal for current deployment status.",
							depSvc.Name,
							int(opts.deployTimeout.Seconds()),
						)
					}
					return fmt.Errorf("deploying service %s: %w", depSvc.Name, depErr)
				}

				opts.state.StoreResult(depSvc.Name, result)

				return nil
			},
		}); err != nil {
			return nil, fmt.Errorf("building deploy step %s: %w", deployStepName, err)
		}
	}

	return handles, nil
}

// deployTimeoutWarning is the UX element emitted when a deploy step exceeds
// its timeout. Kept next to the graph builder so both call sites share
// identical wording.
func deployTimeoutWarning(svcName string, timeout time.Duration) *ux.WarningMessage {
	return &ux.WarningMessage{
		Description: fmt.Sprintf(
			"Deployment of service '%s' exceeded the azd wait timeout (%s)."+
				" azd has stopped waiting, but the deployment may still be running in Azure.",
			svcName, timeout,
		),
		Hints: []string{
			"Check the Azure Portal for current deployment status.",
			fmt.Sprintf(
				"Increase timeout with --timeout flag (e.g. azd deploy --timeout %d)"+
					" or AZD_DEPLOY_TIMEOUT env var (e.g. AZD_DEPLOY_TIMEOUT=%d).",
				int(timeout.Seconds())*2, int(timeout.Seconds())*2),
		},
	}
}
