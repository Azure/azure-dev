// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.demo/internal/project"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newListenCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "listen",
		Short: "Starts the extension and listens for events.",
		RunE: func(cmd *cobra.Command, args []string) error {
			runCtx, cancel := context.WithCancel(cmd.Context())
			defer cancel()
			ctx := azdext.WithAccessToken(runCtx)

			// Create a new AZD client.
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			host := azdext.NewExtensionHost(azdClient)
			configureExtensionHostWithOutput(host, cmd.OutOrStdout())

			if err := host.Run(ctx); err != nil {
				return fmt.Errorf("failed to run extension: %w", err)
			}
			return nil
		},
	}

	return cmd
}

// configureExtensionHost wires the demo extension's providers and event handlers onto
// the supplied host, so tests can verify the registrations against extension.yaml.
func configureExtensionHost(host *azdext.ExtensionHost) {
	configureExtensionHostWithOutput(host, io.Discard)
}

func configureExtensionHostWithOutput(host *azdext.ExtensionHost, output io.Writer) {
	azdClient := host.Client()

	host.
		WithServiceTarget("demo", func() azdext.ServiceTargetProvider {
			return project.NewDemoServiceTargetProvider(azdClient)
		}).
		WithFrameworkService("rust", func() azdext.FrameworkServiceProvider {
			return project.NewDemoFrameworkServiceProvider(azdClient)
		}).
		WithProvisioningProvider("demo", func() azdext.ProvisioningProvider {
			return project.NewDemoProvisioningProvider(azdClient)
		}).
		WithValidationCheck(azdext.ValidationCheckRegistration{
			// Bicep-only check: runs during BicepProvider provision validation
			// and receives the Bicep snapshot / ARM template context. It is
			// skipped gracefully when no snapshot is available (e.g. a
			// non-Bicep provider), but is not dead code for Bicep.
			CheckType: azdext.ValidationCheckTypeArmProvision,
			RuleID:    "demo_warning",
			Factory: func() azdext.ValidationCheckProvider {
				return project.NewDemoValidationCheck()
			},
		}).
		WithValidationCheck(azdext.ValidationCheckRegistration{
			// Provider-agnostic check: runs before provisioning for every
			// provider (Bicep, Terraform, and extension providers such as
			// this demo provider). Receives the lean provision context.
			CheckType: azdext.ValidationCheckTypeProvision,
			RuleID:    "demo_provision_warning",
			Factory: func() azdext.ValidationCheckProvider {
				return project.NewDemoProvisionValidationCheck()
			},
		}).
		WithProjectEventHandler("preprovision", func(ctx context.Context, args *azdext.ProjectEventArgs) error {
			return runDemoWork(ctx, func(index int) error {
				_, err := fmt.Fprintf(output, "%d. Doing important work in extension...\n", index)
				return err
			})
		}).
		WithProjectEventHandler("predeploy", func(ctx context.Context, args *azdext.ProjectEventArgs) error {
			return runDemoWork(ctx, func(index int) error {
				_, err := fmt.Fprintf(
					output,
					"%d. Doing important predeploy project work in extension...\n",
					index,
				)
				return err
			})
		}).
		WithProjectEventHandler("postdeploy", func(ctx context.Context, args *azdext.ProjectEventArgs) error {
			return runDemoWork(ctx, func(index int) error {
				_, err := fmt.Fprintf(
					output,
					"%d. Doing important postdeploy project work in extension...\n",
					index,
				)
				return err
			})
		}).
		WithServiceEventHandler("prepackage", func(ctx context.Context, args *azdext.ServiceEventArgs) error {
			return runDemoWork(ctx, func(int) error {
				_, err := fmt.Fprintf(
					output,
					"Service: %s, Artifacts: %d\n",
					args.Service.Name,
					len(args.ServiceContext.Package),
				)
				return err
			})
		}, nil).
		WithServiceEventHandler("postpackage", func(ctx context.Context, args *azdext.ServiceEventArgs) error {
			return runDemoWork(ctx, func(int) error {
				_, err := fmt.Fprintf(
					output,
					"Service: %s, Artifacts: %d\n",
					args.Service.Name,
					len(args.ServiceContext.Package),
				)
				return err
			})
		}, nil).
		WithBetaServiceEventHandler(
			"predeploy",
			func(_ context.Context, args *azdext.ServiceEventArgs) (*azdext.BetaServiceEventResponse, error) {
				return &azdext.BetaServiceEventResponse{
					Messages: []azdext.BetaServiceEventMessage{{
						Kind:    azdext.BetaServiceEventMessageInfo,
						Message: fmt.Sprintf("Preparing service %q for deployment.", args.Service.Name),
					}},
				}, nil
			},
			nil,
		).
		WithBetaServiceEventHandler(
			"postdeploy",
			func(_ context.Context, args *azdext.ServiceEventArgs) (*azdext.BetaServiceEventResponse, error) {
				return &azdext.BetaServiceEventResponse{
					Messages: []azdext.BetaServiceEventMessage{{
						Kind:       azdext.BetaServiceEventMessageWarning,
						Message:    fmt.Sprintf("Demo warning for service %q.", args.Service.Name),
						Suggestion: "This is an example structured deploy message.",
					}},
				}, nil
			},
			nil,
		)
}

func runDemoWork(ctx context.Context, write func(int) error) error {
	for index := 1; index <= 20; index++ {
		if err := write(index); err != nil {
			return err
		}
		if index == 20 {
			break
		}

		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}
