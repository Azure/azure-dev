//go:build tools
// +build tools

package cmd

// This file only contains build tool dependencies for azd and does not contain actual code artifacts.
// This is the recommended way of specifying tool dependencies.
// See https://github.com/golang/go/wiki/Modules#how-can-i-track-tool-dependencies-for-a-module for more details.

import (
	_ "github.com/google/wire/cmd/wire"
)
