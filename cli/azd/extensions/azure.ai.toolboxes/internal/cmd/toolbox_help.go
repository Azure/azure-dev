// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

// fileShapeBlurb returns the JSON/YAML payload documentation shared by
// `toolbox create --from-file` and `toolbox connection add --from-file`.
//
// includeDescription controls whether the `description` field is advertised.
// It is accepted by `create` but not by `connection add` (which carries the
// existing version's description forward).
func fileShapeBlurb(includeDescription bool) string {
	if includeDescription {
		return `File shape (JSON example):

  {
    "description": "research toolbox",
    "connections": [
      { "name": "my-mcp" },
      { "name": "my-search", "index": "products" }
    ]
  }

Equivalent YAML:

  description: research toolbox
  connections:
    - name: my-mcp
    - name: my-search
      index: products

Fields:
  description   Optional. Stored on the initial toolbox version.
  connections   Required. List of existing project connections to attach.
                Each entry needs 'name' (the project connection short name).
                'index' is required only for CognitiveSearch connections and
                is the search index name inside that service.
                Supported connection categories: RemoteTool (MCP),
                CognitiveSearch (Azure AI Search).

Project connections must already exist on the Foundry project; this command
does not create them. Run 'azd ai agent connection list' to see available
connections.`
	}

	return `File shape (JSON example):

  {
    "connections": [
      { "name": "my-mcp" },
      { "name": "my-search", "index": "products" }
    ]
  }

Equivalent YAML:

  connections:
    - name: my-mcp
    - name: my-search
      index: products

Fields:
  connections   Required. List of existing project connections to attach.
                Each entry needs 'name' (the project connection short name).
                'index' is required only for CognitiveSearch connections and
                is the search index name inside that service.
                Supported connection categories: RemoteTool (MCP),
                CognitiveSearch (Azure AI Search).

The toolbox's existing description is carried forward unchanged; use
'azd ai toolbox update' to change it.

Project connections must already exist on the Foundry project; this command
does not create them. Run 'azd ai agent connection list' to see available
connections.`
}
