// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/runcontext/agentdetect"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/resource"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/update"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// extractFlagsWithValues extracts flags that take values from a cobra command.
// This ensures we have a single source of truth for flag definitions by
// dynamically inspecting the command's flag definitions instead of
// maintaining a separate hardcoded list.
//
// The function inspects both regular flags and persistent flags, checking
// the flag's value type to determine if it takes an argument:
// - Bool flags don't take values
// - String, Int, StringSlice, etc. flags do take values
func extractFlagsWithValues(cmd *cobra.Command) map[string]bool {
	flagsWithValues := make(map[string]bool)

	// Extract flags that take values from the command
	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		// String, StringSlice, StringArray, Int, Int64, etc. all take values
		// Bool flags don't take values
		if flag.Value.Type() != "bool" {
			flagsWithValues["--"+flag.Name] = true
			if flag.Shorthand != "" {
				flagsWithValues["-"+flag.Shorthand] = true
			}
		}
	})

	// Also check persistent flags (global flags)
	// IMPORTANT: cmd.Flags().VisitAll() does NOT include persistent flags.
	// In Cobra, cmd.Flags() only returns local flags specific to that command,
	// while cmd.PersistentFlags() returns flags that are inherited by subcommands.
	// These are separate flag sets, so we must call both VisitAll functions
	// to capture all flags that can take values.
	cmd.PersistentFlags().VisitAll(func(flag *pflag.Flag) {
		if flag.Value.Type() != "bool" {
			flagsWithValues["--"+flag.Name] = true
			if flag.Shorthand != "" {
				flagsWithValues["-"+flag.Shorthand] = true
			}
		}
	})

	return flagsWithValues
}

// findFirstNonFlagArg finds the first argument that doesn't start with '-' and isn't a flag value.
// This function properly handles flags that take values (like --output json) to avoid
// incorrectly identifying flag values as commands.
// Returns the command and any unknown flags encountered before the command.
func findFirstNonFlagArg(args []string, flagsWithValues map[string]bool) (command string, unknownFlags []string) {
	// Initialize as empty slice instead of nil for consistent behavior
	unknownFlags = []string{}

	skipNext := false
	for i, arg := range args {
		// Skip this argument if it's marked as a flag value from previous iteration
		if skipNext {
			skipNext = false
			continue
		}

		// If it doesn't start with '-', it's a potential command
		if !strings.HasPrefix(arg, "-") {
			return arg, unknownFlags
		}

		// Check if this is a known flag that takes a value
		if flagsWithValues[arg] {
			// This flag takes a value, so skip the next argument
			skipNext = true
			continue
		}

		// Handle flags with '=' syntax like --output=json
		if strings.Contains(arg, "=") {
			parts := strings.SplitN(arg, "=", 2)
			if flagsWithValues[parts[0]] {
				// This is a known flag=value format, no need to skip next
				continue
			}
			// Unknown flag with equals - record it
			unknownFlags = append(unknownFlags, parts[0])
			continue
		}

		// This is an unknown flag - record it
		unknownFlags = append(unknownFlags, arg)

		// Conservative heuristic: if the next argument doesn't start with '-'
		// and there are more args after it, assume the unknown flag takes a value
		if i+1 < len(args) && i+2 < len(args) {
			nextArg := args[i+1]
			argAfterNext := args[i+2]
			if !strings.HasPrefix(nextArg, "-") && !strings.HasPrefix(argAfterNext, "-") {
				// Pattern: --unknown value command
				// Skip the value, let command be found next
				skipNext = true
			}
		}
	}

	return "", unknownFlags
}

// checkForMatchingExtensions checks for extensions that match any possible namespace
// from the command arguments. For example, "azd foo demo bar" will check for
// extensions with namespaces: "foo", "foo.demo", "foo.demo.bar"
func checkForMatchingExtensions(
	ctx context.Context, extensionManager *extensions.Manager, args []string) ([]*extensions.ExtensionMetadata, error) {
	if len(args) == 0 {
		return nil, nil
	}

	options := &extensions.FilterOptions{}
	registryExtensions, err := extensionManager.FindExtensions(ctx, options)
	if err != nil {
		return nil, err
	}

	var matchingExtensions []*extensions.ExtensionMetadata

	// Generate all possible namespace combinations from the command arguments
	// For "azd something demo foo" -> check "something", "something.demo", "something.demo.foo"
	for i := 1; i <= len(args); i++ {
		candidateNamespace := strings.Join(args[:i], ".")

		// Check if any extension has this exact namespace
		for _, ext := range registryExtensions {
			if ext.Namespace == candidateNamespace {
				matchingExtensions = append(matchingExtensions, ext)
			}
		}
	}

	return matchingExtensions, nil
}

