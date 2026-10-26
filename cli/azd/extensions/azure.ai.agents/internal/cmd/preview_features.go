// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"azureaiagent/internal/featureflags"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gopkg.in/yaml.v3"
)

// Preview annotations declare which registered feature a command, flag, or
// help block belongs to. The root command enforces and hides them in one place.
const (
	// featureAnnotation on a command or flag names the feature it requires.
	// A command or a flag without featureValuesAnnotation is hidden when off.
	featureAnnotation = "azure.ai.agents/feature"
	// featureValuesAnnotation limits a flag requirement to these values.
	featureValuesAnnotation = "azure.ai.agents/feature-values"
	// featureUsageAnnotation holds a feature ID and the flag usage shown
	// while that feature is off.
	featureUsageAnnotation = "azure.ai.agents/feature-usage"
	// discoverAnnotation hides a command when the feature is off.
	// The command stays runnable.
	discoverAnnotation = "azure.ai.agents/discover"
	// serviceGateAnnotation classifies every agent service the command selects.
	serviceGateAnnotation = "azure.ai.agents/service-gate"
	// helpLongPrefix and helpExamplePrefix key help blocks by feature ID.
	helpLongPrefix    = "azure.ai.agents/help-long/"
	helpExamplePrefix = "azure.ai.agents/help-example/"
	helpTextPrefix    = "azure.ai.agents/help-text/"
	helpBlockSep      = "\x1f"
)

// previewFlag marks a flag as part of a preview feature. With values, only
// those values (case-insensitive, trimmed) need the feature and the flag stays
// visible. Without values, setting the flag needs the feature (a bool flag only
// when true) and the flag is hidden from help while the feature is off.
func previewFlag(cmd *cobra.Command, name string, feature featureflags.Feature, values ...string) {
	flag := cmd.Flags().Lookup(name)
	if flag == nil {
		panic(fmt.Sprintf("preview flag %q is not defined on %q", name, cmd.Name()))
	}
	if flag.Annotations == nil {
		flag.Annotations = map[string][]string{}
	}
	flag.Annotations[featureAnnotation] = []string{feature.ID}
	if len(values) > 0 {
		flag.Annotations[featureValuesAnnotation] = values
	}
}

// previewFlagUsage replaces a flag's usage text while the feature is off.
// The flag itself does not need to be a preview flag.
func previewFlagUsage(cmd *cobra.Command, name string, feature featureflags.Feature, offUsage string) {
	flag := cmd.Flags().Lookup(name)
	if flag == nil {
		panic(fmt.Sprintf("flag %q is not defined on %q", name, cmd.Name()))
	}
	if flag.Annotations == nil {
		flag.Annotations = map[string][]string{}
	}
	flag.Annotations[featureUsageAnnotation] = []string{feature.ID, offUsage}
}

// previewCommand makes a whole command require the feature.
func previewCommand(cmd *cobra.Command, feature featureflags.Feature) {
	setAnnotation(cmd, featureAnnotation, feature.ID)
}

// discoverCommand hides a command from help while the feature is off.
func discoverCommand(cmd *cobra.Command, feature featureflags.Feature) {
	setAnnotation(cmd, discoverAnnotation, feature.ID)
}

// gateAgentServices makes every agent service the command selects pass the
// project-layer classification check.
func gateAgentServices(cmd *cobra.Command) {
	setAnnotation(cmd, serviceGateAnnotation, "true")
}

// previewHelp removes Long and Example blocks while the feature is off.
func previewHelp(cmd *cobra.Command, feature featureflags.Feature, long []string, examples []string) {
	for _, block := range long {
		if !strings.Contains(cmd.Long, block) {
			panic(fmt.Sprintf("help block for %q is not in %q Long text", feature.ID, cmd.Name()))
		}
	}
	for _, block := range examples {
		if !strings.Contains(cmd.Example, block) {
			panic(fmt.Sprintf("example block for %q is not in %q examples", feature.ID, cmd.Name()))
		}
	}
	if len(long) > 0 {
		setAnnotation(cmd, helpLongPrefix+feature.ID, strings.Join(long, helpBlockSep))
	}
	if len(examples) > 0 {
		setAnnotation(cmd, helpExamplePrefix+feature.ID, strings.Join(examples, helpBlockSep))
	}
}

