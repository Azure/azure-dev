// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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
	}
	return "?"
}

// doctorResult is one row in the doctor output table.
type doctorResult struct {
	Title  string
	Status doctorStatus
	Detail string
	Fix    string // optional follow-up command (rendered via nextstep)
	Reason string // optional human-friendly "why" caption for the fix; falls back to Title
}

// doctorAction implements `azd ai agent doctor`.
type doctorAction struct {
	azdClient *azdext.AzdClient
	out       io.Writer
}

func newDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose your Azure AI Agent project setup",
		Long: "Runs a series of lightweight health checks against the current azd project " +
			"and AI Agent configuration. Reports pass / warn / fail per check along with the " +
			"recommended follow-up command for any non-passing item.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			client, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer client.Close()
			a := &doctorAction{azdClient: client, out: os.Stdout}
			return a.run(ctx)
		},
	}
}

// run executes all checks and prints a summary plus follow-up suggestions.
func (a *doctorAction) run(ctx context.Context) error {
	results := a.runChecks(ctx)
	state, _ := nextstep.AssembleState(ctx, a.azdClient)
	printDoctorReport(a.out, results, state)
	return nil
}

// runChecks executes the lightweight diagnostic checks. The order is
// stable so output is deterministic — earlier checks gate later ones
// where it makes sense (e.g., environment must exist before reading
// AZURE_AI_PROJECT_ENDPOINT).
func (a *doctorAction) runChecks(ctx context.Context) []doctorResult {
	out := []doctorResult{}

	// 1. azd CLI present.
	out = append(out, doctorResult{
		Title:  "azd CLI is installed and reachable",
		Status: doctorOK,
		Detail: "extension running, gRPC channel established",
	})

	// 2. Project loaded.
	projectPath, projectStatus, projectDetail := a.checkProject(ctx)
	out = append(out, projectStatus)
	if projectStatus.Status == doctorFail {
		// No project — bail out of subsequent checks that depend on it.
		return out
	}

	// 3. Current environment selected.
	envName, envResult := a.checkEnvironment(ctx)
	out = append(out, envResult)

	// 4. Agent service detected in azure.yaml.
	agentServices, svcResult := a.checkAgentService(ctx)
	out = append(out, svcResult)

	// 5. AZURE_AI_PROJECT_ENDPOINT set.
	out = append(out, a.checkProjectEndpoint(ctx, envName))

	// 6. Local agent.yaml validity for each detected service.
	out = append(out, a.checkAgentManifest(projectPath, agentServices)...)

	_ = projectDetail // detail captured into status row already
	return out
}

func (a *doctorAction) checkProject(ctx context.Context) (string, doctorResult, string) {
	resp, err := a.azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil || resp == nil || resp.Project == nil {
		return "", doctorResult{
			Title:  "Project loaded from azure.yaml",
			Status: doctorFail,
			Detail: "no azure.yaml could be loaded from the working directory",
			Fix:    "azd ai agent init",
			Reason: "scaffold an agent project in the current directory",
		}, ""
	}
	return resp.Project.Path, doctorResult{
		Title:  "Project loaded from azure.yaml",
		Status: doctorOK,
		Detail: resp.Project.Path,
	}, resp.Project.Path
}

func (a *doctorAction) checkEnvironment(ctx context.Context) (string, doctorResult) {
	resp, err := a.azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil || resp == nil || resp.Environment == nil || resp.Environment.Name == "" {
		return "", doctorResult{
			Title:  "Current azd environment selected",
			Status: doctorFail,
			Detail: "no environment is set; provisioned values cannot be read",
			Fix:    "azd env select <name>",
			Reason: "select an existing environment, or run `azd env new <name>` to create one",
		}
	}
	return resp.Environment.Name, doctorResult{
		Title:  "Current azd environment selected",
		Status: doctorOK,
		Detail: resp.Environment.Name,
	}
}

func (a *doctorAction) checkAgentService(ctx context.Context) ([]*azdext.ServiceConfig, doctorResult) {
	resp, err := a.azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil || resp == nil || resp.Project == nil {
		return nil, doctorResult{
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
		Title:  "Agent service detected in azure.yaml",
		Status: doctorOK,
		Detail: fmt.Sprintf("%d service(s): %v", len(agents), names),
	}
}

func (a *doctorAction) checkProjectEndpoint(ctx context.Context, envName string) doctorResult {
	if envName == "" {
		return doctorResult{
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
		return doctorResult{
			Title:  "AZURE_AI_PROJECT_ENDPOINT is set",
			Status: doctorFail,
			Detail: "value missing from azd environment — agent cannot reach Foundry",
			Fix:    "azd provision",
			Reason: "deploy Azure resources to populate AZURE_AI_PROJECT_ENDPOINT",
		}
	}
	return doctorResult{
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
		title := fmt.Sprintf("agent.yaml for service %q is valid", svc.Name)
		manifestPath := filepath.Join(projectPath, svc.RelativePath, "agent.yaml")
		data, err := os.ReadFile(manifestPath) //nolint:gosec // G304: path constructed from azd project root
		if err != nil {
			if os.IsNotExist(err) {
				out = append(out, doctorResult{
					Title:  title,
					Status: doctorSkip,
					Detail: fmt.Sprintf("no agent.yaml at %s", manifestPath),
				})
				continue
			}
			out = append(out, doctorResult{
				Title:  title,
				Status: doctorFail,
				Detail: fmt.Sprintf("read failed: %s", err),
			})
			continue
		}
		if err := agent_yaml.ValidateAgentDefinition(data); err != nil {
			out = append(out, doctorResult{
				Title:  title,
				Status: doctorFail,
				Detail: fmt.Sprintf("schema validation failed: %s", err),
				Fix:    fmt.Sprintf("edit %s", manifestPath),
				Reason: "fix the agent.yaml schema errors above",
			})
			continue
		}
		out = append(out, doctorResult{
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
//	          /home/me/myproject
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
func printDoctorReport(w io.Writer, results []doctorResult, state *nextstep.State) {
	fmt.Fprintln(w, color.New(color.Bold).Sprint("azd ai agent doctor"))
	for _, r := range results {
		fmt.Fprintf(w, "  %s  %s\n", r.Status.badge(), r.Title)
		if r.Detail != "" {
			fmt.Fprintf(w, "          %s\n", color.HiBlackString(r.Detail))
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
			Command:     r.Fix,
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
							Command:     fmt.Sprintf("azd ai agent invoke %s <payload>", name),
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
