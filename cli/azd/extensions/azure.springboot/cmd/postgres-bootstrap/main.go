// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"azurespringboot/internal/spring"
	"context"
	"os"
)

func main() {
	if err := spring.RunPostgresBootstrap(
		context.Background(),
		os.Getenv("AZD_SPRING_POSTGRES_BOOTSTRAP_INPUT"),
		os.Stdout,
		os.Stderr,
	); err != nil {
		os.Exit(1)
	}
}