// promptForExtensionChoice prompts the user to choose from multiple matching extensions
func promptForExtensionChoice(
	ctx context.Context,
	console input.Console,
	matches []*extensions.ExtensionMetadata) (*extensions.ExtensionMetadata, error) {

	if len(matches) == 0 {
		return nil, fmt.Errorf("no extensions to choose from")
	}

	if len(matches) == 1 {
		return matches[0], nil
	}

	// Under --no-prompt there is no basis for choosing between the matches, and guessing could
	// install a different binary than the user expects. `azd extension install` refuses to pick a
	// source for the same reason.
	if console.IsNoPromptMode() {
		choices := make([]string, 0, len(matches))
		for _, ext := range matches {
			choices = append(choices, fmt.Sprintf("%s (%s)", ext.Id, ext.Source))
		}
		slices.Sort(choices)

		return nil, &internal.ErrorWithSuggestion{
			Err: fmt.Errorf("more than one extension can be installed: %s", strings.Join(choices, ", ")),
			Suggestion: "Run 'azd extension install <id> --source <source>' to select one, " +
				"then run this command again.",
		}
	}

	options := make([]string, len(matches))
	for i, ext := range matches {
		options[i] = fmt.Sprintf("%s (%s) - %s", ext.DisplayName, ext.Source, ext.Description)
	}

	choice, err := console.Select(ctx, input.ConsoleOptions{
		Message: "Which extension would you like to install?",
		Options: options,
	})
	if err != nil {
		return nil, err
	}

	return matches[choice], nil
}

// chooseLogicalExtensionCandidates separates the rare choice between different extension IDs from
// source selection. All source candidates for the selected logical extension are preserved so the
// auto-install UX can present them after discovery is complete.
func chooseLogicalExtensionCandidates(
	ctx context.Context,
	console input.Console,
	matches []*extensions.ExtensionMetadata,
) ([]*extensions.ExtensionMetadata, error) {
	if len(matches) == 0 {
		return nil, fmt.Errorf("no extensions to choose from")
	}

	grouped := map[string][]*extensions.ExtensionMetadata{}
	for _, match := range matches {
		id := strings.ToLower(match.Id)
		grouped[id] = append(grouped[id], match)
	}
	for id := range maps.Keys(grouped) {
		slices.SortFunc(grouped[id], func(a, b *extensions.ExtensionMetadata) int {
			return strings.Compare(strings.ToLower(a.Source), strings.ToLower(b.Source))
		})
	}

	ids := slices.Sorted(maps.Keys(grouped))
	if len(ids) == 1 {
		return grouped[ids[0]], nil
	}

	representatives := make([]*extensions.ExtensionMetadata, 0, len(ids))
	for _, id := range ids {
		representatives = append(representatives, grouped[id][0])
	}
	chosen, err := promptForExtensionChoice(ctx, console, representatives)
	if err != nil {
		return nil, err
	}
	return grouped[strings.ToLower(chosen.Id)], nil
}

func requirementCandidates(requirement projectExtensionRequirement) []*extensions.ExtensionMetadata {
	if len(requirement.candidates) > 0 {
		return requirement.candidates
	}
	if requirement.extension == nil {
		return nil
	}
	return []*extensions.ExtensionMetadata{requirement.extension}
}

// isBuiltInCommand checks if the given command is a built-in command by examining
// the root command's command tree. This includes both core azd commands and any
// installed extensions, preventing auto-install from triggering for known commands.
func isBuiltInCommand(rootCmd *cobra.Command, commandName string) bool {
	if commandName == "" {
		return false
	}

	// Check if the command exists in the root command's subcommands
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == commandName {
			return true
		}
		// Also check aliases
		if slices.Contains(cmd.Aliases, commandName) {
			return true
		}
	}

	return false
}

// hasSubcommand checks if a command has a subcommand with the given name or alias.
func hasSubcommand(cmd *cobra.Command, name string) bool {
	for _, sub := range cmd.Commands() {
		if sub.Name() == name {
			return true
		}
		if slices.Contains(sub.Aliases, name) {
			return true
		}
	}
	return false
}

// getCommandPath returns the command path from root to the given command (excluding root).
// For example, if foundCmd is "agent" under "ai", returns ["ai", "agent"].
func getCommandPath(cmd *cobra.Command) []string {
	var path []string
	for c := cmd; c != nil && c.Parent() != nil; c = c.Parent() {
		path = append([]string{c.Name()}, path...)
	}
	return path
}

// buildNamespaceArgs builds the full namespace argument list by combining
// the found command's path with remaining non-flag arguments.
func buildNamespaceArgs(foundCmd *cobra.Command, remainingArgs []string) []string {
	args := getCommandPath(foundCmd)

	for _, arg := range remainingArgs {
		if !strings.HasPrefix(arg, "-") {
			args = append(args, arg)
		}
	}

	return args
}

