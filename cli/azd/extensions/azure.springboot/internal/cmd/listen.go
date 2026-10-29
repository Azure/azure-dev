// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"

	"azurespringboot/internal/spring"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func configureExtensionHost(host *azdext.ExtensionHost) {
	runtime := spring.NewRuntime(host.Client())

	host.
		WithFrameworkService("java", func() azdext.FrameworkServiceProvider {
			return spring.NewJavaFrameworkService(host.Client())
		}).
		WithServiceTarget(spring.SpringHost, func() azdext.ServiceTargetProvider {
			return spring.NewServiceTarget(host.Client(), runtime)
		}).
		WithProjectEventHandler("preprovision", func(ctx context.Context, args *azdext.ProjectEventArgs) error {
			return runtime.PreProvision(ctx, args)
		}).
		WithProjectEventHandler("postprovision", func(ctx context.Context, args *azdext.ProjectEventArgs) error {
			return runtime.PostProvision(ctx, args)
		}).
		WithServiceEventHandler("prebuild", func(ctx context.Context, args *azdext.ServiceEventArgs) error {
			return runtime.PreBuild(ctx, args)
		}, &azdext.ServiceEventOptions{Host: spring.SpringHost}).
		WithServiceEventHandler("predeploy", func(ctx context.Context, args *azdext.ServiceEventArgs) error {
			return runtime.PreDeploy(ctx, args)
		}, &azdext.ServiceEventOptions{Host: spring.SpringHost}).
		WithServiceEventHandler("postdeploy", func(ctx context.Context, args *azdext.ServiceEventArgs) error {
			return runtime.PostDeploy(ctx, args)
		}, &azdext.ServiceEventOptions{Host: spring.SpringHost})
}
