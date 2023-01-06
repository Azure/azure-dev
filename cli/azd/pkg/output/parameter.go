// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	outputFlagName               = "output"
	supportedFormatterAnnotation = "github.com/azure/azure-dev/cli/azd/pkg/output/supportedOutputFormatters"
)

func AddOutputFlag(f *pflag.FlagSet, s *string, supportedFormats []Format, defaultFormat Format) {
	formatNames := make([]string, len(supportedFormats))
	for i, f := range supportedFormats {
		formatNames[i] = string(f)
	}

	description := fmt.Sprintf("The output format (the supported formats are %s).", strings.Join(formatNames, ", "))
	f.StringVarP(s, outputFlagName, "o", string(defaultFormat), description)

	// Only error that can occur is "flag not found", which is not possible given we just added the flag on the previous line
	_ = f.SetAnnotation(outputFlagName, supportedFormatterAnnotation, formatNames)
}

func AddOutputParam(cmd *cobra.Command, supportedFormats []Format, defaultFormat Format) *cobra.Command {
	discard := new(string)
	AddOutputFlag(cmd.Flags(), discard, supportedFormats, defaultFormat)
	return cmd
}

func GetCommandFormatter(cmd *cobra.Command) (Formatter, error) {
	// If the command does not specify any output params just return nil Formatter pointer
	outputVal, err := cmd.Flags().GetString(outputFlagName)
	if err != nil {
		return &NoneFormatter{}, nil
	}

	desiredFormatter := strings.ToLower(strings.TrimSpace(outputVal))
	f := cmd.Flags().Lookup(outputFlagName)
	supportedFormatters, hasFormatters := f.Annotations[supportedFormatterAnnotation]
	if !hasFormatters {
		return NewFormatter(desiredFormatter)
	}

	supported := false
	for _, formatter := range supportedFormatters {
		if formatter == desiredFormatter {
			supported = true
			break
		}
	}
	if !supported {
		return nil, fmt.Errorf("unsupported format '%s'", desiredFormatter)
	}

	return NewFormatter(desiredFormatter)
}
