// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

param exists bool
param name string

resource existingApp 'Microsoft.App/containerApps@2023-05-02-preview' existing = if (exists) {
  name: name
}

output containers array = exists ? existingApp!.properties.template.containers : []
