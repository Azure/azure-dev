// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"go.opentelemetry.io/otel/propagation"
)

const (
	// flagAllowedValuesAnnotationPrefix is the cobra command annotation prefix
	// used to record per-command allowed values for an inherited persistent
	// flag. The full key is "azdext.allowed-values/<flagName>" and the value
	// is a comma-joined list. See [RegisterFlagOptions].
	flagAllowedValuesAnnotationPrefix = "azdext.allowed-values/"
	// flagDefaultAnnotationPrefix records the per-command default value for
	// an inherited persistent flag. See [RegisterFlagOptions].
	flagDefaultAnnotationPrefix = "azdext.default/"

	defaultOutputFlagUsage = "The output format"
)

// ExtensionContext holds parsed global state available to extension commands.
type ExtensionContext struct {
	Debug        bool
	NoPrompt     bool
	Cwd          string
	Environment  string
	OutputFormat string

	ctx context.Context
}

// Context returns the prepared context with tracing and access token metadata.
func (ec *ExtensionContext) Context() context.Context {
	if ec.ctx != nil {
		return ec.ctx
	}
	return context.Background()
}

// ExtensionCommandOptions configures the extension root command.
type ExtensionCommandOptions struct {
	// Name is the extension name (used in command Use field)
	Name string
	// Version is the extension version
	Version string
	// Use overrides the default Use string (defaults to Name)
	Use string
	// Short is a short description
	Short string
	// Long is a long description
	Long string
}

// NewExtensionRootCommand creates a root cobra.Command pre-configured for azd extensions.
// It automatically:
//   - Registers azd's global flags (--debug, --no-prompt, --cwd, -e/--environment, --output)
//   - Reads AZD_* environment variables set by the azd framework
//   - Sets up OpenTelemetry trace context from TRACEPARENT/TRACESTATE env vars
//   - Calls WithAccessToken() on the command context
//
// The returned command has PersistentPreRunE configured to set up the ExtensionContext
// and to apply per-command overrides registered via [RegisterFlagOptions]. Extensions
// must not replace PersistentPreRunE on the root or these behaviors will be lost.
//
// NOTE: This function and its companion helpers ([NewListenCommand], [NewMetadataCommand],
// [NewVersionCommand]) depend on [github.com/spf13/cobra]. If non-cobra CLI frameworks
// gain adoption among extension authors, these symbols are candidates for extraction into
// an azdext/cobra sub-package so the core SDK remains framework-agnostic.
func NewExtensionRootCommand(opts ExtensionCommandOptions) (*cobra.Command, *ExtensionContext) {
	extCtx := &ExtensionContext{}

	use := opts.Use
	if use == "" {
		use = opts.Name
	}

	cmd := &cobra.Command{
		Use:     use,
		Short:   opts.Short,
		Long:    opts.Long,
		Version: opts.Version,
		Annotations: map[string]string{
			"azd-sdk-root": "true",
		},
	}

	// Register persistent flags
	flags := cmd.PersistentFlags()
	flags.BoolVar(&extCtx.Debug, "debug", false, "Enables debug and diagnostics logging")
	flags.BoolVar(&extCtx.NoPrompt, "no-prompt", false, "Accepts the default value instead of prompting")
	flags.StringVarP(&extCtx.Cwd, "cwd", "C", "", "Sets the current working directory")
	flags.StringVarP(&extCtx.Environment, "environment", "e", "", "The name of the environment to use")
	flags.StringVarP(&extCtx.OutputFormat, "output", "o", "default", defaultOutputFlagUsage)

	// Hidden trace flags
	var traceLogFile, traceLogURL string
	flags.StringVar(&traceLogFile, "trace-log-file", "", "Write raw OpenTelemetry trace data to a file")
	flags.StringVar(&traceLogURL, "trace-log-url", "", "Send raw OpenTelemetry trace data to a URL")
	_ = flags.MarkHidden("trace-log-file")
	_ = flags.MarkHidden("trace-log-url")

	// Register delegating shell completion for the SDK-managed --output flag.
	// Cobra keys completion functions by *pflag.Flag pointer globally, so
	// individual subcommands can't register their own completions for this
	// inherited flag without conflicting. Instead this single delegating
	// function consults the executing subcommand's [RegisterFlagOptions]
	// annotations at completion time.
	_ = cmd.RegisterFlagCompletionFunc("output", flagOptionsCompletion("output"))

	defaultUsage := cmd.UsageFunc()

	// Wrap UsageFunc only. Cobra's default HelpFunc calls UsageString()
	// which goes through UsageFunc, so a single layer of mutation here
	// covers both `--help` rendering and usage-on-error rendering. Wrapping
	// HelpFunc in addition would cause overrides to be applied twice.
	cmd.SetUsageFunc(func(usageCmd *cobra.Command) error {
		restore := applyFlagOverridesForCommand(usageCmd)
		defer restore()
		return defaultUsage(usageCmd)
	})

	cmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		// Env-var fallback for flags not explicitly set
		if !cmd.Flags().Changed("debug") {
			if v := os.Getenv("AZD_DEBUG"); v != "" {
				if b, err := strconv.ParseBool(v); err == nil {
					extCtx.Debug = b
				}
			}
		}

		if !cmd.Flags().Changed("no-prompt") {
			if v := os.Getenv("AZD_NO_PROMPT"); v != "" {
				if b, err := strconv.ParseBool(v); err == nil {
					extCtx.NoPrompt = b
				}
			}
		}

		if !cmd.Flags().Changed("cwd") {
			if v := os.Getenv("AZD_CWD"); v != "" {
				extCtx.Cwd = v
			}
		}

		if !cmd.Flags().Changed("environment") {
			if v := os.Getenv("AZD_ENVIRONMENT"); v != "" {
				extCtx.Environment = v
			}
		}

		// Apply per-command flag option overrides registered via
		// RegisterFlagOptions: validate user-supplied values against the
		// allowed set, and substitute the per-command default when the user
		// did not pass the flag.
		if err := applyFlagOptionsForCommand(cmd); err != nil {
			return err
		}

		// Change working directory if specified.
		// This mirrors azd's own --cwd flag behavior. The value comes from the
		// trusted --cwd flag or AZD_CWD env var set by the azd framework.
		if extCtx.Cwd != "" {
			absPath, err := filepath.Abs(extCtx.Cwd)
			if err != nil {
				return fmt.Errorf("invalid working directory %q: %w", extCtx.Cwd, err)
			}
			if err := os.Chdir(absPath); err != nil {
				return err
			}
		}

		// Extract OTel trace context from environment
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}

		if parent := os.Getenv(TraceparentEnv); parent != "" {
			tc := propagation.TraceContext{}
			ctx = tc.Extract(ctx, propagation.MapCarrier{
				TraceparentKey: parent,
				TracestateKey:  os.Getenv(TracestateEnv),
			})
		}

		// Inject gRPC access token
		ctx = WithAccessToken(ctx)

		extCtx.ctx = ctx
		cmd.SetContext(ctx)

		return nil
	}

	return cmd, extCtx
}

