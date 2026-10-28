// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

// Public API reference: ListAgentServerTasksRequest in
// src/agentserver-persistence/routes.tsp, with record/page models in models.tsp:
// https://github.com/Azure/azure-rest-api-specs/tree/main/specification/ai-foundry/data-plane/Foundry
// The merged Tasks contract and its model/query definitions are also available at:
// https://github.com/Azure/azure-rest-api-specs/pull/45095/files

// TaskListOptions selects one page of project-scoped Tasks. AgentName is required.
// Zero Limit and empty Order leave the service defaults unchanged. Boolean pointers
// distinguish an omitted predicate from an explicit false predicate. Tags are repeated
// key:value equalities, split at the first colon. After is an opaque service cursor.
type TaskListOptions struct {
	AgentName            string
	SessionID            string
	Status               string
	SourceType           string
	LeaseOwner           string
	After                string
	Order                string
	Limit                int
	HasError             *bool
	LeaseExpired         *bool
	Tags                 []string
	OmitAttachmentValues *bool
}
