// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"azureaiagent/internal/cmd/nextstep"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// doctorStatus is the outcome of a single health check.
type doctorStatus int

const (
	doctorOK doctorStatus = iota
	doctorWarn
	doctorFail
	doctorSkip
	// doctorInfo is a 5th status used only by check 12 (agent identity
	// roles) when there is nothing actionable to flag — the user inspects
	// the rendered role list to confirm it matches their mental model.
	// Warn / Fail variants of the same check surface only the actionable
	// cases. Renders as `ⓘ INFO`; exit code is treated as a pass.
	doctorInfo
)

// doctorReportSchemaVersion is the version of the JSON envelope emitted by
// `azd ai agent doctor --output json`. Bump on any breaking schema change.
const doctorReportSchemaVersion = "1.0"

// Redaction patterns used by [redactDoctorString]. Centralized here so a
// single change applies to every render path (text + JSON).
//
// The patterns are deliberately broad — false positives just print more
// `<redacted>` markers, false negatives leak identifiers into CI logs.
//
//nolint:lll // patterns benefit from being on a single line for readability
var (
	// GUIDs in any string field. Matches Azure principal IDs, object IDs,
	// tenant IDs, role-definition IDs, and resource GUIDs.
	doctorGUIDRegex = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

	// ARM scope ARNs starting with /subscriptions/.... Replaced wholesale
	// because the GUID inside the subscription segment is already covered
	// by [doctorGUIDRegex] but the full ARN still reveals resource group +
	// resource names.
	doctorScopeARNRegex = regexp.MustCompile(`/subscriptions/[^\s'"]+`)

	// UPNs / email-shaped identifiers. The trailing `[^\s/'"]+` is
	// conservative so we don't accidentally redact URL hosts like
	// `eastus.api.azureml.ms` — only `local-part@domain` shapes match.
	doctorUPNRegex = regexp.MustCompile(`[A-Za-z0-9_.+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
)

func (s doctorStatus) badge() string {
	switch s {
	case doctorOK:
		return color.GreenString("✓ PASS")
	case doctorWarn:
		return color.YellowString("! WARN")
	case doctorFail:
		return color.RedString("✗ FAIL")
	case doctorSkip:
		return color.HiBlackString("- SKIP")
	case doctorInfo:
		return color.CyanString("ⓘ INFO")
	}
	return "?"
}

// String returns the lowercase JSON-friendly name for the status.
func (s doctorStatus) String() string {
	switch s {
	case doctorOK:
		return "pass"
	case doctorWarn:
		return "warn"
	case doctorFail:
		return "fail"
	case doctorSkip:
		return "skip"
	case doctorInfo:
		return "info"
	}
	return "unknown"
}

// doctorResult is one row in the doctor output table.
type doctorResult struct {
	// ID is a stable machine-readable identifier (e.g., "local.azure-yaml").
	// Used by the JSON envelope so consumers can key off the check, not the
	// human-readable title.
	ID         string
	Title      string
	Status     doctorStatus
	Detail     string
	Fix        string // optional follow-up command (rendered via nextstep)
	Reason     string // optional human-friendly "why" caption for the fix; falls back to Title
	DurationMs int64  // wall-clock time spent on the check
}

// doctorJSONCheck mirrors the design's per-check JSON shape.
type doctorJSONCheck struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	Detail     string `json:"detail,omitempty"`
	Fix        string `json:"fix,omitempty"`
	DurationMs int64  `json:"durationMs"`
}

// doctorJSONReport is the envelope emitted under --output json. It mirrors
// the schema in the design doc; the human-friendly Next: block is intentionally
// omitted so machine consumers get a flat, deterministic structure.
type doctorJSONReport struct {
	SchemaVersion string            `json:"schemaVersion"`
	Remote        bool              `json:"remote"`
	Redacted      bool              `json:"redacted"`
	Checks        []doctorJSONCheck `json:"checks"`
}

// doctorFlags holds parsed CLI flags.
type doctorFlags struct {
	// localOnly skips remote (cloud) health checks. Today the implementation
	// only ships local checks (1–6); the flag surface is wired now so the
	// default can flip when remote checks land without breaking existing
	// invocations or scripts.
	localOnly bool
	// output is the rendering format: "default" (text) or "json".
	output string
	// unredacted disables redaction of principal IDs / scope ARNs in the
	// JSON envelope. Interactive-only escape hatch — ignored when stdout
	// is not a TTY (so CI logs never accidentally capture sensitive values).
	unredacted bool
}

// doctorAction implements `azd ai agent doctor`.
type doctorAction struct {
	azdClient *azdext.AzdClient
	out       io.Writer
	flags     *doctorFlags
}

func newDoctorCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	flags := &doctorFlags{}
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose your Azure AI Agent project setup",
		Long: "Runs a series of health checks against the current azd project and AI Agent " +
			"configuration. Reports pass / warn / fail per check along with the recommended " +
			"follow-up command for any non-passing item.\n\n" +
			"Exit codes: 0 = all checks passed (or only warnings), 1 = at least one failure, " +
			"2 = no checks could run (all skipped).",
		Example: `  # Run all health checks
  azd ai agent doctor

  # Skip remote (cloud) checks — fastest, works air-gapped
  azd ai agent doctor --local-only

  # Machine-readable JSON output
  azd ai agent doctor --output json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.output = extCtx.OutputFormat
			ctx := azdext.WithAccessToken(cmd.Context())
			client, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer client.Close()
			a := &doctorAction{azdClient: client, out: os.Stdout, flags: flags}
			return a.run(ctx)
		},
	}

	cmd.Flags().BoolVar(&flags.localOnly, "local-only", false,
		"Skip remote (cloud) checks. Useful in CI or air-gapped environments.")
	cmd.Flags().BoolVar(&flags.unredacted, "unredacted", false,
		"Show full principal IDs and scope ARNs in JSON output. Interactive use only.")

	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"default", "json"},
		Default:       "default",
	})

	return cmd
}

