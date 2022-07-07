// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

const (
	outputFlagName               = "output"
	supportedFormatterAnnotation = "github.com/azure/azure-dev/cli/azd/pkg/output/supportedOutputFormatters"
)

func AddOutputParam(cmd *cobra.Command, supportedFormats []Format, defaultFormat Format) *cobra.Command {
	formatNames := make([]string, len(supportedFormats))
	for i, f := range supportedFormats {
		formatNames[i] = string(f)
	}

	description := fmt.Sprintf("Output format (supported formats are %s)", strings.Join(formatNames, ", "))
	cmd.Flags().StringP(outputFlagName, "o", string(defaultFormat), description)

	// Only error that can occur is "flag not found", which is not possible given we just added the flag on the previous line
	_ = cmd.Flags().SetAnnotation(outputFlagName, supportedFormatterAnnotation, formatNames)

	return cmd
}

func GetFormatter(cmd *cobra.Command) (Formatter, error) {
	outputVal, err := cmd.Flags().GetString(outputFlagName)
	if err != nil {
		return nil, err
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
