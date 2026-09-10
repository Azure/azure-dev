// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"

	"azureaiskills/internal/exterrors"
	"azureaiskills/internal/foundry/envkey"
	"azureaiskills/internal/pkg/skill_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type deleteFlags struct {
	name            string
	force           bool
	noPrompt        bool
	output          string
	projectEndpoint string
}

type deleteAction struct{ flags *deleteFlags }

var clearSkillMarkersFunc = clearSkillMarkers

type skillDeleteClient interface {
	DeleteSkill(context.Context, string) (*skill_api.DeleteSkillResponse, error)
}

// deleteResult is the JSON shape printed when --output=json.
type deleteResult struct {
	Name      string `json:"name"`
	Deleted   bool   `json:"deleted"`
	Cancelled bool   `json:"cancelled,omitempty"`
}

func (a *deleteAction) Run(ctx context.Context) error {
	if err := validateSkillName(a.flags.name); err != nil {
		return err
	}

	if !a.flags.force {
		if a.flags.noPrompt {
			return exterrors.Validation(
				exterrors.CodeMissingForceFlag,
				fmt.Sprintf("deleting %q requires confirmation", a.flags.name),
				"pass --force to skip confirmation in non-interactive mode",
			)
		}
		confirmed, err := a.confirmDelete(ctx)
		if err != nil {
			return err
		}
		if !confirmed {
			return a.printResult(deleteResult{Name: a.flags.name, Cancelled: true})
		}
	}

	skillCtx, err := resolveSkillContext(ctx, a.flags.projectEndpoint)
	if err != nil {
		return err
	}
	if err := deleteSkillAndClearMarkers(ctx, skillCtx.client, a.flags.name, skillCtx.endpoint); err != nil {
		return err
	}
	return a.printResult(deleteResult{Name: a.flags.name, Deleted: true})
}

func deleteSkillAndClearMarkers(
	ctx context.Context,
	client skillDeleteClient,
	skillName string,
	projectEndpoint string,
) error {
	if _, err := client.DeleteSkill(ctx, skillName); err != nil {
		if isNotFound(err) {
			if clearErr := clearSkillMarkersFunc(ctx, skillName, projectEndpoint); clearErr != nil {
				log.Printf("skill deleted remotely; marker cleanup failed: %v", clearErr)
			}
			return nil
		}
		return exterrors.ServiceFromAzure(err, exterrors.OpDeleteSkill)
	}
	if err := clearSkillMarkersFunc(ctx, skillName, projectEndpoint); err != nil {
		log.Printf("skill deleted remotely; marker cleanup failed: %v", err)
	}
	return nil
}

func clearSkillMarkers(ctx context.Context, skillName, projectEndpoint string) error {
	client, err := azdext.NewAzdClient()
	if err != nil {
		return fmt.Errorf("create azd client for skill marker cleanup: %w", err)
	}
	defer client.Close()
	env, err := client.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		if isNoSkillAzdEnvironment(err) {
			log.Printf("skill marker cleanup skipped: no active azd environment: %v", err)
			return nil
		}
		return fmt.Errorf("read active azd environment for skill marker cleanup: %w", err)
	}
	projectKey := envkey.SkillProjectEndpoint(skillName)
	marker, err := client.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: env.Environment.GetName(), Key: projectKey,
	})
	if err != nil {
		return fmt.Errorf("read skill readiness marker %s: %w", projectKey, err)
	}
	if !sameSkillProjectEndpoint(marker.Value, projectEndpoint) {
		log.Printf("skill marker cleanup skipped: %s belongs to another project", projectKey)
		return nil
	}
	return clearSkillMarkerValues(skillName, func(key, value string) error {
		_, err := client.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: env.Environment.GetName(), Key: key, Value: "",
		})
		return err
	})
}

func isNoSkillAzdEnvironment(err error) bool {
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	if st.Code() == codes.NotFound {
		return true
	}
	message := strings.ToLower(st.Message())
	return strings.Contains(message, "default environment not found") ||
		strings.Contains(message, "no project exists")
}

func clearSkillMarkerValues(skillName string, setValue func(string, string) error) error {
	for _, key := range []string{envkey.SkillVersion(skillName), envkey.SkillProjectEndpoint(skillName)} {
		if err := setValue(key, ""); err != nil {
			return fmt.Errorf("clearing skill readiness marker %s: %w", key, err)
		}
	}
	return nil
}

func sameSkillProjectEndpoint(a, b string) bool {
	if strings.EqualFold(
		strings.TrimRight(strings.TrimSpace(a), "/"),
		strings.TrimRight(strings.TrimSpace(b), "/"),
	) {
		return true
	}
	aHost, aProject := skillProjectIdentity(a)
	bHost, bProject := skillProjectIdentity(b)
	return aHost != "" && aProject != "" &&
		strings.EqualFold(aHost, bHost) && strings.EqualFold(aProject, bProject)
}

// Keep project identity semantics aligned with azure.ai.agents foundry_dependencies.go.
func skillProjectIdentity(endpoint string) (string, string) {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || u.Hostname() == "" {
		return "", ""
	}
	const segment = "/projects/"
	index := strings.Index(strings.ToLower(u.Path), segment)
	if index < 0 {
		return "", ""
	}
	project := strings.Split(strings.Trim(u.Path[index+len(segment):], "/"), "/")[0]
	return u.Hostname(), project
}

func (a *deleteAction) confirmDelete(ctx context.Context) (bool, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return false, fmt.Errorf("create azd client for confirmation: %w", err)
	}
	defer azdClient.Close()

	defaultValue := false
	resp, err := azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
		Options: &azdext.ConfirmOptions{
			Message:      fmt.Sprintf("Delete skill %q?", a.flags.name),
			DefaultValue: &defaultValue,
		},
	})
	if err != nil {
		return false, err
	}
	if resp.Value == nil {
		return false, nil
	}
	return *resp.Value, nil
}

func (a *deleteAction) printResult(res deleteResult) error {
	if a.flags.output == outputJSON {
		return printJSON(res)
	}
	if res.Cancelled {
		fmt.Printf("Skill %q deletion cancelled.\n", res.Name)
	} else {
		fmt.Printf("Skill %q deleted.\n", res.Name)
	}
	return nil
}

func newDeleteCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &deleteFlags{}
	action := &deleteAction{flags: flags}

	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a Foundry skill.",
		Long: `Delete a skill from the resolved Foundry project.

By default the CLI prompts for confirmation. Pass --force to skip the prompt.
In --no-prompt mode (set globally), --force is required.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.name = args[0]
			flags.output = extCtx.OutputFormat
			flags.noPrompt = extCtx.NoPrompt
			flags.projectEndpoint, _ = cmd.Flags().GetString("project-endpoint")
			return action.Run(azdext.WithAccessToken(cmd.Context()))
		},
	}

	cmd.Flags().BoolVar(&flags.force, "force", false, "Skip the confirmation prompt")
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{outputJSON, outputTable}, Default: outputTable,
	})
	return cmd
}
