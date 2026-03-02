// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/resource"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/installer"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/update"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type updateFlags struct {
	channel            string
	autoUpdate         string
	checkIntervalHours int
	global             *internal.GlobalCommandOptions
}

func newUpdateFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *updateFlags {
	flags := &updateFlags{}
	flags.Bind(cmd.Flags(), global)
	return flags
}

func (f *updateFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.global = global

	local.StringVar(
		&f.channel,
		"channel",
		"",
		"Update channel: stable or daily.",
	)
	local.StringVar(
		&f.autoUpdate,
		"auto-update",
		"",
		"Enable or disable auto-update: on or off.",
	)
	local.IntVar(
		&f.checkIntervalHours,
		"check-interval-hours",
		0,
		"Override the update check interval in hours.",
	)
}

func newUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "update",
		Short:  "Updates azd to the latest version.",
		Hidden: true,
	}
}

type updateAction struct {
	flags               *updateFlags
	console             input.Console
	formatter           output.Formatter
	writer              io.Writer
	configManager       config.UserConfigManager
	commandRunner       exec.CommandRunner
	alphaFeatureManager *alpha.FeatureManager
}

func newUpdateAction(
	flags *updateFlags,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
	configManager config.UserConfigManager,
	commandRunner exec.CommandRunner,
	alphaFeatureManager *alpha.FeatureManager,
) actions.Action {
	return &updateAction{
		flags:               flags,
		console:             console,
		formatter:           formatter,
		writer:              writer,
		configManager:       configManager,
		commandRunner:       commandRunner,
		alphaFeatureManager: alphaFeatureManager,
	}
}

