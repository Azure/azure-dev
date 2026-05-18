// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import "github.com/azure/azure-dev/cli/azd/pkg/project"

// aspireBuildGateKey is the gate-key policy used by `azd deploy` and `azd up`
// to group Aspire services that share a single .NET AppHost build.
//
// It returns a constant non-empty key ("aspire") for every service whose
// [project.ServiceConfig.DotNetContainerApp] is populated by the Aspire
// importer, and "" for every other service.
//
// The effect: all Aspire services in the same gate group receive a shared
// mutex via context. The deploy target
// ([project.DotNetContainerAppTarget.Deploy]) uses this as a signal to
// create a per-service --artifacts-path temp directory, so concurrent
// dotnet publish invocations write to isolated intermediate output trees
// and run fully in parallel. Only if the temp-dir creation fails does the
// mutex act as a serialization fallback — the normal path is zero
// serialization.
//
// This is the *only* Aspire-specific policy the execution graph layer
// carries; the graph builder itself (see service_graph.go) is Aspire-
// agnostic and consumes any opaque grouping key. Extensions that own their
// own sequential build lane can supply a different callback through
// [serviceGraphOptions.buildGateKey] without touching the DAG code.
//
// When Aspire eventually moves into an extension, this function — and only
// this function — needs to move with it.
func aspireBuildGateKey(svc *project.ServiceConfig) string {
	if svc != nil && svc.DotNetContainerApp != nil {
		return "aspire"
	}
	return ""
}