// previewHelpText replaces existing Short/Long/Example text while off.
func previewHelpText(cmd *cobra.Command, feature featureflags.Feature, onText, offText string) {
	if !strings.Contains(cmd.Short, onText) && !strings.Contains(cmd.Long, onText) &&
		!strings.Contains(cmd.Example, onText) {
		panic(fmt.Sprintf("help text for %q is not in %q", feature.ID, cmd.Name()))
	}
	key := helpTextPrefix + feature.ID
	replacements := onText + helpBlockSep + offText
	if existing := cmd.Annotations[key]; existing != "" {
		replacements = existing + helpBlockSep + replacements
	}
	setAnnotation(cmd, key, replacements)
}

func setAnnotation(cmd *cobra.Command, key, value string) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[key] = value
}

func registeredFeature(id string) featureflags.Feature {
	feature, ok := featureflags.Lookup(id)
	if !ok {
		panic(fmt.Sprintf("preview feature %q is not registered", id))
	}
	return feature
}

// requiredPreviewFeatures returns the features the parsed command line needs.
func requiredPreviewFeatures(cmd *cobra.Command) []featureflags.Feature {
	var features []featureflags.Feature
	add := func(id string) {
		feature := registeredFeature(id)
		if !slices.Contains(features, feature) {
			features = append(features, feature)
		}
	}
	for command := cmd; command != nil; command = command.Parent() {
		if id := command.Annotations[featureAnnotation]; id != "" {
			add(id)
		}
	}
	cmd.Flags().Visit(func(flag *pflag.Flag) {
		ids := flag.Annotations[featureAnnotation]
		if len(ids) == 0 || !flagNeedsFeature(flag) {
			return
		}
		add(ids[0])
	})
	return features
}

func flagNeedsFeature(flag *pflag.Flag) bool {
	value := strings.TrimSpace(flag.Value.String())
	if values := flag.Annotations[featureValuesAnnotation]; len(values) > 0 {
		if slice, ok := flag.Value.(pflag.SliceValue); ok {
			return slices.ContainsFunc(slice.GetSlice(), func(value string) bool {
				return slices.ContainsFunc(values, func(candidate string) bool {
					return strings.EqualFold(strings.TrimSpace(value), candidate)
				})
			})
		}
		return slices.ContainsFunc(values, func(candidate string) bool {
			return strings.EqualFold(value, candidate)
		})
	}
	if flag.Value.Type() == "bool" {
		return value == "true"
	}
	return true
}

type serviceGateKey struct{}

var previewValidators sync.Map

// validateBeforePreview registers a pure local check to run only when the gate would reject.
func validateBeforePreview(cmd *cobra.Command, fn func(*cobra.Command, []string) error) {
	previewValidators.Store(cmd, fn)
}

// enforcePreviewFeatures marks service-gated commands and rejects
// disabled preview inputs. Local errors take precedence on rejection.
func enforcePreviewFeatures(cmd *cobra.Command, args []string) error {
	if cmd.Annotations[serviceGateAnnotation] != "" {
		cmd.SetContext(context.WithValue(cmd.Context(), serviceGateKey{}, true))
	}

	features := requiredPreviewFeatures(cmd)
	if len(features) == 0 {
		return nil
	}
	reject := func(gateErr error) error {
		if validator, ok := previewValidators.Load(cmd); ok {
			if err := validator.(func(*cobra.Command, []string) error)(cmd, args); err != nil {
				return err
			}
		}
		return gateErr
	}
	for _, feature := range features {
		if err := featureflags.Require(feature); err != nil {
			return reject(err)
		}
	}
	return nil
}

// requireSelectedServiceFeatures classifies a selected agent service when the
// running command is annotated with gateAgentServices.
func requireSelectedServiceFeatures(
	ctx context.Context, service *azdext.ServiceConfig, projectRoot string,
) error {
	if gated, _ := ctx.Value(serviceGateKey{}).(bool); !gated {
		return nil
	}
	if err := project.RequireAgentFeatures(service, projectRoot); err != nil {
		return &serviceGateError{err: err}
	}
	return nil
}

// serviceGateError marks a service gate failure so callers that recover from
// selection errors return it unchanged instead of falling back.
type serviceGateError struct{ err error }

func (e *serviceGateError) Error() string { return e.err.Error() }
func (e *serviceGateError) Unwrap() error { return e.err }

func isServiceGateError(err error) bool {
	_, ok := errors.AsType[*serviceGateError](err)
	return ok
}

// previewHelpStates reads the registered process switches for help.
func previewHelpStates() map[string]bool {
	states := map[string]bool{}
	for _, feature := range featureflags.All() {
		enabled, err := featureflags.IsEnabled(feature)
		if err != nil {
			panic(err)
		}
		states[feature.ID] = enabled
	}
	return states
}