// tryAutoInstallForPartialNamespace checks if the found command is a partial namespace match
// and prompts for extension installation if an uninstalled extension matches.
// Returns true if an extension was installed, false otherwise.
//
// This handles the scenario where an extension like "ai.foo" is installed (creating the "ai"
// command group), but the user runs "azd ai bar init" where the "ai.bar" extension is not installed.
// Without this check, Cobra would find the "ai" command and show its help instead of prompting to install
// the "ai.bar" extension.
func tryAutoInstallForPartialNamespace(
	ctx context.Context,
	rootContainer *ioc.NestedContainer,
	foundCmd *cobra.Command,
	remainingArgs []string,
) (autoInstallResult, error) {
	if _, isExtensionCmd := foundCmd.Annotations["extension.id"]; isExtensionCmd {
		// Extension commands handle their own args via DisableFlagParsing
		return autoInstallResult{}, nil
	}

	var firstRemainingArg string
	for _, arg := range remainingArgs {
		if !strings.HasPrefix(arg, "-") {
			firstRemainingArg = arg
			break
		}
	}

	if firstRemainingArg == "" || hasSubcommand(foundCmd, firstRemainingArg) {
		return autoInstallResult{}, nil
	}

	argsForMatching := buildNamespaceArgs(foundCmd, remainingArgs)
	if len(argsForMatching) == 0 {
		return autoInstallResult{}, nil
	}

	var extensionManager *extensions.Manager
	var console input.Console
	if err := rootContainer.Resolve(&extensionManager); err != nil {
		log.Printf("failed to resolve extension manager: %v", err)
		return autoInstallResult{}, nil
	}
	if err := rootContainer.Resolve(&console); err != nil {
		log.Printf("failed to resolve console: %v", err)
		return autoInstallResult{}, nil
	}

	extensionMatches, err := checkForMatchingExtensions(ctx, extensionManager, argsForMatching)
	if err != nil {
		log.Printf("failed to check for matching extensions: %v", err)
		return autoInstallResult{}, nil
	}
	if len(extensionMatches) == 0 {
		return autoInstallResult{}, nil
	}

	return autoInstallCommandMatches(
		ctx,
		console,
		extensionManager,
		extensionMatches,
		fmt.Sprintf(
			"Command '%s' isn't available. Install the required extension to use this command.",
			strings.Join(argsForMatching, " "),
		),
	)
}

type extensionAutoInstallManager interface {
	FindExtensions(ctx context.Context, options *extensions.FilterOptions) ([]*extensions.ExtensionMetadata, error)
	GetInstalled(options extensions.FilterOptions) (*extensions.Extension, error)
	Install(
		ctx context.Context,
		extension *extensions.ExtensionMetadata,
		versionPreference string,
	) (*extensions.ExtensionVersion, error)
	ListInstalled() (map[string]*extensions.Extension, error)
}

func tryAutoInstallExtensionVersion(
	ctx context.Context,
	console input.Console,
	extensionManager extensionAutoInstallManager,
	extension extensions.ExtensionMetadata,
	versionPreference string,
) (bool, error) {
	// Check if the extension is already installed
	installedExtension, err := extensionManager.GetInstalled(extensions.FilterOptions{
		Id: extension.Id,
	})
	if err == nil {
		if err := validateInstalledExtensionVersion(installedExtension, versionPreference); err != nil {
			return false, err
		}
		return false, nil
	}

	installedBefore, err := extensionManager.ListInstalled()
	if err != nil {
		return false, fmt.Errorf("listing installed extensions: %w", err)
	}
	preInstalledIds := make(map[string]struct{}, len(installedBefore))
	for id := range installedBefore {
		preInstalledIds[id] = struct{}{}
	}

	stepMessage := fmt.Sprintf(
		"Installing %s extension",
		output.WithHighLightFormat(extension.Id),
	)
	console.ShowSpinner(ctx, stepMessage, input.Step)
	installedVersion, err := extensionManager.Install(ctx, &extension, versionPreference)
	if err != nil {
		console.StopSpinner(ctx, stepMessage, input.StepFailed)
		return false, fmt.Errorf("failed to install extension: %w", err)
	}

	stepMessage += output.WithGrayFormat(" (%s)", installedVersion.Version)
	console.StopSpinner(ctx, stepMessage, input.StepDone)
	if len(installedVersion.Dependencies) > 0 {
		displayInstalledDependencies(
			ctx,
			console,
			extensionManager,
			installedVersion.Dependencies,
			preInstalledIds,
			"  ",
			map[string]struct{}{extension.Id: {}},
		)
	}
	return true, nil
}

