// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/exegraph"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

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

	// serviceContexts is the cross-step sink for per-service
	// [project.ServiceContext] values produced by package and consumed by
	// publish and deploy. The caller owns the map and supplies the mutex so
	// package-step cleanup (temporary artifact removal) can run after the
	// graph finishes.
	serviceContexts map[string]*project.ServiceContext
	svcCtxMu        *sync.Mutex

	// deployResults is the per-service sink for [project.ServiceDeployResult]
	// values produced by successful deploy steps. The caller owns the map and
	// supplies the mutex.
	deployResults map[string]*project.ServiceDeployResult
	resultsMu     *sync.Mutex

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
	if opts.serviceContexts == nil || opts.svcCtxMu == nil {
		return nil, fmt.Errorf("serviceContexts and svcCtxMu must be provided")
	}
	if opts.deployResults == nil || opts.resultsMu == nil {
		return nil, fmt.Errorf("deployResults and resultsMu must be provided")
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
					progress := async.NewNoopProgress[project.ServiceProgress]()
					defer progress.Done()
					if _, pkgErr := opts.serviceManager.Package(
						ctx, pkgSvc, sc, progress, nil,
					); pkgErr != nil {
						return fmt.Errorf("packaging service %s: %w", pkgSvc.Name, pkgErr)
					}
				}

				opts.svcCtxMu.Lock()
				opts.serviceContexts[pkgSvc.Name] = sc
				opts.svcCtxMu.Unlock()
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
				opts.svcCtxMu.Lock()
				sc := opts.serviceContexts[pubSvc.Name]
				opts.svcCtxMu.Unlock()

				if sc == nil {
					return fmt.Errorf(
						"service context for %s not found — package step may have failed",
						pubSvc.Name,
					)
				}

				progress := async.NewNoopProgress[project.ServiceProgress]()
				defer progress.Done()
				if _, pubErr := opts.serviceManager.Publish(
					ctx, pubSvc, sc, progress, nil,
				); pubErr != nil {
					return fmt.Errorf("publishing service %s: %w", pubSvc.Name, pubErr)
				}
				return nil
			},
		}); err != nil {
			return nil, fmt.Errorf("building publish step %s: %w", publishStepName, err)
		}

		// ── deploy-<svc> ── publish + build gate (if any) + any caller-supplied fan-in.
		deployDeps := make([]string, 0, 1+1+len(opts.deployExtraDeps))
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
		deployDeps = append(deployDeps, opts.deployExtraDeps...)

		depSvc := svc
		if err := g.AddStep(&exegraph.Step{
			Name:      deployStepName,
			DependsOn: deployDeps,
			Tags:      []string{"deploy"},
			Action: func(stepCtx context.Context) error {
				opts.svcCtxMu.Lock()
				sc := opts.serviceContexts[depSvc.Name]
				opts.svcCtxMu.Unlock()

				if sc == nil {
					return fmt.Errorf(
						"service context for %s not found — publish step may have failed",
						depSvc.Name,
					)
				}

				deployCtx, deployCancel := context.WithTimeout(stepCtx, opts.deployTimeout)
				defer deployCancel()

				progress := async.NewNoopProgress[project.ServiceProgress]()
				defer progress.Done()

				result, depErr := opts.serviceManager.Deploy(deployCtx, depSvc, sc, progress)
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

				opts.resultsMu.Lock()
				opts.deployResults[depSvc.Name] = result
				opts.resultsMu.Unlock()

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
func deployTimeoutWarning(svcName string) *ux.WarningMessage {
	return &ux.WarningMessage{
		Description: fmt.Sprintf(
			"Deployment of service '%s' exceeded the azd wait timeout."+
				" azd has stopped waiting, but the deployment may still be running in Azure.",
			svcName,
		),
		Hints: []string{
			"Check the Azure Portal for current deployment status.",
			"Increase timeout with --timeout flag or AZD_DEPLOY_TIMEOUT env var.",
		},
	}
}
