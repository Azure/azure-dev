// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package nextstep computes context-aware "Next: …" guidance shown after
// successful azd ai agent commands.
//
// Each command's success path calls a resolver (ResolveAfterInit,
// ResolveAfterRun, ResolveAfterInvokeLocal, ResolveAfterInvokeRemote,
// ResolveAfterShow, ResolveAfterDeploy) and prints the result with
// PrintNext. Resolvers consume an assembled *State that captures the
// project's azure.yaml services, azd environment values, and (where
// applicable) live runtime data.
//
// The package never returns errors for missing or partial state — the
// resolvers are designed to give useful guidance with whatever inputs
// are available. Callers must ensure that error paths are handled
// separately; structured error guidance lives in the exterrors package.
package nextstep