// startUpdateCheck launches a background goroutine that checks for a newer
// version of azd and returns a channel that will receive the result.
// The caller should read from the returned channel after command execution.
func startUpdateCheck(ctx context.Context) <-chan *update.VersionInfo {
	ch := make(chan *update.VersionInfo, 1)

	// Allow the user to skip the update check by setting AZD_SKIP_UPDATE_CHECK.
	if value, has := os.LookupEnv("AZD_SKIP_UPDATE_CHECK"); has {
		if setting, err := strconv.ParseBool(value); err == nil && setting {
			log.Print("skipping update check since AZD_SKIP_UPDATE_CHECK is true")
			close(ch)
			return ch
		} else if err != nil {
			log.Printf("could not parse value for AZD_SKIP_UPDATE_CHECK as a boolean "+
				"(it was: %s), proceeding with update check", value)
		}
	}

	bgCtx, bgCancel := context.WithTimeout(ctx, 60*time.Second)

	go func() {
		defer close(ch)
		defer bgCancel()

		configMgr := config.NewUserConfigManager(config.NewFileConfigManager(config.NewManager()))
		userConfig, err := configMgr.Load()
		if err != nil {
			userConfig = config.NewEmptyConfig()
		}

		cfg := update.LoadUpdateConfig(userConfig)

		mgr := update.NewManager(nil, nil)
		versionInfo, err := mgr.CheckForUpdate(bgCtx, cfg, false)
		if err != nil {
			log.Printf("failed to check for updates: %v, skipping update check", err)
			return
		}

		ch <- versionInfo
	}()

	return ch
}

// ExecuteResult holds the outcome of ExecuteWithAutoInstall, including
// metadata about the executed command that callers may need for post-execution
// decisions (e.g., whether to wait for the background update check).
type ExecuteResult struct {
	// Err is the error returned by the command execution, if any.
	Err error
	// IsLightspeed is true when the executed command was marked as Lightspeed.
	// Callers should skip non-essential post-execution work (update checks, banners)
	// for lightspeed commands so the process can exit quickly.
	IsLightspeed bool
	// LatestVersion receives the result of the background update check.
	// Nil when the update check was not started (lightspeed commands).
	// When the check is skipped via AZD_SKIP_UPDATE_CHECK, the returned channel
	// is closed without a value.
	LatestVersion <-chan *update.VersionInfo
}

// projectDirExists reports whether the directory azd will run in already exists. An empty cwd means
// the caller's own directory. A --cwd that PersistentPreRunE still has to create holds no project.
func projectDirExists(cwd string) bool {
	if cwd == "" {
		return true
	}

	_, err := os.Stat(cwd)
	return err == nil
}

// newRootCmdForExecution builds the root command, constructing it from --cwd when one was supplied
// so that cached AzdContext and ProjectConfig state resolves against the requested project. Cobra's
// PersistentPreRunE performs the real directory change during execution, so the caller's directory
// is restored before returning. globalOpts.Cwd is normalized to an absolute path.
func newRootCmdForExecution(
	rootContainer *ioc.NestedContainer,
	globalOpts *internal.GlobalCommandOptions,
) (cmd *cobra.Command, err error) {
	if globalOpts.Cwd == "" {
		return NewRootCmd(false, nil, rootContainer), nil
	}

	absoluteCwd, err := filepath.Abs(globalOpts.Cwd)
	if err != nil {
		return nil, fmt.Errorf("resolving cwd: %w", err)
	}
	globalOpts.Cwd = absoluteCwd

	if _, statErr := os.Stat(absoluteCwd); os.IsNotExist(statErr) {
		// PersistentPreRunE owns prompting for and creating a missing --cwd directory.
		return NewRootCmd(false, nil, rootContainer), nil
	} else if statErr != nil {
		return nil, fmt.Errorf("checking cwd: %w", statErr)
	}

	previousCwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting current directory: %w", err)
	}
	if err := os.Chdir(absoluteCwd); err != nil {
		return nil, fmt.Errorf("changing directory to %s: %w", absoluteCwd, err)
	}
	defer func() {
		// Deferred so the process never keeps the temporary directory after a failure.
		if restoreErr := os.Chdir(previousCwd); restoreErr != nil && err == nil {
			cmd, err = nil, fmt.Errorf("restoring current directory: %w", restoreErr)
		}
	}()

	return NewRootCmd(false, nil, rootContainer), nil
}