// run executes all checks, prints a report in the requested format, and
// terminates the process with the design's exit-code semantics:
//
//	0 — all checks passed (warnings allowed, at least one pass)
//	1 — at least one check failed
//	2 — every check was skipped (nothing actually ran)
func (a *doctorAction) run(ctx context.Context) error {
	results := a.runChecks(ctx)
	state, _ := nextstep.AssembleState(ctx, a.azdClient)

	if strings.EqualFold(a.flags.output, "json") {
		if err := writeDoctorJSON(a.out, results, a.flags); err != nil {
			return err
		}
	} else {
		printDoctorReport(a.out, results, state, a.flags)
	}

	exitCode := computeDoctorExitCode(results)
	if exitCode != 0 {
		os.Exit(exitCode)
	}
	return nil
}

// computeDoctorExitCode maps the result set to an exit code.
//   - 1 if any check failed (Fail wins regardless of other rows).
//   - 2 if no check actually ran (every result is Skip).
//   - 0 otherwise (Pass / Warn / Pass+Skip mix).
func computeDoctorExitCode(results []doctorResult) int {
	hasFail := false
	hasNonSkip := false
	for _, r := range results {
		if r.Status == doctorFail {
			hasFail = true
		}
		if r.Status != doctorSkip {
			hasNonSkip = true
		}
	}
	switch {
	case hasFail:
		return 1
	case !hasNonSkip:
		return 2
	default:
		return 0
	}
}

