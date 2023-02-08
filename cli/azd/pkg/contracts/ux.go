// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package contracts

// EndpointEventData is the schema of the payload for an event
// of the EndpointEventDataType type.
type EndpointEventData struct {
	Endpoint string `json:"endpoint"`
}

// OperationStartEventData is the schema of the payload for an event
// of the OperationStartEventDataType type.
type OperationStartEventData struct {
	Title string `json:"title"`
	Note  string `json:"note"`
}

// WarningEventData is the schema of the payload for an event
// of the WarningEventDataType type.
type WarningEventData struct {
	Description string `json:"description"`
	HidePrefix  bool   `json:"hidePrefix"`
}
