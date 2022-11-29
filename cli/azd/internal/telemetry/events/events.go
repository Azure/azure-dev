// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package events provides definitions and functions related to the definition of telemetry events.
package events

// Command event names follow the convention cmd.<command invocation path with spaces replaced by .>.
//
// Examples:
//   - cmd.infra.create
//   - cmd.init
//   - cmd.up
const CommandEventPrefix = "cmd."

// BicepInstallEvent is the name of the event which tracks the overall bicep install operation.
const BicepInstallEvent = "tools.bicep.install"