// ExecuteWithAutoInstall executes the command and handles auto-installation of extensions for unknown commands.
func ExecuteWithAutoInstall(ctx context.Context, rootContainer *ioc.NestedContainer) *ExecuteResult {
	result := &ExecuteResult{}

	// Parse global flags BEFORE creating the command tree.
	// This allows us to access flag values (like --no-prompt, --debug) early for auto-install logic.
	// This also enables the global options to be set in the container for support during extension framework callbacks.
	globalOpts := &internal.GlobalCommandOptions{}
	if err := ParseGlobalFlags(os.Args[1:], globalOpts); err != nil {
		// main.go only exits on result.Err — print here so users see pre-cobra parse failures.
		fmt.Fprintln(os.Stderr, output.WithErrorFormat("ERROR: %s", err.Error()))
		result.Err = err
		return result
	}

	// Register GlobalCommandOptions as a singleton in the container BEFORE building the command tree.
	// This ensures all components (FlagsResolver, actions, etc.) get the same pre-parsed instance.
	ioc.RegisterInstance(rootContainer, globalOpts)

	// Creating the RootCmd takes care of registering common dependencies in rootContainer.
	// The command tree will retrieve globalOpts from the container via its FlagsResolver.
	rootCmd, err := newRootCmdForExecution(rootContainer, globalOpts)
	if err != nil {
		fmt.Fprintln(os.Stderr, output.WithErrorFormat("ERROR: %s", err.Error()))
		result.Err = err
		return result
	}

	var extensionManager *extensions.Manager
	var console input.Console

	// rootCmd.Find() returns error if the command is not identified. Cobra checks all the registered commands
	// and returns error if the input command is not registered.
	// This allows us to determine if a subcommand was provided or not or if the command is unknown.
	foundCmd, originalArgs, err := rootCmd.Find(os.Args[1:])
	if err == nil {
		// Detect lightspeed commands from the cobra annotation set by CobraBuilder.
		result.IsLightspeed = foundCmd.Annotations[actions.AnnotationLightspeed] == "true"

		// Start the background update check AFTER command identification.
		// Lightspeed commands (e.g., auth token) skip the update check entirely
		// so the process can exit as fast as possible — this prevents the
		// AzureDeveloperCLICredential 10-second subprocess timeout from being
		// hit when the update check is slow (laptop wake, DNS stalls, etc.).
		if !result.IsLightspeed {
			result.LatestVersion = startUpdateCheck(ctx)
		}

		projectExtensions := projectExtensionResult{}
		// A --cwd that does not exist yet holds no project, so resolving now would pick up the
		// caller's unrelated project instead.
		if projectDirExists(globalOpts.Cwd) {
			projectExtensions, err = tryAutoInstallProjectExtensions(
				ctx, rootContainer, foundCmd, originalArgs,
			)
			if err != nil {
				if resolveErr := rootContainer.Resolve(&console); resolveErr != nil {
					fmt.Fprintln(os.Stderr, output.WithErrorFormat("ERROR: %s", err.Error()))
				} else {
					displayAutoInstallError(ctx, console, err)
				}
				result.Err = err
				return result
			}
			if projectExtensions.declined {
				return result
			}

			if projectExtensions.installed {
				rootCmd = newRootCmdWithoutRegistration(rootContainer)
				foundCmd, originalArgs, err = rootCmd.Find(os.Args[1:])
				if err != nil {
					result.Err = err
					return result
				}
			}
		}

		// Check for partial namespace match (e.g., "ai" found but "ai.agent" not installed)
		partialNamespace, partialErr := tryAutoInstallForPartialNamespace(
			ctx, rootContainer, foundCmd, originalArgs,
		)
		if partialErr != nil {
			if resolveErr := rootContainer.Resolve(&console); resolveErr != nil {
				fmt.Fprintln(os.Stderr, output.WithErrorFormat("ERROR: %s", partialErr.Error()))
			} else {
				displayAutoInstallError(ctx, console, partialErr)
			}
			result.Err = partialErr
			return result
		}
		if partialNamespace.declined {
			return result
		}
		if partialNamespace.installed {
			// Extension was installed, rebuild command tree and execute
			rootCmd = newRootCmdWithoutRegistration(rootContainer)
			result.Err = rootCmd.ExecuteContext(ctx)
			return result
		}

		// Known command, proceed with normal execution. The failure is held separately because the
		// auto-install below declares its own err, and every path out of here has to report it.
		commandErr := rootCmd.ExecuteContext(ctx)

		// Only attempt service-host auto-install when the command failed with that specific error.
		// Other command errors (for example, unsupported output formats) should be returned directly.
		unsupportedErr, ok := errors.AsType[*project.UnsupportedServiceHostError](commandErr)
		if !ok {
			result.Err = commandErr
			return result
		}
		if projectExtensions.handled {
			if resolveErr := rootContainer.Resolve(&console); resolveErr != nil {
				fmt.Fprintln(os.Stderr, unsupportedErr.ErrorMessage)
			} else {
				console.Message(ctx, unsupportedErr.ErrorMessage)
			}
			result.Err = commandErr
			return result
		}

		if err := rootContainer.Resolve(&extensionManager); err != nil {
			log.Panic("failed to resolve extension manager for auto-install:", err)
		}
		if err := rootContainer.Resolve(&console); err != nil {
			log.Panic("failed to resolve console for unknown flags error:", err)
		}

		requiredHost := unsupportedErr.Host
		availableExtensionsForHost, err := extensionManager.FindExtensions(ctx, &extensions.FilterOptions{
			Capability: extensions.ServiceTargetProviderCapability,
			Provider:   requiredHost,
		})
		if err != nil {
			// Do not fail if we couldn't check for extensions - just report the command's own failure
			log.Println("Error: check for extensions. Skipping auto-install:", err)
			console.Message(ctx, unsupportedErr.ErrorMessage)
			result.Err = commandErr
			return result
		}
		installedExtensions, err := extensionManager.ListInstalled()
		if err != nil {
			log.Println("Error: list installed extensions. Skipping auto-install:", err)
			console.Message(ctx, unsupportedErr.ErrorMessage)
			// Auto-install could not run, so the command's own failure stands.
			result.Err = commandErr
			return result
		}
		// Offer only the extensions whose selected version supplies the host and that are not
		// already installed.
		availableExtensionsForHost = filterExtensionsForProvider(
			availableExtensionsForHost,
			extensions.ServiceTargetProviderCapability,
			requiredHost,
		)
		availableExtensionsForHost = uninstalledExtensionMatches(availableExtensionsForHost, installedExtensions)
		if len(availableExtensionsForHost) == 0 {
			// Nothing can be installed to supply the host, so the command's failure stands.
			console.Message(ctx, unsupportedErr.ErrorMessage)
			result.Err = commandErr
			return result
		}

		autoInstall, installErr := autoInstallCommandMatches(
			ctx,
			console,
			extensionManager,
			availableExtensionsForHost,
			fmt.Sprintf(
				"Your project requires support for host '%s'. Install the required extension to continue.",
				unsupportedErr.Host,
			),
		)
		if installErr != nil {
			displayAutoInstallError(ctx, console, installErr)
			result.Err = installErr
			return result
		}
		if autoInstall.declined {
			return result
		}
		if autoInstall.installed {
			// Extension was installed, build command tree and execute
			rootCmd := newRootCmdWithoutRegistration(rootContainer)
			result.Err = rootCmd.ExecuteContext(ctx)
			return result
		}
		result.Err = commandErr
		return result
	}

	// Unknown command path — always start the update check since these aren't lightspeed.
	result.LatestVersion = startUpdateCheck(ctx)

	// Extract flags that take values from the root command
	flagsWithValues := extractFlagsWithValues(rootCmd)

	// Find the first non-flag argument (the actual command) and check for unknown flags
	unknownCommand, unknownFlags := findFirstNonFlagArg(originalArgs, flagsWithValues)

	// If we have a command, check if it's a built-in command first
	if unknownCommand != "" {
		// Check if this is a built-in command first (includes core commands and installed extensions)
		if isBuiltInCommand(rootCmd, unknownCommand) {
			// This is a built-in command, proceed with normal execution without checking for extensions
			result.Err = rootCmd.ExecuteContext(ctx)
			return result
		}

		if err := rootContainer.Resolve(&extensionManager); err != nil {
			log.Panic("failed to resolve extension manager for auto-install:", err)
		}
		if err := rootContainer.Resolve(&console); err != nil {
			log.Panic("failed to resolve console for unknown flags error:", err)
		}

		// Check for deprecated commands and provide helpful redirection messages
		if unknownCommand == "login" {
			console.Message(ctx, "Error: The 'azd login' command has been removed.")
			console.Message(ctx, "Please use 'azd auth login' instead.")
			result.Err = fmt.Errorf("unknown command 'login'")
			return result
		}
		if unknownCommand == "logout" {
			console.Message(ctx, "Error: The 'azd logout' command has been removed.")
			console.Message(ctx, "Please use 'azd auth logout' instead.")
			result.Err = fmt.Errorf("unknown command 'logout'")
			return result
		}

		// If unknown flags were found before a non-built-in command, return an error with helpful guidance
		if len(unknownFlags) > 0 {
			flagsList := strings.Join(unknownFlags, ", ")
			errorMsg := fmt.Sprintf(
				"Unknown flags detected before command '%s': %s\n\n"+
					"If you're trying to run an extension command, the extension name must come BEFORE any flags.\n"+
					"This is because extension-specific flags are not known until the extension is installed.\n\n"+
					"Correct usage:\n"+
					"  azd %s %s    # Extension name first, then flags\n"+
					"  azd %s --help          # Get help for the extension\n\n"+
					"If this is not an extension command, please check the flag names for typos.",
				unknownCommand, flagsList,
				unknownCommand, strings.Join(unknownFlags, " "),
				unknownCommand)

			console.Message(ctx, errorMsg)
			result.Err = fmt.Errorf("unknown flags before command: %s", flagsList)
			return result
		}

		// Get all remaining arguments starting from the command for namespace matching
		// This allows checking longer namespaces like "something.demo.foo" from "azd something demo foo"
		var argsForMatching []string
		for i, arg := range originalArgs {
			if !strings.HasPrefix(arg, "-") && arg == unknownCommand {
				// Found the command, collect all non-flag arguments from here
				for j := i; j < len(originalArgs); j++ {
					if !strings.HasPrefix(originalArgs[j], "-") {
						argsForMatching = append(argsForMatching, originalArgs[j])
					}
				}
				break
			}
		}

		// Check if any commands might match extensions with various namespace lengths
		extensionMatches, err := checkForMatchingExtensions(ctx, extensionManager, argsForMatching)
		if err != nil {
			// Do not fail if we couldn't check for extensions - just proceed to normal execution
			log.Println("Error: check for extensions. Skipping auto-install:", err)
			result.Err = rootCmd.ExecuteContext(ctx)
			return result
		}

		if len(extensionMatches) > 0 {
			var console input.Console
			if err := rootContainer.Resolve(&console); err != nil {
				log.Panic("failed to resolve console for auto-install:", err)
			}

			autoInstall, installErr := autoInstallCommandMatches(
				ctx,
				console,
				extensionManager,
				extensionMatches,
				fmt.Sprintf(
					"Command '%s' isn't available. Install the required extension to use this command.",
					strings.Join(argsForMatching, " "),
				),
			)
			if installErr != nil {
				displayAutoInstallError(ctx, console, installErr)
				result.Err = installErr
				return result
			}
			if autoInstall.declined {
				return result
			}
			if autoInstall.installed {
				// Extension was installed, build command tree and execute
				rootCmd := newRootCmdWithoutRegistration(rootContainer)
				result.Err = rootCmd.ExecuteContext(ctx)
				return result
			}
		}
	}

	// Normal execution path - either no args, no matching extension, or user declined install
	result.Err = rootCmd.ExecuteContext(ctx)
	return result
}

