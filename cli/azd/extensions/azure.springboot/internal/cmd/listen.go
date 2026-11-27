// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"

	"azurespringboot/internal/deploy"
	"azurespringboot/internal/lifecycle"
	"azurespringboot/internal/packaging"
	"azurespringboot/internal/springmodel"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func configureExtensionHost(host *azdext.ExtensionHost) {
	configureExtensionHostWithNoPrompt(host, false)
}

func configureExtensionHostWithNoPrompt(host *azdext.ExtensionHost, noPrompt bool) {
	runtime := lifecycle.NewRuntime(host.Client(), noPrompt)

	host.
		WithFrameworkService("java", func() azdext.FrameworkServiceProvider {
			return packaging.NewJavaFrameworkService(host.Client())
		}).
		WithServiceTarget(springmodel.SpringHost, func() azdext.ServiceTargetProvider {
			return deploy.NewServiceTarget(host.Client(), runtime, runtime.EnvironmentStore())
		}).
		WithProjectEventHandler("preprovision", func(ctx context.Context, args *azdext.ProjectEventArgs) error {
			return runtime.PreProvision(ctx, args)
		}).
		WithProjectEventHandler("postprovision", func(ctx context.Context, args *azdext.ProjectEventArgs) error {
			return runtime.PostProvision(ctx, args)
		}).
		WithServiceEventHandler("prebuild", func(ctx context.Context, args *azdext.ServiceEventArgs) error {
			return runtime.PreBuild(ctx, args)
		}, &azdext.ServiceEventOptions{Host: springmodel.SpringHost}).
		WithServiceEventHandler("predeploy", func(ctx context.Context, args *azdext.ServiceEventArgs) error {
			return runtime.PreDeploy(ctx, args)
		}, &azdext.ServiceEventOptions{Host: springmodel.SpringHost})
}
