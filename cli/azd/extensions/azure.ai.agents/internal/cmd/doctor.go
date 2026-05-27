// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"azureaiagent/internal/cmd/doctor"
	"azureaiagent/internal/cmd/nextstep"
	"azureaiagent/internal/version"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// doctorFlags are the Cobra-bound flags for `azd ai agent doctor`.
type doctorFlags struct {
	localOnly  bool
	unredacted bool
}

func newDoctorCommand() *cobra.Command {
	flags := &doctorFlags{}

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose problems with an azd ai agent project.",
		Long: `Diagnose problems with an azd ai agent project.

Runs a sequence of local and remote checks against the current azd project,
reporting on each one and (when all checks pass) suggesting the next
command to run. Use this when you have lost terminal context or hit a
confusing error and want a complete picture of the project's state.

Exit codes:
  0 — at least one check passed and no checks failed
  1 — any check failed
  2 — all checks were skipped (e.g. preconditions unmet)`,
		Example: `  # Run the full check suite
  azd ai agent doctor`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			logCleanup := setupDebugLogging(cmd.Flags())
			defer logCleanup()

			// `--debug` (persistent root flag) also toggles the verbose per-check
			// detail block in the doctor report.
			debug := isDebug(cmd.Flags())

			// Let `local.grpc-extension` report client creation failures so
			// downstream checks can skip instead of duplicating the error.
			azdClient, clientErr := azdext.NewAzdClient()
			if azdClient != nil {
				defer azdClient.Close()
			}

			deps := doctor.Dependencies{
				AzdClient:        azdClient,
				AzdClientErr:     clientErr,
				ExtensionVersion: version.Version,
				AgentAPIVersion:  DefaultAgentAPIVersion,
			}

			opts := doctor.Options{
				LocalOnly:  flags.localOnly,
				Unredacted: flags.unredacted,
			}

			report, err := runAndRenderDoctorText(ctx, deps, opts, azdClient, os.Stdout, debug)
			if err != nil {
				return err
			}

			// Use os.Exit to preserve doctor's 0/1/2 exit-code contract;
			// Cobra/azdext would otherwise collapse all errors to 1.
			// os.Exit skips defers, so do not add cleanup-critical defers here.
			code := doctor.ExitCode(report)
			if code == 0 {
				return nil
			}
			os.Exit(code)
			return nil // unreachable
		},
	}

	cmd.Flags().BoolVar(
		&flags.localOnly, "local-only", false,
		"Skip remote (network-dependent) checks. "+
			"Useful when offline, behind a proxy, or for a fast local triage.",
	)
	cmd.Flags().BoolVar(
		&flags.unredacted, "unredacted", false,
		"Show raw principal IDs, scope ARNs, and UPNs in the report.",
	)

	return cmd
}

// runAndRenderDoctorText streams human-readable output as checks complete.
// `debug` switches between the default concise rendering and the verbose
// per-check Message/Suggestion/Links block.
func runAndRenderDoctorText(
	ctx context.Context,
	deps doctor.Dependencies,
	opts doctor.Options,
	azdClient *azdext.AzdClient,
	w io.Writer,
	debug bool,
) (doctor.Report, error) {
	renderer := newDoctorRenderer(w, debug)
	if err := renderer.writeHeader(); err != nil {
		return doctor.Report{}, err
	}

	report, trailing, err := runDoctorWithObserver(
		ctx,
		deps,
		opts,
		azdClient,
		func(result doctor.Result) error {
			return renderer.writeCheck(result)
		},
	)
	if err != nil {
		return report, err
	}

	showNext := len(trailing) > 0 && writerIsTerminal(w)
	if err := renderer.writeFooter(report, trailing, showNext); err != nil {
		return report, err
	}
	return report, nil
}