func (a *updateAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	// Auto-enable the alpha feature if not already enabled.
	// The user's intent is clear by running `azd update` directly.
	if !a.alphaFeatureManager.IsEnabled(update.FeatureUpdate) {
		userCfg, err := a.configManager.Load()
		if err != nil {
			userCfg = config.NewEmptyConfig()
		}

		if err := userCfg.Set(fmt.Sprintf("alpha.%s", update.FeatureUpdate), "on"); err != nil {
			return nil, fmt.Errorf("failed to enable update feature: %w", err)
		}

		if err := a.configManager.Save(userCfg); err != nil {
			return nil, fmt.Errorf("failed to save config: %w", err)
		}

		a.console.MessageUxItem(ctx, &ux.MessageTitle{
			Title: fmt.Sprintf("azd update is in alpha. "+
				"To turn off in the future, run `azd config unset alpha.%s`.\n",
				update.FeatureUpdate),
		})
	}

	// Track install method for telemetry
	installedBy := installer.InstalledBy()
	tracing.SetUsageAttributes(
		fields.UpdateInstallMethod.String(string(installedBy)),
	)

	userConfig, err := a.configManager.Load()
	if err != nil {
		userConfig = config.NewEmptyConfig()
	}

	// Determine current channel BEFORE persisting any flags
	currentCfg := update.LoadUpdateConfig(userConfig)
	switchingChannels := a.flags.channel != "" && update.Channel(a.flags.channel) != currentCfg.Channel

	// Persist non-channel config flags immediately (auto-update, check-interval)
	configChanged, err := a.persistNonChannelFlags(userConfig)
	if err != nil {
		return nil, err
	}

	// If switching channels, persist channel to a temporary config for the version check
	// but don't save to disk until after confirmation
	if switchingChannels {
		newChannel, err := update.ParseChannel(a.flags.channel)
		if err != nil {
			return nil, err
		}
		_ = update.SaveChannel(userConfig, newChannel)
		configChanged = true
	} else if a.flags.channel != "" {
		// Same channel explicitly set — just persist it
		if err := update.SaveChannel(userConfig, update.Channel(a.flags.channel)); err != nil {
			return nil, err
		}
		configChanged = true
	}

	cfg := update.LoadUpdateConfig(userConfig)

	// Track channel for telemetry
	tracing.SetUsageAttributes(
		fields.UpdateChannel.String(string(cfg.Channel)),
		fields.UpdateFromVersion.String(internal.VersionInfo().Version.String()),
	)

	mgr := update.NewManager(a.commandRunner)

	// Block update in CI/CD environments
	if resource.IsRunningOnCI() {
		tracing.SetUsageAttributes(fields.UpdateResult.String(update.CodeSkippedCI))
		return nil, &update.UpdateError{
			Code: update.CodeSkippedCI,
			Err: &internal.ErrorWithSuggestion{
				Err:        fmt.Errorf("azd update is not supported in CI/CD environments"),
				Suggestion: "Use your pipeline to install the desired version directly.",
			},
		}
	}

	// Check if the user is trying to switch to daily via a package manager
	if a.flags.channel == string(update.ChannelDaily) && update.IsPackageManagerInstall() {
		tracing.SetUsageAttributes(fields.UpdateResult.String(update.CodePackageManagerFailed))

		uninstallCmd := update.PackageManagerUninstallCmd(installedBy)
		return nil, &update.UpdateError{
			Code: update.CodePackageManagerFailed,
			Err: &internal.ErrorWithSuggestion{
				Err: fmt.Errorf("daily builds aren't available via %s", installedBy),
				Suggestion: fmt.Sprintf(
					"Uninstall first with: %s\nThen install daily with: "+
						"curl -fsSL https://aka.ms/install-azd.sh | bash -s -- --version daily",
					uninstallCmd),
			},
		}
	}

	// If only config flags were set (no channel change, no update needed), just confirm
	if a.onlyConfigFlagsSet() {
		if configChanged {
			if err := a.configManager.Save(userConfig); err != nil {
				return nil, fmt.Errorf("failed to save config: %w", err)
			}
		}
		tracing.SetUsageAttributes(fields.UpdateResult.String(update.CodeSuccess))
		return &actions.ActionResult{
			Message: &actions.ResultMessage{
				Header: "Update preferences saved.",
			},
		}, nil
	}

	// Check for updates (always fresh for manual invocation)
	a.console.ShowSpinner(ctx, "Checking for updates...", input.Step)
	versionInfo, err := mgr.CheckForUpdate(ctx, cfg, true)
	a.console.StopSpinner(ctx, "", input.StepDone)

	if err != nil {
		tracing.SetUsageAttributes(fields.UpdateResult.String(update.CodeVersionCheckFailed))
		return nil, &update.UpdateError{
			Code: update.CodeVersionCheckFailed, Err: err,
		}
	}

	// Track target version
	tracing.SetUsageAttributes(
		fields.UpdateToVersion.String(versionInfo.Version),
	)

	if !versionInfo.HasUpdate && !switchingChannels {
		currentVersion := internal.VersionInfo().Version.String()
		tracing.SetUsageAttributes(fields.UpdateResult.String(update.CodeAlreadyUpToDate))

		header := fmt.Sprintf("azd is up to date (version %s) on the %s channel.", currentVersion, cfg.Channel)
		if cfg.Channel == update.ChannelDaily {
			header += " To check for stable updates, run: azd update --channel stable"
		}

		return &actions.ActionResult{
			Message: &actions.ResultMessage{
				Header: header,
			},
		}, nil
	}

	// Confirm channel switch with version details
	if switchingChannels {
		currentVersion := internal.VersionInfo().Version.String()
		confirmMsg := fmt.Sprintf(
			"Switch from %s channel (%s) to %s channel (%s)?",
			currentCfg.Channel, currentVersion,
			cfg.Channel, versionInfo.Version,
		)

		confirm, err := a.console.Confirm(ctx, input.ConsoleOptions{
			Message:      confirmMsg,
			DefaultValue: true,
		})

		if err != nil || !confirm {
			tracing.SetUsageAttributes(fields.UpdateResult.String(update.CodeChannelSwitchDecline))
			a.console.Message(ctx, "Channel switch cancelled.")
			return nil, nil
		}
	}

	// Now persist all config changes (including channel) after confirmation
	if configChanged {
		if err := a.configManager.Save(userConfig); err != nil {
			return nil, fmt.Errorf("failed to save config: %w", err)
		}
	}

	// Perform the update
	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: fmt.Sprintf("Updating azd to %s (%s)", versionInfo.Version, cfg.Channel),
	})

	stdout := a.console.Handles().Stdout
	if err := mgr.Update(ctx, cfg, stdout); err != nil {
		// UpdateError already has the right code, just track it
		var updateErr *update.UpdateError
		if errors.As(err, &updateErr) {
			tracing.SetUsageAttributes(fields.UpdateResult.String(updateErr.Code))
		} else {
			tracing.SetUsageAttributes(fields.UpdateResult.String(update.CodeReplaceFailed))
		}
		return nil, err
	}

	tracing.SetUsageAttributes(fields.UpdateResult.String(update.CodeSuccess))

	// Clean up any staged binary now that a manual update succeeded
	update.CleanStagedUpdate()

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: fmt.Sprintf(
				"Successfully updated azd to version %s. Changes take effect on next invocation.",
				versionInfo.Version,
			),
		},
	}, nil
}

// persistNonChannelFlags saves auto-update and check-interval flags to config.
// Channel is handled separately to allow confirmation before persisting.
func (a *updateAction) persistNonChannelFlags(cfg config.Config) (bool, error) {
	changed := false

	if a.flags.autoUpdate != "" {
		enabled := a.flags.autoUpdate == "on"
		if a.flags.autoUpdate != "on" && a.flags.autoUpdate != "off" {
			return false, fmt.Errorf("invalid auto-update value %q, must be \"on\" or \"off\"", a.flags.autoUpdate)
		}
		if err := update.SaveAutoUpdate(cfg, enabled); err != nil {
			return false, err
		}
		changed = true
	}

	if a.flags.checkIntervalHours > 0 {
		if err := update.SaveCheckIntervalHours(cfg, a.flags.checkIntervalHours); err != nil {
			return false, err
		}
		changed = true
	}

	return changed, nil
}

// onlyConfigFlagsSet returns true if only config flags were provided (no channel that requires an update).
func (a *updateAction) onlyConfigFlagsSet() bool {
	return a.flags.channel == "" &&
		(a.flags.autoUpdate != "" || a.flags.checkIntervalHours > 0)
}