// writeDoctorJSON renders the design's JSON envelope. When the report is
// marked redacted (stdout is non-TTY, or interactive without `--unredacted`),
// every `detail` and `fix` field is rewritten through [redactDoctorString]
// so principal IDs, scope ARNs, and full UPNs are replaced with
// `<redacted>` before serialization. The `redacted` field on the envelope
// signals to consumers which mode they are getting.
func writeDoctorJSON(w io.Writer, results []doctorResult, flags *doctorFlags) error {
	redacted := shouldRedactDoctorJSON(flags)
	// Defensive: nil flags is treated as "default invocation" — no
	// --local-only, no --unredacted. Keeps any future internal caller that
	// reuses this writer (e.g., tests) from panicking.
	remoteAttempted := flags == nil || !flags.localOnly
	report := doctorJSONReport{
		SchemaVersion: doctorReportSchemaVersion,
		// Remote reflects whether remote checks were *attempted*. With
		// --local-only the runner skips them; otherwise it would have run
		// them (even if 0 are implemented today).
		Remote:   remoteAttempted,
		Redacted: redacted,
		Checks:   make([]doctorJSONCheck, 0, len(results)),
	}
	for _, r := range results {
		report.Checks = append(report.Checks, doctorJSONCheck{
			ID:         r.ID,
			Title:      r.Title,
			Status:     r.Status.String(),
			Detail:     redactDoctorString(r.Detail, redacted),
			Fix:        redactDoctorString(r.Fix, redacted),
			DurationMs: r.DurationMs,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

// shouldRedactDoctorJSON returns true when the JSON envelope must mark
// values as redacted. Rule: redact when stdout is not a TTY OR when the
// caller is on a TTY but did not pass --unredacted. Interactive users can
// opt in to raw values; CI / piped output is always redacted.
//
// The fields rewritten by [redactDoctorString] are principal IDs (GUIDs),
// scope ARNs (`/subscriptions/...`), and full UPNs in `Detail` / `Fix`.
// Local checks don't surface those today; remote checks 10 and 12 do.
func shouldRedactDoctorJSON(flags *doctorFlags) bool {
	// Defensive: a nil flags value defaults to "redact". Any future internal
	// caller that forgets to thread flags through gets the safe behavior.
	if flags == nil {
		return true
	}
	if !isTerminalStdout() {
		return true
	}
	return !flags.unredacted
}

// redactDoctorString rewrites sensitive identifiers to the literal
// `<redacted>` so check results can be safely piped into CI logs. Today
// it covers:
//
//   - principal/object IDs (GUIDs like 8eb8d4f6-1d4f-4f1a-9a7d-8b6e2c8b0e1d)
//   - ARM scope ARNs (`/subscriptions/<guid>/...`)
//   - full UPNs (anything containing an `@` outside of a URL host)
//
// The function intentionally over-redacts when the caller is unsure:
// false positives just leak more `<redacted>` placeholders in output;
// false negatives leak real identifiers into CI. When `redacted` is
// false the input is returned unchanged.
func redactDoctorString(s string, redacted bool) string {
	if !redacted || s == "" {
		return s
	}
	out := s
	out = doctorGUIDRegex.ReplaceAllString(out, "<redacted>")
	out = doctorScopeARNRegex.ReplaceAllString(out, "<redacted>")
	out = doctorUPNRegex.ReplaceAllString(out, "<redacted>")
	return out
}

// runChecks executes the diagnostic checks. The order is stable so output
// is deterministic — earlier checks gate later ones where it makes sense
// (e.g., environment must exist before reading AZURE_AI_PROJECT_ENDPOINT).
//
// Each check is wrapped via timed() so the JSON envelope can surface a
// per-check duration. Today every check is local; remote checks (7–12)
// gated behind --local-only will be folded in here once they land.
func (a *doctorAction) runChecks(ctx context.Context) []doctorResult {
	out := []doctorResult{}

	// 1. azd CLI present.
	out = append(out, timed(func() doctorResult {
		return doctorResult{
			ID:     "local.azd-cli",
			Title:  "azd CLI is installed and reachable",
			Status: doctorOK,
			Detail: "extension running, gRPC channel established",
		}
	}))

	// 2. Project loaded.
	var projectPath string
	out = append(out, timed(func() doctorResult {
		path, res := a.checkProject(ctx)
		projectPath = path
		return res
	}))
	if out[len(out)-1].Status == doctorFail {
		// No project — bail out of subsequent checks that depend on it.
		return out
	}

	// 3. Current environment selected.
	var envName string
	out = append(out, timed(func() doctorResult {
		name, res := a.checkEnvironment(ctx)
		envName = name
		return res
	}))

	// 4. Agent service detected in azure.yaml.
	var agentServices []*azdext.ServiceConfig
	out = append(out, timed(func() doctorResult {
		svcs, res := a.checkAgentService(ctx)
		agentServices = svcs
		return res
	}))

	// 5. AZURE_AI_PROJECT_ENDPOINT set.
	var endpointValue string
	out = append(out, timed(func() doctorResult {
		ep, res := a.checkProjectEndpoint(ctx, envName)
		endpointValue = ep
		return res
	}))

	// 6. Local agent.yaml validity for each detected service.
	for _, manifest := range a.checkAgentManifest(projectPath, agentServices) {
		// Each manifest result is captured under its own duration window;
		// re-wrap so the timing reflects per-service work, not the loop.
		m := manifest
		out = append(out, timed(func() doctorResult { return m }))
	}

	// Remote checks 7–12. Gated by --local-only. When the flag is set, the
	// runner emits explicit Skip rows so users see why remote checks didn't
	// run — "never quietly drop a check" per the design.
	out = append(out, a.runRemoteChecks(ctx, remotePreconditions{
		endpointSet:   endpointValue != "",
		endpoint:      endpointValue,
		envName:       envName,
		projectPath:   projectPath,
		agentServices: agentServices,
	})...)

	return out
}

// timed runs fn and stamps the result's DurationMs from wall-clock time.
// Keeps the per-check timing cost (~ns) out of every check's hand-written
// body and means every result that flows through the runner has a duration.
func timed(fn func() doctorResult) doctorResult {
	start := time.Now()
	r := fn()
	r.DurationMs = time.Since(start).Milliseconds()
	return r
}

func (a *doctorAction) checkProject(ctx context.Context) (string, doctorResult) {
	resp, err := a.azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil || resp == nil || resp.Project == nil {
		return "", doctorResult{
			ID:     "local.project-loaded",
			Title:  "Project loaded from azure.yaml",
			Status: doctorFail,
			Detail: "no azure.yaml could be loaded from the working directory",
			Fix:    "azd ai agent init",
			Reason: "scaffold an agent project in the current directory",
		}
	}
	return resp.Project.Path, doctorResult{
		ID:     "local.project-loaded",
		Title:  "Project loaded from azure.yaml",
		Status: doctorOK,
		Detail: resp.Project.Path,
	}
}

func (a *doctorAction) checkEnvironment(ctx context.Context) (string, doctorResult) {
	resp, err := a.azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil || resp == nil || resp.Environment == nil || resp.Environment.Name == "" {
		return "", doctorResult{
			ID:     "local.env-selected",
			Title:  "Current azd environment selected",
			Status: doctorFail,
			Detail: "no environment is set; provisioned values cannot be read",
			Fix:    "azd env select <name>",
			Reason: "select an existing environment, or run `azd env new <name>` to create one",
		}
	}
	return resp.Environment.Name, doctorResult{
		ID:     "local.env-selected",
		Title:  "Current azd environment selected",
		Status: doctorOK,
		Detail: resp.Environment.Name,
	}
}

func (a *doctorAction) checkAgentService(ctx context.Context) ([]*azdext.ServiceConfig, doctorResult) {
	resp, err := a.azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil || resp == nil || resp.Project == nil {
		return nil, doctorResult{
			ID:     "local.agent-service",
			Title:  "Agent service detected in azure.yaml",
			Status: doctorSkip,
			Detail: "project not loaded",
		}
	}
	services := resp.Project.Services
	agents := make([]*azdext.ServiceConfig, 0, len(services))
	for _, s := range services {
		if s == nil {
			continue
		}
		if s.Host == nextstep.AiAgentHost || s.Host == nextstep.AiToolboxHost {
			agents = append(agents, s)
		}
	}
	if len(agents) == 0 {
		return nil, doctorResult{
			ID:     "local.agent-service",
			Title:  "Agent service detected in azure.yaml",
			Status: doctorWarn,
			Detail: "no service with host 'azure.ai.agent' or 'azure.ai.toolbox'",
			Fix:    "azd ai agent init",
			Reason: "add an agent service to azure.yaml",
		}
	}
	names := make([]string, 0, len(agents))
	for _, s := range agents {
		names = append(names, s.Name)
	}
	return agents, doctorResult{
		ID:     "local.agent-service",
		Title:  "Agent service detected in azure.yaml",
		Status: doctorOK,
		Detail: fmt.Sprintf("%d service(s): %v", len(agents), names),
	}
}

func (a *doctorAction) checkProjectEndpoint(ctx context.Context, envName string) (string, doctorResult) {
	if envName == "" {
		return "", doctorResult{
			ID:     "local.project-endpoint",
			Title:  "AZURE_AI_PROJECT_ENDPOINT is set",
			Status: doctorSkip,
			Detail: "no environment selected",
		}
	}
	resp, err := a.azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envName,
		Key:     "AZURE_AI_PROJECT_ENDPOINT",
	})
	if err != nil || resp == nil || resp.Value == "" {
		return "", doctorResult{
			ID:     "local.project-endpoint",
			Title:  "AZURE_AI_PROJECT_ENDPOINT is set",
			Status: doctorFail,
			Detail: "value missing from azd environment — agent cannot reach Foundry",
			Fix:    "azd provision",
			Reason: "deploy Azure resources to populate AZURE_AI_PROJECT_ENDPOINT",
		}
	}
	return resp.Value, doctorResult{
		ID:     "local.project-endpoint",
		Title:  "AZURE_AI_PROJECT_ENDPOINT is set",
		Status: doctorOK,
		Detail: resp.Value,
	}
}

func (a *doctorAction) checkAgentManifest(projectPath string, services []*azdext.ServiceConfig) []doctorResult {
	if len(services) == 0 {
		return nil
	}
	out := make([]doctorResult, 0, len(services))
	for _, svc := range services {
		id := fmt.Sprintf("local.agent-yaml.%s", svc.Name)
		title := fmt.Sprintf("agent.yaml for service %q is valid", svc.Name)
		manifestPath := filepath.Join(projectPath, svc.RelativePath, "agent.yaml")
		data, err := os.ReadFile(manifestPath) //nolint:gosec // G304: path constructed from azd project root
		if err != nil {
			if os.IsNotExist(err) {
				out = append(out, doctorResult{
					ID:     id,
					Title:  title,
					Status: doctorSkip,
					Detail: fmt.Sprintf("no agent.yaml at %s", manifestPath),
				})
				continue
			}
			out = append(out, doctorResult{
				ID:     id,
				Title:  title,
				Status: doctorFail,
				Detail: fmt.Sprintf("read failed: %s", err),
			})
			continue
		}
		if err := agent_yaml.ValidateAgentDefinition(data); err != nil {
			out = append(out, doctorResult{
				ID:     id,
				Title:  title,
				Status: doctorFail,
				Detail: fmt.Sprintf("schema validation failed: %s", err),
				Fix:    fmt.Sprintf("edit %s", manifestPath),
				Reason: "fix the agent.yaml schema errors above",
			})
			continue
		}
		out = append(out, doctorResult{
			ID:     id,
			Title:  title,
			Status: doctorOK,
			Detail: manifestPath,
		})
	}
	return out
}

// printDoctorReport renders the results to the writer. Format:
//
//	azd ai agent doctor
//	  ✓ PASS  azd CLI is installed and reachable
//	  ✓ PASS  Project loaded from azure.yaml
//	          /home/me/agent-project
//	  ✗ FAIL  AZURE_AI_PROJECT_ENDPOINT is set
//	          value missing — agent cannot reach Foundry
//
//	Next:
//	  azd provision   -- provision Azure resources
//
// The "Next:" tail is built from each non-passing result's Fix command,
// reusing the nextstep formatter for visual consistency. When every
// check passes, the Next: block falls back to the post-init resolver so
// the user always sees the next logical action (run/invoke/deploy).
//
// Redaction applies to non-TTY stdout unless the user opted in with
// `--unredacted` — same rule as the JSON envelope. The text renderer
// uses the same predicate as `shouldRedactDoctorJSON` so interactive
// and piped output stay in lockstep.
func printDoctorReport(w io.Writer, results []doctorResult, state *nextstep.State, flags *doctorFlags) {
	redacted := shouldRedactDoctorJSON(flags)
	fmt.Fprintln(w, color.New(color.Bold).Sprint("azd ai agent doctor"))
	for _, r := range results {
		fmt.Fprintf(w, "  %s  %s\n", r.Status.badge(), r.Title)
		if r.Detail != "" {
			fmt.Fprintf(w, "          %s\n", color.HiBlackString(redactDoctorString(r.Detail, redacted)))
		}
	}

	suggestions := make([]nextstep.Suggestion, 0, len(results))
	for _, r := range results {
		if r.Fix == "" {
			continue
		}
		desc := r.Reason
		if desc == "" {
			desc = r.Title
		}
		suggestions = append(suggestions, nextstep.Suggestion{
			Command:     redactDoctorString(r.Fix, redacted),
			Description: desc,
		})
	}

	// All checks passed (or only had non-fixable warnings/skips):
	// fall back to the post-init guidance so the user always sees the
	// next logical action — run locally, invoke locally, or deploy.
	if len(suggestions) == 0 {
		serviceName := ""
		readmeHint := ""
		if state != nil {
			if primary := state.PrimaryAgent(); primary != nil {
				serviceName = primary.ServiceName
				if rel := strings.TrimSpace(primary.RelativePath); rel != "" {
					readmeHint = fmt.Sprintf(
						"See %s/README.md for a sample payload appropriate for this agent.",
						filepath.ToSlash(rel),
					)
				}
				if primary.IsDeployed {
					// Already deployed — suggest test + monitor + redeploy.
					name := primary.DeployedName
					if name == "" {
						name = primary.ServiceName
					}
					deployedSuggestions := []nextstep.Suggestion{
						{
							Command:     fmt.Sprintf("azd ai agent show %s", name),
							Description: "inspect agent status, version, and metadata",
						},
						{
							Command:     "azd ai agent invoke <payload>",
							Description: "test the deployed agent end-to-end",
						},
					}
					if state.HasProjectEndpoint {
						deployedSuggestions = append(deployedSuggestions, nextstep.Suggestion{
							Command:     "azd ai agent monitor --follow",
							Description: "stream live invocation logs",
						})
					}
					nextstep.PrintNextWithHint(w, deployedSuggestions, readmeHint)
					return
				}
			}
		}
		nextstep.PrintNextWithHint(w, nextstep.ResolveAfterInit(state, serviceName), readmeHint)
		return
	}

	nextstep.PrintNext(w, suggestions)
}
