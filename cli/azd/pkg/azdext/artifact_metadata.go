// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

// ArtifactMetadataKeyFromPackage marks an artifact that was supplied through
// the --from-package option rather than produced by azd. This is deployment
// payload provenance, not telemetry.
const ArtifactMetadataKeyFromPackage = "azd.fromPackage"