// RegisterFlagOptions configures per-subcommand metadata for an inherited
// persistent flag (typically one registered by [NewExtensionRootCommand], such
// as -o/--output). When set, the SDK uses this single registration to drive:
//
//   - help and usage rendering — the flag's help text is annotated with
//     "(supported: ...)" and the per-command default is shown
//   - extension metadata JSON (see [GenerateExtensionMetadata]) — populates
//     [extensions.Flag.ValidValues] and overrides [extensions.Flag.Default]
//   - parse-time validation — values not in allowedValues are rejected before
//     the command's RunE is invoked
//   - shell completion — allowedValues are suggested for the flag
//   - default substitution — when the user did not pass the flag, the
//     bound variable (e.g. [ExtensionContext.OutputFormat]) is set to
//     defaultValue before RunE runs, so RunE can read it directly without
//     extra normalization
//
// Pass an empty allowedValues to skip validation/completion while still
// customizing the default. Pass an empty defaultValue to leave the SDK
// default in place. Calling this multiple times for the same flag overwrites
// the previous configuration. Passing a nil command or empty flagName is a
// no-op.
//
// Typical usage in an extension subcommand:
//
//	cmd := &cobra.Command{Use: "list", RunE: runList}
//	azdext.RegisterFlagOptions(cmd, "output", []string{"json", "table"}, "json")
func RegisterFlagOptions(cmd *cobra.Command, flagName string, allowedValues []string, defaultValue string) *cobra.Command {
	if cmd == nil || flagName == "" {
		return cmd
	}
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	if len(allowedValues) > 0 {
		cmd.Annotations[flagAllowedValuesAnnotationPrefix+flagName] = strings.Join(allowedValues, ",")
		// Best-effort: register completion if the flag is owned directly by
		// cmd (not inherited). For inherited persistent flags managed by
		// [NewExtensionRootCommand], a delegating completion function is
		// already registered at the root that consults this command's
		// annotations; RegisterFlagCompletionFunc would error with "already
		// registered" here, which we deliberately ignore.
		_ = cmd.RegisterFlagCompletionFunc(
			flagName,
			cobra.FixedCompletions(allowedValues, cobra.ShellCompDirectiveNoFileComp),
		)
	} else {
		delete(cmd.Annotations, flagAllowedValuesAnnotationPrefix+flagName)
	}
	if defaultValue != "" {
		cmd.Annotations[flagDefaultAnnotationPrefix+flagName] = defaultValue
	} else {
		delete(cmd.Annotations, flagDefaultAnnotationPrefix+flagName)
	}
	return cmd
}

