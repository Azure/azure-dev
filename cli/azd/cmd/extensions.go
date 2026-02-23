// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal/grpcserver"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	pkgux "github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// isJsonOutputFromArgs checks if --output json or -o json was passed in args
func isJsonOutputFromArgs(args []string) bool {
	for i, arg := range args {
		if arg == "--output" || arg == "-o" {
			if i+1 < len(args) && args[i+1] == "json" {
				return true
			}
		}
		if arg == "--output=json" || arg == "-o=json" {
			return true
		}
	}
	return false
}

// bindExtension binds the extension to the root command
func bindExtension(
	root *actions.ActionDescriptor,
	extension *extensions.Extension,
) error {
	// Split the namespace by dots to support nested namespaces
	namespaceParts := strings.Split(extension.Namespace, ".")

	// Start with the root command
	current := root

	// For each part except the last one, create or find a command in the hierarchy
	for i := 0; i < len(namespaceParts)-1; i++ {
		part := namespaceParts[i]

		// Check if a command with this name already exists
		found := false
		for _, child := range current.Children() {
			if child.Name == part {
				current = child
				found = true
				break
			}
		}

		// If not found, create a new command
		if !found {
			// Build the full namespace path up to this point for the description
			namespacePath := strings.Join(namespaceParts[:i+1], ".")
			description := fmt.Sprintf("Commands for the %s extension namespace.", namespacePath)
			cmd := &cobra.Command{
				Use:   part,
				Short: description,
			}

			current = current.Add(part, &actions.ActionDescriptorOptions{
				Command: cmd,
				GroupingOptions: actions.CommandGroupOptions{
					RootLevelHelp: actions.CmdGroupExtensions,
				},
			})
		}
	}

	// The last part of the namespace is the actual command
	lastPart := namespaceParts[len(namespaceParts)-1]

	cmd := &cobra.Command{
		Use:                lastPart,
		Short:              extension.Description,
		Long:               extension.Description,
		DisableFlagParsing: true,
		// Add extension metadata as annotations for faster lookup later during invocation.
		Annotations: map[string]string{
			"extension.id":        extension.Id,
			"extension.namespace": extension.Namespace,
		},
	}

	current.Add(lastPart, &actions.ActionDescriptorOptions{
		Command:                cmd,
		ActionResolver:         newExtensionAction,
		DisableTroubleshooting: true,
		GroupingOptions: actions.CommandGroupOptions{
			RootLevelHelp: actions.CmdGroupExtensions,
		},
	})

	return nil
}

type extensionAction struct {
	console          input.Console
	extensionRunner  *extensions.Runner
	lazyEnv          *lazy.Lazy[*environment.Environment]
	extensionManager *extensions.Manager
	azdServer        *grpcserver.Server
	cmd              *cobra.Command
	args             []string
}

func newExtensionAction(
	console input.Console,
	extensionRunner *extensions.Runner,
	commandRunner exec.CommandRunner,
	lazyEnv *lazy.Lazy[*environment.Environment],
	extensionManager *extensions.Manager,
	cmd *cobra.Command,
	azdServer *grpcserver.Server,
	args []string,
) actions.Action {
	return &extensionAction{
		console:          console,
		extensionRunner:  extensionRunner,
		lazyEnv:          lazyEnv,
		extensionManager: extensionManager,
		azdServer:        azdServer,
		cmd:              cmd,
		args:             args,
	}
}

