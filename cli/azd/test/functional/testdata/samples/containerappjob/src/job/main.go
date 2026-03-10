// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"fmt"
	"os"
	"time"
)

func main() {
	fmt.Println("Container App Job started")
	fmt.Printf("Time: %s\n", time.Now().Format(time.RFC3339))

	// Read environment variables
	if msg := os.Getenv("JOB_MESSAGE"); msg != "" {
		fmt.Printf("Message: %s\n", msg)
	}

	// Simulate work
	fmt.Println("Processing...")
	time.Sleep(2 * time.Second)
	fmt.Println("Container App Job completed successfully")
}