// CreateGlobalFlagSet creates a new flag set with all global flags defined.
// This is the single source of truth for global flag definitions.
//
// When adding or modifying flags here, also update the reserved flag registry
// in internal/reserved_flags.go. The SDK package (pkg/azdext) derives its
// reserved flag indexes from that registry automatically.
func CreateGlobalFlagSet() *pflag.FlagSet {
	globalFlags := pflag.NewFlagSet("global", pflag.ContinueOnError)

	globalFlags.StringP("cwd", "C", "", "Sets the current working directory.")
	globalFlags.Bool("debug", false, "Enables debugging and diagnostics logging.")
	globalFlags.Bool(
		"no-prompt",
		false,
		"Runs without prompts. Uses existing values; "+
			"fails if any required value or decision cannot be resolved automatically. "+
			"Automatically enabled when azd detects a CI/CD or AI-agent environment; "+
			"set AZD_NON_INTERACTIVE=false to opt out of that automatic enablement.")
	globalFlags.Bool(
		"non-interactive",
		false,
		"Alias for --no-prompt.")
	_ = globalFlags.MarkHidden("non-interactive")
	globalFlags.StringP(internal.EnvironmentNameFlagName, "e", "", "The name of the environment to use.")

	// The telemetry system is responsible for reading these flags value and using it to configure the telemetry
	// system, but we still need to add it to our flag set so that when we parse the command line with Cobra we
	// don't error due to an "unknown flag".
	globalFlags.String("trace-log-file", "", "Write a diagnostics trace to a file.")
	_ = globalFlags.MarkHidden("trace-log-file")

	globalFlags.String("trace-log-url", "", "Send traces to an Open Telemetry compatible endpoint.")
	_ = globalFlags.MarkHidden("trace-log-url")

	return globalFlags
}

