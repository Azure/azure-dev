// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

type DeploymentPreview struct {
	Status     string
	Properties *DeploymentPreviewProperties
}

type DeploymentPreviewProperties struct {
	Changes []*DeploymentPreviewChange
}

type DeploymentPreviewChange struct {
	ChangeType        ChangeType
	ResourceId        Resource
	ResourceType      string
	Name              string
	UnsupportedReason string
	Before            interface{}
	After             interface{}
	Delta             []DeploymentPreviewPropertyChange
}

type DeploymentPreviewPropertyChange struct {
	ChangeType PropertyChangeType
	Path       string
	Before     interface{}
	After      interface{}
	Children   []DeploymentPreviewPropertyChange
}

type ChangeType string

const (
	ChangeTypeCreate      ChangeType = "Create"
	ChangeTypeDelete      ChangeType = "Delete"
	ChangeTypeDeploy      ChangeType = "Deploy"
	ChangeTypeIgnore      ChangeType = "Ignore"
	ChangeTypeModify      ChangeType = "Modify"
	ChangeTypeNoChange    ChangeType = "NoChange"
	ChangeTypeUnsupported ChangeType = "Unsupported"
)

type PropertyChangeType string

const (
	PropertyChangeTypeArray    PropertyChangeType = "Array"
	PropertyChangeTypeCreate   PropertyChangeType = "Create"
	PropertyChangeTypeDelete   PropertyChangeType = "Delete"
	PropertyChangeTypeModify   PropertyChangeType = "Modify"
	PropertyChangeTypeNoEffect PropertyChangeType = "NoEffect"
)
