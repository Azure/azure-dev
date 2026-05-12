// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"azureaiagent/internal/cmd/doctor"
	"azureaiagent/internal/cmd/nextstep"
	"azureaiagent/internal/version"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// doctorFlags are the Cobra-bound flags for `azd ai agent doctor`.
//
// localOnly is exposed today as a no-op: every shipped check is local
// (Phase 4 covers checks 1–6). The Cobra surface is locked early so the
// Phase 5 follow-up that adds remote checks does not need to introduce
// the flag in the same commit as the new check implementations.
//
// output selects the rendering path: "text" (default, human-readable
// with a trailing Next: block on success) or "json" (structured envelope
// for scripted consumers).
//
// unredacted is reserved for Phase 5 — once remote checks surface
// principal IDs, scope ARNs, and UPNs, this flag will toggle the
// redaction layer. It is bound today and threaded into doctor.Options
// so that callers (and tests) can already exercise the wire without
// the future Phase 5 fix-up touching the Cobra surface.
type doctorFlags struct {
	localOnly  bool
	output     string
	unredacted bool
}

func newDoctorCommand() *cobra.Command {
	flags := &doctorFlags{}

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose problems with an azd ai agent project.",
		Long: `Diagnose problems with an azd ai agent project.

Runs a sequence of local checks against the current azd project,
reporting on each one and (when all checks pass) suggesting the next
command to run. Use this when you have lost terminal context or hit a
confusing error and want a complete picture of the project's state.

Exit codes:
  0 — at least one check passed and no checks failed
  1 — any check failed
  2 — all checks were skipped (e.g. preconditions unmet)`,
		Example: `  # Run the full check suite with human-readable output
  azd ai agent doctor

  # Emit a structured JSON envelope (for scripts / CI)
  azd ai agent doctor --output json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateDoctorFlags(flags); err != nil {
				return err
			}

			ctx := azdext.WithAccessToken(cmd.Context())
			logCleanup := setupDebugLogging(cmd.Flags())
			defer logCleanup()

			// NewAzdClient errors are not fatal — the gRPC check
			// (`local.grpc-extension`) surfaces the failure verbatim
			// to the user, and downstream checks Skip cleanly when
			// the client is nil. We deliberately do NOT short-circuit
			// the command here.
			azdClient, clientErr := azdext.NewAzdClient()
			if azdClient != nil {
				defer azdClient.Close()
			}

			deps := doctor.Dependencies{
				AzdClient:        azdClient,
				AzdClientErr:     clientErr,
				ExtensionVersion: version.Version,
			}

			opts := doctor.Options{
				LocalOnly:  flags.localOnly,
				Unredacted: flags.unredacted,
			}

			report, trailing := runDoctor(ctx, deps, opts, azdClient)
			if err := renderDoctorReport(os.Stdout, flags.output, report, trailing); err != nil {
				return err
			}

			// Exit codes are part of the doctor contract (see design
			// `docs/design/azd-ai-agent-nextsteps.md`, "Exit codes &
			// JSON output"). Cobra/azdext maps a nil return to exit 0
			// and any non-nil return to exit 1, which collapses our
			// three-state contract into a two-state one. We call
			// os.Exit directly to preserve the 0/1/2 distinction.
			//
			// os.Exit does NOT run deferred functions. The deferred
			// logCleanup and azdClient.Close above will not execute on
			// the non-zero path. This is acceptable today because the
			// process exits immediately and the OS reclaims the gRPC
			// socket and (in --debug mode) the log fd; neither defer
			// has on-disk state to flush. Do NOT add cleanup-critical
			// defers to this RunE — call them explicitly before
			// os.Exit instead.
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
		"Run only local checks (no network calls). "+
			"All checks are local today; this flag is reserved for an upcoming remote-checks pass.",
	)
	cmd.Flags().StringVarP(
		&flags.output, "output", "o", "text",
		"Output format (text or json).",
	)
	cmd.Flags().BoolVar(
		&flags.unredacted, "unredacted", false,
		"Show raw principal IDs, scope ARNs, and UPNs in the report. "+
			"Reserved for the upcoming remote-checks pass (no-op today).",
	)

	return cmd
}

// validateDoctorFlags enforces the closed set of values for --output. We
// validate before any work so an obvious typo (`--output yaml`) does not
// run the entire check suite only to print nothing useful.
func validateDoctorFlags(flags *doctorFlags) error {
	switch flags.output {
	case "text", "json":
		return nil
	default:
		return fmt.Errorf("invalid --output value %q (must be 'text' or 'json')", flags.output)
	}
}

// runDoctor is the testable core of the doctor command. It constructs a
// Runner from the configured checks, executes it, and (when the report
// is clean) resolves a trailing Next: block via the nextstep resolver.
//
// The trailing block is computed unconditionally but only rendered by
// the text formatter — the JSON envelope deliberately excludes it (see
// design spec, "Exit codes & JSON output"). Computing it here keeps the
// expensive bit (gRPC round-trip in AssembleStateFromSource) out of the
// formatter and lets tests assert the resolver branch by inspection.
//
// azdClient may be nil when NewAzdClient failed at startup; in that
// case the trailing block is skipped (resolver has no state to work
// with). The function never returns an error: every failure mode is
// captured in the Report or in a skipped trailing block.
func runDoctor(
	ctx context.Context,
	deps doctor.Dependencies,
	opts doctor.Options,
	azdClient *azdext.AzdClient,
) (doctor.Report, []nextstep.Suggestion) {
	runner := doctor.Runner{Checks: doctor.NewLocalChecks(deps)}
	report := runner.Run(ctx, opts)

	// Trailing Next: block is only meaningful when checks all pass
	// (exit code 0). On Fail or all-skip, the user's next move is to
	// fix the surfaced problem — burying that under "Next: azd deploy"
	// would be noise. Locked by the design spec at
	// `docs/design/azd-ai-agent-nextsteps.md`, "Doctor output shape":
	// "When all checks pass, the trailing Next: block is ...".
	if doctor.ExitCode(report) != 0 {
		return report, nil
	}

	trailing := resolveDoctorTrailing(ctx, azdClient)
	return report, trailing
}

// resolveDoctorTrailing assembles state from the azd gRPC channel and
// asks the nextstep resolver for the doctor's trailing block.
// Returns nil on any error — the trailing block is a courtesy, not a
// load-bearing surface, and the body of the doctor report already
// tells the user what to do.
//
// Branch selection:
//   - Any service in azure.yaml has IsDeployed == true →
//     ResolveAfterDeploy (filtered to deployed services). The resolver
//     emits show + invoke for each deployed agent.
//   - No service deployed → ResolveAfterInit. Same block the user saw
//     at the end of `azd ai agent init`, which guides them toward
//     `azd provision` / `azd ai agent run` / `azd deploy`.
func resolveDoctorTrailing(ctx context.Context, azdClient *azdext.AzdClient) []nextstep.Suggestion {
	if azdClient == nil {
		return nil
	}

	state, _ := nextstep.AssembleStateFromSource(ctx, nextstep.NewSource(azdClient))
	if len(state.Services) == 0 {
		// Healthy project but no agent services in azure.yaml — the
		// init resolver still produces a useful "run azd ai agent
		// init" hint via its empty-services branch, but for doctor
		// the body of the report already covered that via the
		// `local.agent-service-detected` check. Emitting the same
		// hint twice is noise.
		return nil
	}

	if anyServiceDeployed(state.Services) {
		// Capture the total agent-service count BEFORE filtering. The
		// resolver's `len(state.Services) == 1` heuristic ordinarily
		// keys "should I emit no-arg show/invoke commands?" off the
		// total count of agent services in azure.yaml. Once we filter
		// to deployed-only, that heuristic breaks: a 2-service project
		// with 1 deployed would emit `azd ai agent show` (no name),
		// but runtime `resolveAgentService` still sees both services
		// in azure.yaml and would either prompt or error. Forcing
		// qualified suggestions whenever azure.yaml has multiple
		// services preserves copy-paste correctness in the partial-
		// deploy case and is a no-op when all services are deployed
		// (the resolver naturally qualifies len > 1 anyway).
		totalServices := len(state.Services)
		filtered := filterDeployedServices(state)
		return nextstep.ResolveAfterDeploy(
			filtered,
			doctorCachedPayload(ctx, azdClient),
			doctorReadmeExists(ctx, azdClient),
			nextstep.AfterDeployOpts{ForceQualified: totalServices > 1},
		)
	}

	return nextstep.ResolveAfterInit(state)
}

func anyServiceDeployed(services []nextstep.ServiceState) bool {
	for _, s := range services {
		if s.IsDeployed {
			return true
		}
	}
	return false
}

// filterDeployedServices returns a shallow clone of state whose Services
// list contains only the entries with IsDeployed == true. The clone is
// necessary because ResolveAfterDeploy emits one show + one invoke
// per Service it sees; passing an unfiltered state would produce
// `azd ai agent invoke <undeployed-service>` lines, which 404.
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

// doctorCachedPayload returns a cachedPayload closure for
// ResolveAfterDeploy. It looks up the cached remote OpenAPI spec (the
// one populated by prior `azd ai agent invoke` runs) and extracts a
// sample payload via ExtractInvokeExample. Returns "" on any failure
// so the resolver falls back to its protocol-generic literal.
//
// Suffix is "remote" because doctor's trailing block emits commands
// for the deployed agent (`azd ai agent invoke <agent>`); the local
// cache (suffix "local") is from `azd ai agent invoke --local` and is
// not appropriate here.
//
// Key resolution: the on-disk cache is keyed by the deployed Foundry
// agent name (see invoke.go:694-758 — invoke rewrites `name` to
// `info.AgentName` BEFORE caching). That can differ from the azure.yaml
// service name when deploy appends a suffix (documented in
// show.go:40-46). The closure first tries the deployed name via the
// `AGENT_<SERVICE>_NAME` env var, then falls back to the service name
// when the env value is absent (e.g., never-deployed service, or older
// deploys that did not populate the var). The fallback also covers the
// non-divergent case where the two names are identical.
func doctorCachedPayload(ctx context.Context, azdClient *azdext.AzdClient) func(string) string {
	// Resolve the active env name once for the closure's lifetime.
	// A nil/error response leaves envName empty, which short-circuits
	// the deployed-name lookup path inside the closure.
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

		// Try the deployed agent name first.
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

		// Fall back to service-name keyed cache for the non-divergent
		// case (and for projects whose AGENT_<SERVICE>_NAME var is
		// absent for any reason).
		spec, err := nextstep.ReadCachedOpenAPISpec(configDir, serviceName, "remote")
		if err != nil {
			return ""
		}
		return nextstep.ExtractInvokeExample(spec)
	}
}

// doctorReadmeExists returns a readmeExists closure for
// ResolveAfterDeploy. The closure resolves the project root once
// (cached across calls) and reports whether
// <projectRoot>/<relativePath>/README.md exists.
//
// Only the canonical "README.md" casing is checked, matching the
// rendered "see <relPath>/README.md" line; accepting other casings
// would yield a broken pointer on case-sensitive filesystems.
func doctorReadmeExists(ctx context.Context, azdClient *azdext.AzdClient) func(string) bool {
	projectRoot := resolveProjectPath(ctx, azdClient)
	return func(relativePath string) bool {
		if projectRoot == "" || relativePath == "" {
			return false
		}
		_, err := os.Stat(filepath.Join(projectRoot, relativePath, "README.md"))
		return err == nil
	}
}