// flagOverride holds the per-command configuration recorded by
// [RegisterFlagOptions] for a single inherited flag.
type flagOverride struct {
	AllowedValues []string
	Default       string
}

// flagOptionsCompletion returns a cobra completion function for flagName that
// resolves allowed values from the executing command's [RegisterFlagOptions]
// annotations. Used as a delegating completion for SDK-managed inherited
// persistent flags (notably --output): cobra keys completion funcs by
// *pflag.Flag pointer, which is shared across the tree for inherited flags,
// so per-subcommand completions can't be registered directly.
func flagOptionsCompletion(flagName string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		if ov, ok := flagOverridesForCommand(cmd)[flagName]; ok && len(ov.AllowedValues) > 0 {
			return ov.AllowedValues, cobra.ShellCompDirectiveNoFileComp
		}
		return nil, cobra.ShellCompDirectiveDefault
	}
}

// flagOverridesForCommand returns the per-command flag overrides recorded on
// cmd via [RegisterFlagOptions], keyed by flag name. Returns nil when none
// are registered.
func flagOverridesForCommand(cmd *cobra.Command) map[string]flagOverride {
	if cmd == nil || cmd.Annotations == nil {
		return nil
	}
	out := map[string]flagOverride{}
	for key, val := range cmd.Annotations {
		switch {
		case strings.HasPrefix(key, flagAllowedValuesAnnotationPrefix):
			name := strings.TrimPrefix(key, flagAllowedValuesAnnotationPrefix)
			o := out[name]
			if val != "" {
				o.AllowedValues = strings.Split(val, ",")
			}
			out[name] = o
		case strings.HasPrefix(key, flagDefaultAnnotationPrefix):
			name := strings.TrimPrefix(key, flagDefaultAnnotationPrefix)
			o := out[name]
			o.Default = val
			out[name] = o
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// applyFlagOverridesForCommand transiently mutates the inherited persistent
// flags referenced by cmd's per-command overrides so help/usage rendering
// reflects the per-command supported values and default. Returns a restore
// function that callers must defer to undo the mutation.
func applyFlagOverridesForCommand(cmd *cobra.Command) func() {
	overrides := flagOverridesForCommand(cmd)
	if len(overrides) == 0 || cmd == nil {
		return func() {}
	}
	type saved struct {
		flag         *pflag.Flag
		origUsage    string
		origDefValue string
	}
	var savedFlags []saved
	for name, ov := range overrides {
		flag := cmd.Flags().Lookup(name)
		if flag == nil {
			continue
		}
		savedFlags = append(savedFlags, saved{flag, flag.Usage, flag.DefValue})
		if len(ov.AllowedValues) > 0 {
			flag.Usage = fmt.Sprintf("%s (supported: %s)", flag.Usage, strings.Join(ov.AllowedValues, ", "))
		}
		if ov.Default != "" {
			flag.DefValue = ov.Default
		}
	}
	return func() {
		for _, s := range savedFlags {
			s.flag.Usage = s.origUsage
			s.flag.DefValue = s.origDefValue
		}
	}
}

// applyFlagOptionsForCommand validates and defaults the inherited persistent
// flags configured via [RegisterFlagOptions] for the executing command. It is
// invoked from the root command's PersistentPreRunE so it runs once before
// the leaf command's RunE.
//
// For each registered override:
//   - If the user supplied a value (cmd.Flags().Changed) and an allowedValues
//     set is configured, the value is checked and a descriptive error is
//     returned for unsupported values.
//   - If the user did not supply a value and a per-command default is
//     configured, the flag is set to that default. Because the SDK persistent
//     flags are bound to fields on [ExtensionContext], this also updates the
//     extension context value (e.g. [ExtensionContext.OutputFormat]).
func applyFlagOptionsForCommand(cmd *cobra.Command) error {
	overrides := flagOverridesForCommand(cmd)
	for name, ov := range overrides {
		flag := cmd.Flags().Lookup(name)
		if flag == nil {
			continue
		}
		if cmd.Flags().Changed(name) {
			if len(ov.AllowedValues) == 0 {
				continue
			}
			current := flag.Value.String()
			if !slices.Contains(ov.AllowedValues, current) {
				return fmt.Errorf(
					"invalid value %q for --%s (supported: %s)",
					current, name, strings.Join(ov.AllowedValues, ", "),
				)
			}
			continue
		}
		if ov.Default != "" {
			if err := cmd.Flags().Set(name, ov.Default); err != nil {
				return fmt.Errorf("apply default for --%s: %w", name, err)
			}
		}
	}
	return nil
}
