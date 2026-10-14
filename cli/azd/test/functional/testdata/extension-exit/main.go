// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func main() {
	exitCode, err := strconv.Atoi(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid exit code")
		os.Exit(1)
	}

	if len(os.Args) > 2 && os.Args[2] == "structured" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		ctx = azdext.WithAccessToken(ctx)
		err := azdext.ReportError(ctx, fmt.Errorf("evaluation: %w", &azdext.LocalError{
			Message:    "quality gate not met",
			Code:       "quality_gate",
			Category:   azdext.LocalErrorCategoryValidation,
			Suggestion: "Inspect the evaluation results",
		}))
		cancel()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(99)
		}
	}

	if err := json.NewEncoder(os.Stdout).Encode(map[string]int{"exitCode": exitCode}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(99)
	}
	if exitCode != 0 {
		fmt.Fprintln(os.Stderr, "extension diagnostic")
	}
	os.Exit(exitCode)
}