// applyPreviewVisibility hides disabled preview commands, flags, and help
// blocks across the tree. Static visibility is kept for enabled features.
func applyPreviewVisibility(root *cobra.Command, states map[string]bool) {
	off := func(id string) bool { return id != "" && !states[registeredFeature(id).ID] }
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		if off(cmd.Annotations[featureAnnotation]) || off(cmd.Annotations[discoverAnnotation]) {
			cmd.Hidden = true
		}
		for key, value := range cmd.Annotations {
			switch {
			case strings.HasPrefix(key, helpTextPrefix) && off(strings.TrimPrefix(key, helpTextPrefix)):
				replacements := strings.Split(value, helpBlockSep)
				if len(replacements)%2 != 0 {
					panic(fmt.Sprintf("invalid preview help replacement on %q", cmd.Name()))
				}
				for i := 0; i < len(replacements); i += 2 {
					onText, offText := replacements[i], replacements[i+1]
					cmd.Short = strings.ReplaceAll(cmd.Short, onText, offText)
					cmd.Long = strings.ReplaceAll(cmd.Long, onText, offText)
					cmd.Example = strings.ReplaceAll(cmd.Example, onText, offText)
				}
			case strings.HasPrefix(key, helpLongPrefix) && off(strings.TrimPrefix(key, helpLongPrefix)):
				cmd.Long = removeHelpBlocks(cmd.Long, value)
			case strings.HasPrefix(key, helpExamplePrefix) && off(strings.TrimPrefix(key, helpExamplePrefix)):
				cmd.Example = removeHelpBlocks(cmd.Example, value)
			}
		}
		cmd.LocalFlags().VisitAll(func(flag *pflag.Flag) {
			if usage := flag.Annotations[featureUsageAnnotation]; len(usage) == 2 && off(usage[0]) {
				flag.Usage = usage[1]
			}
			// Init's protocol list depends on two independent previews. Keep its
			// Voice annotations and full static metadata; render the combined list here.
			if cmd.Name() == "init" && flag.Name == "protocol" {
				flag.Usage = "Protocols supported by the agent (" +
					strings.Join(protocolNames(states[featureflags.Voice.ID], states[featureflags.A2A.ID]), ", ") +
					"). Can be specified multiple times."
			}
			ids := flag.Annotations[featureAnnotation]
			if len(ids) == 0 || !off(ids[0]) {
				return
			}
			if len(flag.Annotations[featureValuesAnnotation]) == 0 {
				flag.Hidden = true
			}
		})
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(root)
}

func removeHelpBlocks(text, joined string) string {
	for block := range strings.SplitSeq(joined, helpBlockSep) {
		text = strings.Replace(text, block, "", 1)
	}
	return text
}

func requireProjectPreviewFeatures(config *azdext.ProjectConfig) error {
	for _, service := range config.GetServices() {
		if err := project.RequireAgentFeatures(service, config.GetPath()); err != nil {
			return err
		}
	}
	return nil
}

func requireManifestPreviewFeatures(
	content []byte, projectRoot string,
) error {
	var document struct {
		Services map[string]*agentServiceAddRawService `yaml:"services"`
	}
	if err := yaml.Unmarshal(content, &document); err != nil {
		return fmt.Errorf("reading preview features from azure.yaml: %w", err)
	}
	services, err := agentServiceAddDependencyServices(document.Services)
	if err != nil {
		return err
	}
	return requireProjectPreviewFeatures(&azdext.ProjectConfig{Services: services, Path: projectRoot})
}

func requireStagedPreviewFeatures(root string) error {
	content, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
	if err != nil {
		return err
	}
	return requireManifestPreviewFeatures(content, root)
}

// selectLocalService selects a service for local preview checks.
// Without an explicit selector, projects with no agents are skipped.
func (a *InvokeAction) selectLocalService(ctx context.Context) error {
	client, err := azdext.NewAzdClient()
	if err != nil {
		return err
	}
	defer client.Close()
	response, err := client.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		st, _ := status.FromError(err)
		if st.Code() == codes.Unknown && st.Message() == azdcontext.ErrNoProject.Error() &&
			len(st.Proto().Details) == 0 {
			return nil
		}
		return err
	}
	if response.GetProject() == nil {
		return fmt.Errorf("loading project for local preview classification: missing project response")
	}
	hasAgent := false
	for _, service := range response.Project.GetServices() {
		if service.GetHost() == AiAgentHost {
			hasAgent = true
			break
		}
	}
	if !hasAgent && a.serviceNameSelector() == "" {
		return nil
	}
	service, _, err := resolveAgentService(ctx, client, a.serviceNameSelector(), a.noPrompt)
	if err != nil {
		return err
	}
	a.protocolServiceName = service.GetName()
	return nil
}
