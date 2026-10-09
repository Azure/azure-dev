// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"azure.ai.rle/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type rlePublishFlags struct {
	dockerfile          string
	versionBump         string
	limeRouting         string
	limeProjectEndpoint string
	routingSet          bool
	endpointSet         bool
}

type publishAction struct {
	cmd   *cobra.Command
	flags *rlePublishFlags
}

var buildPublishImage = project.BuildRuntimeImage
var pushPublishImage = project.PushImage

func newPublishCommand() *cobra.Command {
	flags := &rlePublishFlags{}
	flags.versionBump = "major"

	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Build, push, and create or update the RLE environment",
		Long: "Build and push an RLE image, then create or update its environment. " +
			"Lime routing is optional and is requested only for this publish; omitting it preserves legacy behavior. " +
			"The extension is preview-gated. Production public API mapping depends on Task 5717034.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&publishAction{cmd: cmd, flags: flags}).Run()
		},
	}

	cmd.Flags().StringVar(&flags.dockerfile, "dockerfile", "",
		"Dockerfile path relative to the current folder. Defaults to Dockerfile at the source root or server/Dockerfile.")
	cmd.Flags().StringVar(
		&flags.versionBump,
		"version-bump",
		flags.versionBump,
		"Version bump to apply when creating or updating the environment: major, minor, or patch.",
	)
	cmd.Flags().StringVar(&flags.limeRouting, "lime-routing", "",
		"Lime routing request: legacy, disabled, same-project, or custom. Omitted and legacy preserve existing behavior.")
	cmd.Flags().StringVar(&flags.limeProjectEndpoint, "lime-project-endpoint", "",
		"HTTPS Foundry project endpoint for --lime-routing custom only.")
	return cmd
}

func (a *publishAction) Run() error {
	a.flags.routingSet = a.cmd.Flags().Changed("lime-routing")
	a.flags.endpointSet = a.cmd.Flags().Changed("lime-project-endpoint")
	versionBump, err := normalizeVersionBumpFlag(a.flags.versionBump)
	if err != nil {
		return err
	}
	if err := validateLimeRoutingFlags(a.flags); err != nil {
		return err
	}

	state, initialized, err := resolvePublishState()
	if err != nil {
		return err
	}
	if !initialized {
		if _, err := fmt.Fprintf(a.cmd.OutOrStdout(), "No %s found; using current folder as the RLE source.\n",
			rleStateFile); err != nil {
			return err
		}
	}

	if state.ProjectEndpoint == "" {
		return &azdext.LocalError{
			Message:  "Foundry project endpoint is required for publish.",
			Code:     "rle_project_required",
			Category: azdext.LocalErrorCategoryUser,
			Suggestion: fmt.Sprintf(
				"Set %s=https://<account>.services.ai.azure.com/api/projects/<project>.",
				foundryProjectEndpointEnvVar,
			),
		}
	}
	lime, err := publishLimeConfiguration(a.flags, state.ProjectEndpoint)
	if err != nil {
		return err
	}

	image, err := resolvePublishImage(state)
	if err != nil {
		return err
	}
	if !project.IsAcrImageReference(image) {
		return &azdext.LocalError{
			Message:    fmt.Sprintf("RLE publish image must be an ACR image reference, got %q.", image),
			Code:       "rle_acr_image_required",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Set AZURE_CONTAINER_REGISTRY_ENDPOINT=<registry>.azurecr.io, then run publish again.",
		}
	}
	if err := buildPublishImage(
		a.cmd.Context(),
		a.cmd.OutOrStdout(),
		a.cmd.ErrOrStderr(),
		image,
		project.BuildOptions{
			Source:     ".",
			Dockerfile: a.flags.dockerfile,
		},
	); err != nil {
		return err
	}
	if err := pushPublishImage(a.cmd.Context(), a.cmd.OutOrStdout(), a.cmd.ErrOrStderr(), image); err != nil {
		return err
	}
	client, err := createRleClient(state.ProjectEndpoint)
	if err != nil {
		return err
	}
	request := buildEnvironmentCreateRequest(state.EnvironmentName, image, versionBump)
	request.LimeConfiguration = lime

	var environment *environmentResource
	created := state.EnvironmentId == ""
	action := "Creating"
	if !created {
		action = "Updating"
	}

	if _, err := fmt.Fprintf(
		a.cmd.OutOrStdout(),
		"%s environment '%s' (image=%s) ...\n",
		action,
		state.EnvironmentName,
		image,
	); err != nil {
		return err
	}
	environment, err = client.createV1Environment(a.cmd.Context(), request)
	if err != nil {
		return serviceError(redactLimeEndpointError(err, a.flags.limeProjectEndpoint))
	}
	state.EnvironmentName = environment.Name
	state.EnvironmentId = environment.Id
	state.EnvironmentVersion = environment.Version
	if err := saveRleState(state); err != nil {
		return err
	}

	label := "Created"
	if !created {
		label = "Updated"
	}
	if _, err := fmt.Fprintf(
		a.cmd.OutOrStdout(),
		"\n%s environment '%s' (%s).\n",
		label,
		state.EnvironmentName,
		state.EnvironmentId,
	); err != nil {
		return err
	}
	body, err := json.MarshalIndent(environmentOutput{
		EnvironmentId:          environment.Id,
		EnvironmentVersion:     state.EnvironmentVersion,
		EnvironmentName:        environment.Name,
		FoundryProjectEndpoint: state.ProjectEndpoint,
		AcrImage:               environment.AcrImagePath,
		CreatedAt:              environment.CreatedAt,
		UpdatedAt:              environment.UpdatedAt,
	}, "", "  ")
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(a.cmd.OutOrStdout(), string(body)); err != nil {
		return err
	}
	if a.cmd.Flags().Changed("lime-routing") {
		if _, err := fmt.Fprintf(a.cmd.OutOrStdout(), "Lime routing requested: %s\n", a.flags.limeRouting); err != nil {
			return err
		}
	}
	return nil
}