// ParseGlobalFlags parses global flags from the provided arguments and populates the GlobalCommandOptions.
// Uses ParseErrorsAllowlist to gracefully ignore unknown flags (like extension-specific flags).
// This function is designed to be called BEFORE Cobra command tree construction to enable
// early access to global flag values for auto-install and other pre-execution logic.
//
// Auto no-prompt detection: If --no-prompt is not explicitly set (via flag or the
// AZD_NON_INTERACTIVE env var) and azd is running in a non-interactive context — an AI coding
// agent (like Claude Code, GitHub Copilot CLI, Cursor, etc.) or a CI/CD environment — NoPrompt is
// automatically enabled. Explicit --no-prompt/--non-interactive flags and AZD_NON_INTERACTIVE take
// precedence; set AZD_NON_INTERACTIVE=false to opt out of this automatic enablement.
func ParseGlobalFlags(args []string, opts *internal.GlobalCommandOptions) error {
	globalFlagSet := CreateGlobalFlagSet()

	// Set output to io.Discard to suppress any error messages from pflag
	// Cobra will handle all user-facing output
	globalFlagSet.SetOutput(io.Discard)

	// Configure the flag set to ignore unknown flags. This is critical for extension commands
	// where extension-specific flags are not yet known and will be handled by the extension's
	// command parser after the extension is loaded.
	globalFlagSet.ParseErrorsAllowlist = pflag.ParseErrorsAllowlist{UnknownFlags: true}

	// Register --output/-o on the pre-parse set only (not in CreateGlobalFlagSet, which
	// would shadow per-command output.AddOutputParam and cause GetCommandFormatter to reject
	// the empty default). Without this, pflag's UnknownFlags allowlist walks attached short
	// values like "-o<value>" char-by-char and trips on "-e" (a known string flag), failing
	// with `flag needs an argument: 'e' in -e`.
	globalFlagSet.StringP("output", "o", "", "The output format (json, table, none).")

	// Parse the arguments - unknown flags will be silently ignored
	err := globalFlagSet.Parse(args)

	// Ignore help errors - let Cobra handle help requests
	if err != nil && !errors.Is(err, pflag.ErrHelp) {
		return fmt.Errorf("failed to parse global flags: %w", err)
	}

	// Bind parsed values to the options struct
	if strVal, err := globalFlagSet.GetString("cwd"); err == nil {
		opts.Cwd = strVal
	}

	if boolVal, err := globalFlagSet.GetBool("debug"); err == nil {
		opts.EnableDebugLogging = boolVal
	}

	// --non-interactive is an alias for --no-prompt; either flag sets NoPrompt.
	// When both are present, true wins (either flag opting in is sufficient).
	noPromptVal, _ := globalFlagSet.GetBool("no-prompt")
	nonInteractiveVal, _ := globalFlagSet.GetBool("non-interactive")
	opts.NoPrompt = noPromptVal || nonInteractiveVal

	// Check if either flag was explicitly provided on the command line
	noPromptFlag := globalFlagSet.Lookup("no-prompt")
	nonInteractiveFlag := globalFlagSet.Lookup("non-interactive")
	flagExplicitlySet := (noPromptFlag != nil && noPromptFlag.Changed) ||
		(nonInteractiveFlag != nil && nonInteractiveFlag.Changed)

	// Environment variable: AZD_NON_INTERACTIVE enables no-prompt mode when set to a
	// truthy value (parsed via strconv.ParseBool: "true", "1", "TRUE", etc.).
	// Explicit flags take precedence over this env var.
	// A successfully parsed boolean (true or false) counts as an explicit user choice and
	// suppresses agent/CI auto-detection below. A value that is not a valid boolean (e.g.
	// a typo like "yes") is ignored entirely: it does NOT set opts.NoPrompt and does NOT suppress
	// auto-detection, so a typo in CI still resolves to deterministic no-prompt behavior
	// rather than silently keeping azd interactive.
	envVarPresent := false
	if !flagExplicitlySet {
		if envVal, ok := os.LookupEnv("AZD_NON_INTERACTIVE"); ok {
			if parsed, err := strconv.ParseBool(envVal); err == nil {
				envVarPresent = true
				if parsed {
					opts.NoPrompt = true
				}
			} else {
				log.Printf(
					"warning: AZD_NON_INTERACTIVE=%q is not a valid boolean"+
						" (expected true/false/1/0), ignoring",
					envVal,
				)
			}
		}
	}

	// Parse -e/--environment with lenient validation.
	// Only accept values that look like valid environment names (alphanumeric, hyphens, dots,
	// underscores). Values that don't match (e.g., URLs from extensions reusing -e for
	// --project-endpoint) are silently ignored — the extension still receives the raw args
	// and can parse -e itself. This avoids breaking third-party extensions that use -e
	// for their own flags while still fixing the environment leak for valid env names.
	if strVal, err := globalFlagSet.GetString(internal.EnvironmentNameFlagName); err == nil && strVal != "" {
		if environment.IsValidEnvironmentName(strVal) {
			opts.EnvironmentName = strVal
		} else if opts.EnableDebugLogging {
			log.Printf(
				"debug: ignoring invalid environment name %q from -e/--environment flag"+
					" (does not match %s pattern)",
				strVal, environment.EnvironmentNameRegexp,
			)
		}
	}

	// Auto no-prompt detection: If no explicit flag or env var was set, automatically enable
	// no-prompt mode when azd runs in a non-interactive context where prompting is not possible —
	// either an AI coding agent or a CI/CD environment. This makes CI behavior deterministic:
	// prompts resolve to their defaults (or fail fast with an actionable error) instead of
	// silently aborting on an EOF stdin. Explicit flags and AZD_NON_INTERACTIVE take precedence;
	// set AZD_NON_INTERACTIVE=false to opt out of this automatic enablement (NoPrompt stays false).
	// Note: some commands still avoid interactive prompts in CI/CD by design, independent of this.
	if !flagExplicitlySet && !envVarPresent &&
		(agentdetect.IsRunningInAgent() || resource.IsRunningOnCI()) {
		opts.NoPrompt = true
	}

	return nil
}
