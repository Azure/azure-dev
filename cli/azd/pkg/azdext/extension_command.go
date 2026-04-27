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
	// flagAllowedValuesAnnotationPrefix prefixes cobra annotation keys that
	// record per-command allowed values for an inherited flag. The value is
	// a comma-joined list. See [RegisterFlagOptions].
	flagAllowedValuesAnnotationPrefix = "azdext.allowed-values/"
	// flagDefaultAnnotationPrefix prefixes cobra annotation keys that record
	// the per-command default for an inherited flag. See [RegisterFlagOptions].
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

	// Delegating completion for the SDK-managed --output flag. Cobra keys
	// completion funcs by *pflag.Flag pointer, so subcommands can't register
	// their own for an inherited flag; this delegate reads the executing
	// subcommand's [RegisterFlagOptions] annotations at completion time.
	// Other inherited string flags (--environment, --cwd) can be added if
	// extensions need to constrain them.
	_ = cmd.RegisterFlagCompletionFunc("output", flagOptionsCompletion("output"))

	defaultUsage := cmd.UsageFunc()

	// Wrap UsageFunc only — cobra's default HelpFunc calls UsageString
	// which goes through UsageFunc, so wrapping both would double-apply
	// the per-command help overrides.
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

		// Validate and apply per-command overrides from [RegisterFlagOptions].
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

// FlagOptions describes per-subcommand configuration for an inherited
// persistent flag. See [RegisterFlagOptions] for the effects each field
// drives.
type FlagOptions struct {
	// Name is the flag name without leading dashes (e.g. "output"). Required.
	Name string

	// AllowedValues, when non-empty, restricts accepted values, drives
	// "(supported: ...)" help text, populates metadata ValidValues, and
	// powers shell completion.
	AllowedValues []string

	// Default, when non-empty, becomes the per-subcommand default: shown in
	// help, surfaced in metadata, and substituted into the bound variable
	// (e.g. [ExtensionContext.OutputFormat]) when the user does not pass
	// the flag.
	Default string
}

// RegisterFlagOptions configures per-subcommand behavior for an inherited
// persistent flag (typically one registered by [NewExtensionRootCommand],
// such as -o/--output). One declaration drives:
//
//   - help/usage rendering — flag usage gets "(supported: ...)" appended and
//     shows the per-command default
//   - extension metadata (see [GenerateExtensionMetadata]) — populates the
//     flag's ValidValues field and overrides its Default field
//   - parse-time validation — values outside AllowedValues are rejected
//     before the command's RunE runs
//   - shell completion — AllowedValues are suggested for the flag
//   - default substitution — when the user does not pass the flag, the
//     bound variable is set to Default before RunE runs
//
// Empty AllowedValues skips validation/completion. Empty Default leaves the
// SDK default in place. Repeat calls for the same flag overwrite. A nil
// command or empty Name is a no-op.
//
// Panics if Default is set but not in a non-empty AllowedValues, since this
// would silently bypass validation.
//
// Typical usage:
//
//	cmd := &cobra.Command{Use: "list", RunE: runList}
//	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
//	    Name:          "output",
//	    AllowedValues: []string{"json", "table"},
//	    Default:       "json",
//	})
func RegisterFlagOptions(cmd *cobra.Command, opts FlagOptions) *cobra.Command {
	if cmd == nil || opts.Name == "" {
		return cmd
	}
	if opts.Default != "" && len(opts.AllowedValues) > 0 && !slices.Contains(opts.AllowedValues, opts.Default) {
		panic(fmt.Sprintf(
			"azdext.RegisterFlagOptions: default %q for flag %q is not in allowed values %v",
			opts.Default, opts.Name, opts.AllowedValues,
		))
	}
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	if len(opts.AllowedValues) > 0 {
		cmd.Annotations[flagAllowedValuesAnnotationPrefix+opts.Name] = strings.Join(opts.AllowedValues, ",")
	} else {
		delete(cmd.Annotations, flagAllowedValuesAnnotationPrefix+opts.Name)
	}
	if opts.Default != "" {
		cmd.Annotations[flagDefaultAnnotationPrefix+opts.Name] = opts.Default
	} else {
		delete(cmd.Annotations, flagDefaultAnnotationPrefix+opts.Name)
	}
	return cmd
}

// flagOverride holds the per-command configuration recorded by
// [RegisterFlagOptions] for a single inherited flag.
type flagOverride struct {
	AllowedValues []string
	Default       string
}

// flagOptionsCompletion returns a delegating completion func for flagName
// that resolves allowed values from the executing command's
// [RegisterFlagOptions] annotations at completion time. Used so completions
// for SDK-managed inherited flags can vary per subcommand (cobra keys flag
// completions by *pflag.Flag pointer, which is shared for inherited flags).
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

// applyFlagOverridesForCommand transiently mutates flag Usage/DefValue so
// help/usage rendering reflects cmd's per-command [RegisterFlagOptions]
// overrides. Returns a restore func that callers must defer.
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

// applyFlagOptionsForCommand applies [RegisterFlagOptions] overrides for cmd:
// rejects user-supplied values not in AllowedValues, and substitutes Default
// when the flag was not passed. Run from the root PersistentPreRunE so the
// substituted value reaches the bound [ExtensionContext] field before RunE.
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