func (a *extensionAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	extensionId, has := a.cmd.Annotations["extension.id"]
	if !has {
		return nil, fmt.Errorf("extension id not found")
	}

	extension, err := a.extensionManager.GetInstalled(extensions.FilterOptions{
		Id: extensionId,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get extension %s: %w", extensionId, err)
	}

	// Start update check in background while extension runs
	// By the time extension finishes, we'll have the result ready
	showUpdateWarning := !isJsonOutputFromArgs(os.Args)
	if showUpdateWarning {
		updateResultChan := make(chan *updateCheckOutcome, 1)
		// Create a minimal copy with only the fields needed for update checking.
		// Cannot copy the full Extension due to sync.Once (contains sync.noCopy).
		// The goroutine will re-fetch the full extension from config when saving.
		extForCheck := &extensions.Extension{
			Id:                extension.Id,
			DisplayName:       extension.DisplayName,
			Version:           extension.Version,
			Source:            extension.Source,
			LastUpdateWarning: extension.LastUpdateWarning,
		}
		go a.checkForUpdateAsync(ctx, extForCheck, updateResultChan)
		// Note: This defer runs AFTER the defer for a.azdServer.Stop() registered later,
		// because defers execute in LIFO order. This is intentional - we want to show
		// the warning after the extension completes but the server stop doesn't affect us.
		defer func() {
			// Collect result and show warning if needed (non-blocking read)
			select {
			case result := <-updateResultChan:
				if result != nil && result.shouldShow && result.warning != nil {
					a.console.MessageUxItem(ctx, result.warning)
					a.console.Message(ctx, "")

					// Record cooldown only after warning is actually displayed
					a.recordUpdateWarningShown(result.extensionId, result.extensionSource)
				}
			default:
				// Check didn't complete in time, skip warning (and don't record cooldown)
			}
		}()
	}

	tracing.SetUsageAttributes(
		fields.ExtensionId.String(extension.Id),
		fields.ExtensionVersion.String(extension.Version))

	allEnv := []string{}
	allEnv = append(allEnv, os.Environ()...)

	forceColor := !color.NoColor
	if forceColor {
		allEnv = append(allEnv, "FORCE_COLOR=1")
	}

	// Pass the console width down to the child process
	// COLUMNS is a semi-standard environment variable used by many Unix programs to determine the width of the terminal.
	width := pkgux.ConsoleWidth()
	if width > 0 {
		allEnv = append(allEnv, fmt.Sprintf("COLUMNS=%d", width))
	}

	env, err := a.lazyEnv.GetValue()
	if err == nil && env != nil {
		allEnv = append(allEnv, env.Environ()...)
	}

	serverInfo, err := a.azdServer.Start()
	if err != nil {
		return nil, fmt.Errorf("failed to start gRPC server: %w", err)
	}

	defer a.azdServer.Stop()

	jwtToken, err := grpcserver.GenerateExtensionToken(extension, serverInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to generate extension token")
	}

	allEnv = append(allEnv,
		fmt.Sprintf("AZD_SERVER=%s", serverInfo.Address),
		fmt.Sprintf("AZD_ACCESS_TOKEN=%s", jwtToken),
	)

	// Create a temp file for the extension to write structured error info into.
	// The path is passed via AZD_ERROR_FILE; the extension calls azdext.ReportError
	// (automatically via azdext.Run) which serializes a LocalError/ServiceError as
	// protojson into this file on failure.
	errorFileEnv, errorFilePath, cleanupErrorFile, err := createExtensionErrorFileEnv()
	if err != nil {
		log.Printf("failed to create extension error file: %v", err)
	} else {
		defer cleanupErrorFile()
		allEnv = append(allEnv, errorFileEnv)
	}

	// Propagate trace context to the extension process
	if traceEnv := tracing.Environ(ctx); len(traceEnv) > 0 {
		allEnv = append(allEnv, traceEnv...)
	}

	options := &extensions.InvokeOptions{
		Args: a.args,
		Env:  allEnv,
		// cmd extensions are always interactive (connected to terminal)
		Interactive: true,
	}

	_, invokeErr := a.extensionRunner.Invoke(ctx, extension, options)

	// Update warning is shown via defer above (runs after invoke completes)

	if invokeErr != nil {
		// Read the structured error the extension wrote to the error file.
		// This gives us a typed LocalError/ServiceError for telemetry classification
		// instead of just a generic exit-code error.
		reportedErr, readErr := readReportedExtensionError(errorFilePath)
		if readErr != nil {
			log.Printf("failed to read reported extension error: %v", readErr)
		} else if reportedErr != nil {
			// Wrap both errors so the chain contains both:
			// - reportedErr (LocalError/ServiceError) for telemetry classification
			// - invokeErr (ExtensionRunError) for UX middleware handling
			return nil, fmt.Errorf("%w: %w", reportedErr, invokeErr)
		}

		return nil, invokeErr
	}

	return nil, nil
}

// createExtensionErrorFileEnv creates a temp file for extension error reporting and returns
// the formatted env var (AZD_ERROR_FILE=<path>), the file path, and a cleanup function.
func createExtensionErrorFileEnv() (envVar string, errorFilePath string, cleanup func(), err error) {
	errorFile, err := os.CreateTemp("", "azd-ext-error-*.json")
	if err != nil {
		return "", "", func() {}, err
	}

	errorFilePath = errorFile.Name()
	if closeErr := errorFile.Close(); closeErr != nil {
		if removeErr := os.Remove(errorFilePath); removeErr != nil && !os.IsNotExist(removeErr) {
			log.Printf("failed to remove extension error file after close error: %v", removeErr)
		}
		return "", "", func() {}, closeErr
	}

	cleanup = func() {
		if removeErr := os.Remove(errorFilePath); removeErr != nil && !os.IsNotExist(removeErr) {
			log.Printf("failed to remove extension error file: %v", removeErr)
		}
	}

	return fmt.Sprintf("%s=%s", azdext.ExtensionErrorFileEnv, errorFilePath), errorFilePath, cleanup, nil
}

// readReportedExtensionError reads the structured error written by the extension to the
// given file path (if any).
func readReportedExtensionError(errorFilePath string) (error, error) {
	if errorFilePath == "" {
		return nil, nil
	}

	return azdext.ReadErrorFile(errorFilePath)
}

// updateCheckOutcome holds the result of an async update check
type updateCheckOutcome struct {
	shouldShow      bool
	warning         *ux.WarningMessage
	extensionId     string // Used to record cooldown only when warning is actually displayed
	extensionSource string // Source of the extension for precise lookup
}

// checkForUpdateAsync performs the update check in a goroutine and sends the result to the channel.
// This runs in parallel with the extension execution, so by the time the extension finishes,
// we have the result ready with zero added latency.
func (a *extensionAction) checkForUpdateAsync(
	ctx context.Context,
	extension *extensions.Extension,
	resultChan chan<- *updateCheckOutcome,
) {
	defer close(resultChan)

	outcome := &updateCheckOutcome{shouldShow: false}

	// Create cache manager
	cacheManager, err := extensions.NewRegistryCacheManager()
	if err != nil {
		log.Printf("failed to create cache manager: %v", err)
		resultChan <- outcome
		return
	}

	// Check if cache needs refresh - if so, refresh it now (we have time while extension runs)
	if cacheManager.IsExpiredOrMissing(ctx, extension.Source) {
		a.refreshCacheForSource(ctx, cacheManager, extension.Source)
	}

	// Create update checker
	updateChecker := extensions.NewUpdateChecker(cacheManager)

	// Check if we should show a warning (respecting cooldown)
	// Uses extension's LastUpdateWarning field
	if !updateChecker.ShouldShowWarning(extension) {
		resultChan <- outcome
		return
	}

	// Check for updates
	result, err := updateChecker.CheckForUpdate(ctx, extension)
	if err != nil {
		log.Printf("failed to check for extension update: %v", err)
		resultChan <- outcome
		return
	}

	if result.HasUpdate {
		outcome.shouldShow = true
		outcome.warning = extensions.FormatUpdateWarning(result)
		outcome.extensionId = extension.Id
		outcome.extensionSource = extension.Source
		// Note: Cooldown is recorded by caller only when warning is actually displayed
	}

	resultChan <- outcome
}

// recordUpdateWarningShown saves the cooldown timestamp after a warning is displayed
func (a *extensionAction) recordUpdateWarningShown(extensionId, extensionSource string) {
	// Re-fetch the full extension from config to avoid overwriting fields
	fullExtension, err := a.extensionManager.GetInstalled(extensions.FilterOptions{
		Id:     extensionId,
		Source: extensionSource,
	})
	if err != nil {
		log.Printf("failed to get extension for saving warning timestamp: %v", err)
		return
	}

	// Record the warning timestamp
	extensions.RecordUpdateWarningShown(fullExtension)

	// Save the updated extension to config
	if err := a.extensionManager.UpdateInstalled(fullExtension); err != nil {
		log.Printf("failed to save warning timestamp: %v", err)
	}
}

// refreshCacheForSource attempts to refresh the cache for a specific source
func (a *extensionAction) refreshCacheForSource(
	ctx context.Context,
	cacheManager *extensions.RegistryCacheManager,
	sourceName string,
) {
	// Find extensions from this source to get registry data
	sourceExtensions, err := a.extensionManager.FindExtensions(ctx, &extensions.FilterOptions{
		Source: sourceName,
	})
	if err != nil {
		log.Printf("failed to fetch extensions from source %s: %v", sourceName, err)
		return
	}

	// Cache the extensions
	if err := cacheManager.Set(ctx, sourceName, sourceExtensions); err != nil {
		log.Printf("failed to cache extensions for source %s: %v", sourceName, err)
	}
}
