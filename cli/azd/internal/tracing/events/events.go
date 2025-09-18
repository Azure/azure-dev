// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package events provides definitions and functions related to the definition of telemetry events.
package events

// Command event names follow the convention cmd.<command invocation path with spaces replaced by .>.
//
// Examples:
//   - cmd.auth.login
//   - cmd.init
//   - cmd.up
const CommandEventPrefix = "cmd."

// Prefix for vsrpc events.
const VsRpcEventPrefix = "vsrpc."

// PackBuildEvent is the name of the event which tracks the overall pack build operation.
const PackBuildEvent = "tools.pack.build"
