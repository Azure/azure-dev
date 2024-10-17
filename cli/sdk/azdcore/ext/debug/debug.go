package debug

import (
	"fmt"
	"os"

	"github.com/azure/azure-dev/cli/sdk/azdcore/ux"
)

func WaitForDebugger() {
	if _, has := os.LookupEnv("AZD_DEBUG"); has {
		for {
			debugConfirm := ux.NewConfirm(&ux.ConfirmConfig{
				Message:      fmt.Sprintf("Debugger Ready? (pid: %d)", os.Getpid()),
				DefaultValue: ux.Ptr(true),
			})

			ready, err := debugConfirm.Ask()
			if err == nil && *ready {
				return
			}
		}
	}
}
