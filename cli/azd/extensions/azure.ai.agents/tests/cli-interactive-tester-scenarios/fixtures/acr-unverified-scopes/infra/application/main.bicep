// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

targetScope = 'subscription'

resource custom 'Microsoft.Resources/resourceGroups@2024-03-01' = {
  name: 'custom'
  location: 'eastus2'
}

resource additional 'Microsoft.Resources/resourceGroups@2024-03-01' = {
  name: 'additional'
  location: 'eastus2'
}