func runDoctorWithObserver(
	ctx context.Context,
	deps doctor.Dependencies,
	opts doctor.Options,
	azdClient *azdext.AzdClient,
	observer doctor.ResultObserver,
) (doctor.Report, []nextstep.Suggestion, error) {
	// Keep local checks first so remote checks can inspect their prior
	// results for skip-cascade decisions.
	checks := append(doctor.NewLocalChecks(deps), doctor.NewRemoteChecks(deps)...)
	runner := doctor.Runner{Checks: checks}
	report, err := runner.RunWithObserver(ctx, opts, observer)
	if err != nil {
		return report, nil, err
	}

	// Show trailing Next: only on clean reports; otherwise it competes with
	// the failing check's remediation.
	if doctor.ExitCode(report) != 0 {
		return report, nil, nil
	}

	trailing := resolveDoctorTrailing(ctx, azdClient)
	return report, trailing, nil
}

// resolveDoctorTrailing returns the doctor's trailing Next block, or nil on
// error. It chooses deployed-agent suggestions when any service is deployed;
// otherwise it reuses the post-init guidance.
func resolveDoctorTrailing(ctx context.Context, azdClient *azdext.AzdClient) []nextstep.Suggestion {
	if azdClient == nil {
		return nil
	}

	state, _ := nextstep.AssembleStateFromSource(ctx, nextstep.NewSource(azdClient))
	if len(state.Services) == 0 {
		// Avoid repeating the missing-service guidance already reported by
		// `local.agent-service-detected`.
		return nil
	}

	if anyServiceDeployed(state.Services) {
		// Filter to deployed services so the generated invoke/show commands
		// stay copy-paste correct.
		return nextstep.ResolveAfterDeploy(
			filterDeployedServices(state),
			doctorCachedPayload(ctx, azdClient),
			readmeExistsForProject(ctx, azdClient),
		)
	}

	return nextstep.ResolveAfterInit(state, readmeExistsForProject(ctx, azdClient))
}

func anyServiceDeployed(services []nextstep.ServiceState) bool {
	for _, s := range services {
		if s.IsDeployed {
			return true
		}
	}
	return false
}

// filterDeployedServices returns a shallow clone with only deployed services.
func filterDeployedServices(state *nextstep.State) *nextstep.State {
	if state == nil {
		return nil
	}
	clone := *state
	clone.Services = make([]nextstep.ServiceState, 0, len(state.Services))
	for _, s := range state.Services {
		if s.IsDeployed {
			clone.Services = append(clone.Services, s)
		}
	}
	return &clone
}

// doctorCachedPayload returns a remote-cache lookup closure for ResolveAfterDeploy.
// It returns "" on failure and tries deployed Foundry agent names before
// falling back to azure.yaml service names.
func doctorCachedPayload(ctx context.Context, azdClient *azdext.AzdClient) func(string) string {
	var envName string
	if azdClient != nil {
		if envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{}); err == nil &&
			envResp != nil && envResp.Environment != nil {
			envName = envResp.Environment.Name
		}
	}

	return func(serviceName string) string {
		if azdClient == nil || serviceName == "" {
			return ""
		}
		configPath, err := resolveConfigPath(ctx, azdClient)
		if err != nil {
			return ""
		}
		configDir := filepath.Dir(configPath)

		if envName != "" {
			nameKey := fmt.Sprintf("AGENT_%s_NAME", toServiceKey(serviceName))
			if v, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
				EnvName: envName,
				Key:     nameKey,
			}); err == nil && v != nil && v.Value != "" && v.Value != serviceName {
				if spec, err := nextstep.ReadCachedOpenAPISpec(configDir, v.Value, "remote"); err == nil {
					if payload := nextstep.ExtractInvokeExample(spec); payload != "" {
						return payload
					}
				}
			}
		}

		spec, err := nextstep.ReadCachedOpenAPISpec(configDir, serviceName, "remote")
		if err != nil {
			return ""
		}
		return nextstep.ExtractInvokeExample(spec)
	}
}

// doctorReadmeExists has moved to helpers.go as readmeExistsForProject
// so that ResolveAfterInit / ResolveAfterRun / ResolveAfterDeploy
// callers (init.go, init_from_code.go, run.go, doctor.go) all share
// the same README-detection contract.
