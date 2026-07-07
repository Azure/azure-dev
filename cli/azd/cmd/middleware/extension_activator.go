// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/grpcserver"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/fatih/color"
)

// ExtensionActivator starts installed extensions on demand so the capabilities they provide
// (currently provisioning providers) become resolvable in commands that do not run the full
// ExtensionsMiddleware, such as `azd env refresh`. Unlike the middleware, it starts only the
// extensions declaring a requested capability, and only when that capability is not already
// resolvable (for example when a parent command's middleware already started every extension).
type ExtensionActivator struct {
	serviceLocator   ioc.ServiceLocator
	extensionManager *extensions.Manager
	extensionRunner  *extensions.Runner
	globalOptions    *internal.GlobalCommandOptions
}

// NewExtensionActivator creates a new ExtensionActivator.
func NewExtensionActivator(
	serviceLocator ioc.ServiceLocator,
	extensionManager *extensions.Manager,
	extensionRunner *extensions.Runner,
	globalOptions *internal.GlobalCommandOptions,
) *ExtensionActivator {
	return &ExtensionActivator{
		serviceLocator:   serviceLocator,
		extensionManager: extensionManager,
		extensionRunner:  extensionRunner,
		globalOptions:    globalOptions,
	}
}

// EnsureProvisioningProviders starts the installed extension(s) that declare any of the given
// provisioning provider names, so those providers can be resolved from the IoC container. Names no
// installed extension declares are left to native resolution, and already-resolvable providers are
// left alone, making activation idempotent. The returned cleanup (always non-nil, safe to defer)
// stops the extension host when one was started. A required extension failing to start is an
// error, since the caller cannot make progress without the provider it declares.
func (a *ExtensionActivator) EnsureProvisioningProviders(
	ctx context.Context,
	providerNames []string,
	environmentName string,
) (cleanup func(), err error) {
	noop := func() {}

	names := distinctProviderNames(providerNames)
	if len(names) == 0 {
		return noop, nil
	}

	installed, err := a.extensionManager.ListInstalled()
	if err != nil {
		return noop, err
	}

	toStart := extensionsForProviders(installed, names)

	// Drop extensions whose requested providers are all already resolvable (host already running).
	toStart = slices.DeleteFunc(toStart, func(ext *extensions.Extension) bool {
		return !slices.ContainsFunc(names, func(name string) bool {
			return providerFromExtension(ext, name) && !a.providerResolvable(name)
		})
	})

	if len(toStart) == 0 {
		return noop, nil
	}

	var grpcServer *grpcserver.Server
	if err := a.serviceLocator.Resolve(&grpcServer); err != nil {
		return noop, err
	}

	serverInfo, err := grpcServer.Start()
	if err != nil {
		return noop, err
	}

	cleanup = func() {
		if err := grpcServer.Stop(); err != nil {
			log.Printf("failed to stop gRPC server after extension activation: %v", err)
		}
	}

	startOpts := extensionStartOptions{
		debug:       a.globalOptions.EnableDebugLogging,
		cwd:         a.globalOptions.Cwd,
		environment: environmentName,
		noPrompt:    a.globalOptions.NoPrompt,
		forceColor:  !color.NoColor,
	}

	// Start the required extensions concurrently, mirroring ExtensionsMiddleware.
	startErrs := make([]error, len(toStart))
	var wg sync.WaitGroup
	for i, ext := range toStart {
		wg.Go(func() {
			startErrs[i] = startAndWaitExtension(ctx, ext, a.extensionRunner, serverInfo, startOpts)
		})
	}
	wg.Wait()

	for i, startErr := range startErrs {
		if startErr == nil {
			continue
		}

		// Fail fast with the actual startup error rather than an opaque resolution failure later.
		ext := toStart[i]
		if reported := ext.GetReportedError(); reported != nil {
			startErr = fmt.Errorf("%w: %w", startErr, reported)
		}

		cleanup()
		return noop, &internal.ErrorWithSuggestion{
			Err: fmt.Errorf("extension '%s', which provides provisioning provider '%s', failed to start: %w",
				ext.Id, strings.Join(declaredProviders(ext, names), "', '"), startErr),
			Suggestion: fmt.Sprintf(
				"Run with %s for details, or check for an update with %s",
				output.WithHighLightFormat("--debug"),
				output.WithHighLightFormat("azd extension upgrade %s", ext.Id),
			),
		}
	}

	return cleanup, nil
}

// SuggestExtensionForProvider returns the id of a registry extension that declares the named
// provisioning provider when no installed extension does, or an empty string when the provider is
// unknown to the registry, already declared by an installed extension, or the lookup fails.
func (a *ExtensionActivator) SuggestExtensionForProvider(ctx context.Context, providerName string) string {
	if strings.TrimSpace(providerName) == "" {
		return ""
	}

	// An installed extension declares this provider, so an install suggestion would be misleading.
	if installed, err := a.extensionManager.ListInstalled(); err == nil {
		if len(extensionsForProviders(installed, []string{providerName})) > 0 {
			return ""
		}
	}

	matches, err := a.extensionManager.FindExtensions(ctx, &extensions.FilterOptions{
		Capability: extensions.ProvisioningProviderCapability,
		Provider:   providerName,
	})
	if err != nil || len(matches) == 0 {
		return ""
	}

	return matches[0].Id
}

// providerResolvable reports whether the named provisioning provider already resolves from the
// IoC container, meaning the extension registering it is already running.
func (a *ExtensionActivator) providerResolvable(providerName string) bool {
	var provider provisioning.Provider
	return a.serviceLocator.ResolveNamed(providerName, &provider) == nil
}

// distinctProviderNames reduces the given provisioning provider names to a distinct
// (case-insensitive), non-empty set, preserving first-seen order and spelling.
func distinctProviderNames(providerNames []string) []string {
	seen := make(map[string]struct{}, len(providerNames))
	names := make([]string, 0, len(providerNames))
	for _, name := range providerNames {
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, has := seen[key]; has {
			continue
		}
		seen[key] = struct{}{}
		names = append(names, name)
	}

	return names
}

// extensionsForProviders returns the installed extensions that advertise the provisioning-provider
// capability and declare at least one of the given provider names. When several installed
// extensions declare the same provider, the one with the lexically smallest id wins so the choice
// is deterministic. The result is ordered by extension id.
func extensionsForProviders(
	installed map[string]*extensions.Extension,
	providerNames []string,
) []*extensions.Extension {
	installedIds := slices.Sorted(maps.Keys(installed))

	byId := map[string]*extensions.Extension{}
	for _, name := range providerNames {
		for _, id := range installedIds {
			ext := installed[id]
			if !ext.HasCapability(extensions.ProvisioningProviderCapability) {
				continue
			}
			if providerFromExtension(ext, name) {
				byId[ext.Id] = ext
				break
			}
		}
	}

	result := make([]*extensions.Extension, 0, len(byId))
	for _, id := range slices.Sorted(maps.Keys(byId)) {
		result = append(result, byId[id])
	}

	return result
}

// declaredProviders returns the subset of the requested provider names that the extension declares.
func declaredProviders(ext *extensions.Extension, providerNames []string) []string {
	declared := make([]string, 0, len(providerNames))
	for _, name := range providerNames {
		if providerFromExtension(ext, name) {
			declared = append(declared, name)
		}
	}

	return declared
}

// providerFromExtension reports whether the extension declares a provider with the given name.
func providerFromExtension(ext *extensions.Extension, providerName string) bool {
	return slices.ContainsFunc(ext.Providers, func(p extensions.Provider) bool {
		return strings.EqualFold(p.Name, providerName)
	})
}
