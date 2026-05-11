// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import "github.com/azure/azure-dev/cli/azd/pkg/project"

// aspireBuildGateKey is the gate-key policy used by `azd deploy` and `azd up`
// to serialize deploys for services that share a single .NET AppHost build.
//
// It returns a constant non-empty key ("aspire") for every service whose
// [project.ServiceConfig.DotNetContainerApp] is populated by the Aspire
// importer, and "" for every other service.
//
// The effect: the first such service's deploy step acts as the build gate,
// and every subsequent Aspire service's deploy depends on it, so the
// AppHost's shared build runs exactly once while deploy-side work for other
// services continues in parallel.
//
// This is the *only* Aspire-specific policy the execution graph layer
// carries; the graph builder itself (see service_graph.go) is Aspire-
// agnostic and consumes any opaque grouping key. Extensions that own their
// own "first-wins" build lane can supply a different callback through
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