func limeRoutingError(message string) error {
	return &azdext.LocalError{
		Message:  message,
		Code:     "rle_invalid_lime_routing",
		Category: azdext.LocalErrorCategoryUser,
		Suggestion: "Use --lime-routing legacy|disabled|same-project|custom; " +
			"provide --lime-project-endpoint only with custom.",
	}
}

func validateLimeRoutingFlags(flags *rlePublishFlags) error {
	if flags.routingSet && flags.limeRouting == "" {
		return limeRoutingError("--lime-routing cannot be empty.")
	}
	switch flags.limeRouting {
	case "", "legacy", "disabled", "same-project", "custom":
	default:
		return limeRoutingError("Invalid --lime-routing value.")
	}
	if flags.limeRouting == "" && !flags.endpointSet && flags.limeProjectEndpoint == "" {
		return nil
	}
	if flags.limeRouting == "" {
		return limeRoutingError("--lime-project-endpoint requires --lime-routing custom.")
	}
	if flags.limeRouting == "custom" && flags.limeProjectEndpoint == "" {
		return limeRoutingError("--lime-routing custom requires --lime-project-endpoint.")
	}
	if flags.limeRouting != "custom" && (flags.endpointSet || flags.limeProjectEndpoint != "") {
		return limeRoutingError("--lime-project-endpoint is only valid with --lime-routing custom.")
	}
	return nil
}

func publishLimeConfiguration(flags *rlePublishFlags, projectEndpoint string) (*limeConfiguration, error) {
	if err := validateLimeRoutingFlags(flags); err != nil {
		return nil, err
	}
	switch flags.limeRouting {
	case "", "legacy":
		return nil, nil
	case "disabled":
		return &limeConfiguration{Enabled: false}, nil
	case "same-project":
		return &limeConfiguration{Enabled: true, ProjectMode: "same_project"}, nil
	case "custom":
		if err := validateLimeProjectEndpoint(flags.limeProjectEndpoint, projectEndpoint); err != nil {
			return nil, err
		}
		return &limeConfiguration{
			Enabled: true, ProjectMode: "custom", ProjectEndpoint: flags.limeProjectEndpoint,
		}, nil
	default:
		return nil, limeRoutingError("Invalid --lime-routing value.")
	}
}

func validateLimeProjectEndpoint(raw string, projectEndpoint string) error {
	invalid := func() error {
		return limeRoutingError("--lime-project-endpoint must be an HTTPS Foundry project URL " +
			"with no credentials, query, fragment, port, or trailing slash.")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.ForceQuery ||
		u.Fragment != "" || strings.Contains(raw, "#") || u.Port() != "" ||
		u.Hostname() == "" || u.Opaque != "" || strings.HasSuffix(raw, "/") {
		return invalid()
	}
	parent, err := url.Parse(projectEndpoint)
	if err != nil {
		return invalid()
	}
	if strings.EqualFold(u.Hostname(), parent.Hostname()) && u.Path == parent.Path {
		return limeRoutingError("Use --lime-routing same-project when the Lime project is the publish project.")
	}
	// Anchor the allowed cloud domain to the already validated publish project, rather than assuming public Azure.
	parentHost := strings.ToLower(parent.Hostname())
	dot := strings.IndexByte(parentHost, '.')
	if dot < 0 {
		return invalid()
	}
	host := strings.ToLower(u.Hostname())
	prefix := strings.TrimSuffix(host, parentHost[dot:])
	if !strings.HasSuffix(host, parentHost[dot:]) ||
		prefix == "" || strings.Contains(prefix, ".") || strings.Contains(u.Host, ":") ||
		u.EscapedPath() != u.Path || !validLimeProjectPath(u.Path) {
		return invalid()
	}
	return nil
}

func validLimeProjectPath(path string) bool {
	parts := strings.Split(path, "/")
	if len(parts) != 4 || parts[0] != "" || parts[1] != "api" || parts[2] != "projects" {
		return false
	}
	name := parts[3]
	if name == "" || name == "." || name == ".." {
		return false
	}
	for _, char := range name {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9' || char == '-' || char == '_') {
			return false
		}
	}
	return true
}

var limeCredentialPattern = regexp.MustCompile(
	`(?i)\b(?:authorization|(?:access|refresh|id)[_-]?token|client[_-]?secret|` +
		`api[_-]?key|token|secret|password|sig)\b\s*[:=]\s*` +
		`(?:(?:bearer|basic)\s+)?(?:"[^"]*"|'[^']*'|[^\s,;}"']+)|` +
		`\b(?:bearer|basic)\s+(?:"[^"]*"|'[^']*'|[^\s,;}"']+)`,
)

func redactLimeEndpointError(err error, endpoint string) error {
	if endpoint == "" {
		return err
	}
	redact := func(text string) string {
		return limeCredentialPattern.ReplaceAllString(
			strings.ReplaceAll(text, endpoint, "[redacted Lime endpoint]"),
			"[redacted credential]",
		)
	}
	if httpErr, ok := errors.AsType[*rleHTTPError](err); ok {
		details := rleErrorBody{Code: redact(httpErr.code()), Message: redact(httpErr.message())}
		body, marshalErr := json.Marshal(details)
		if marshalErr != nil {
			return marshalErr
		}
		return newRleHTTPError(httpErr.statusCode, body)
	}
	return errors.New(redact(err.Error()))
}

func normalizeVersionBumpFlag(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "major":
		return "Major", nil
	case "minor":
		return "Minor", nil
	case "patch":
		return "Patch", nil
	default:
		return "", &azdext.LocalError{
			Message:    fmt.Sprintf("Invalid version bump %q.", value),
			Code:       "rle_invalid_version_bump",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Use --version-bump major, --version-bump minor, or --version-bump patch.",
		}
	}
}

func buildEnvironmentCreateRequest(name string, image string, versionBump string) v1EnvironmentRequest {
	return v1EnvironmentRequest{
		Name:         name,
		AcrImagePath: image,
		VersionBump:  versionBump,
	}
}

func resolvePublishState() (rleState, bool, error) {
	state, err := loadRleState()
	initialized := err == nil
	if err != nil {
		if localErr, ok := errors.AsType[*azdext.LocalError](err); !ok ||
			localErr.Code != "rle_project_not_initialized" {
			return rleState{}, false, err
		}
		state = defaultRleState(defaultSourceName("."))
	}

	state.EnvironmentName = firstNonEmpty(state.EnvironmentName, defaultSourceName("."))

	projectEndpoint, err := resolveFoundryProjectEndpoint()
	if err != nil {
		return rleState{}, false, err
	}
	if projectEndpoint != "" {
		state.ProjectEndpoint = projectEndpoint
	}

	return state, initialized, nil
}

func resolvePublishImage(state rleState) (string, error) {
	registry := strings.Trim(strings.TrimSpace(os.Getenv("AZURE_CONTAINER_REGISTRY_ENDPOINT")), "/")
	if registry == "" {
		return "", &azdext.LocalError{
			Message:    "ACR registry is required for publish.",
			Code:       "rle_acr_registry_required",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Set AZURE_CONTAINER_REGISTRY_ENDPOINT=<registry>.azurecr.io, then run publish again.",
		}
	}
	projectName, err := projectRouteSegment(state)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"%s/%s-%s:latest",
		registry,
		project.Slug(projectName),
		project.Slug(state.EnvironmentName),
	), nil
}

type environmentOutput struct {
	EnvironmentId          string `json:"environmentId"`
	EnvironmentVersion     string `json:"environmentVersion"`
	EnvironmentName        string `json:"environmentName"`
	FoundryProjectEndpoint string `json:"foundryProjectEndpoint"`
	AcrImage               string `json:"acrImage"`
	CreatedAt              string `json:"createdAt"`
	UpdatedAt              string `json:"updatedAt"`
}
