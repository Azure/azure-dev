// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package models

// CustomModel represents a custom model registered in Azure AI Foundry.
type CustomModel struct {
	ID          string            `json:"id,omitempty" table:"ID"`
	Name        string            `json:"name" table:"Name"`
	DisplayName string            `json:"displayName,omitempty"`
	Description string            `json:"description,omitempty"`
	Version     string            `json:"version" table:"Version"`
	BlobURI     string            `json:"blobUri,omitempty"`
	SystemData  *SystemData       `json:"systemData,omitempty"`
	Properties  map[string]any    `json:"properties,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
	CatalogInfo *CatalogInfo      `json:"catalogInfo,omitempty"`

	DerivedModelInformation *DerivedModelInformation `json:"derivedModelInformation,omitempty"`
}

// SystemData contains Azure system metadata for a resource.
type SystemData struct {
	CreatedAt      string `json:"createdAt,omitempty" table:"Created"`
	CreatedBy      string `json:"createdBy,omitempty"`
	CreatedByType  string `json:"createdByType,omitempty"`
	LastModifiedAt string `json:"lastModifiedAt,omitempty"`
}

// CatalogInfo contains catalog-level metadata.
type CatalogInfo struct {
	PublisherID string  `json:"publisherId,omitempty"`
	License     *string `json:"license,omitempty"`
}

// DerivedModelInformation contains information about the base model.
type DerivedModelInformation struct {
	BaseModel *string `json:"baseModel,omitempty"`
}

// ListModelsResponse is the API response for listing custom models.
type ListModelsResponse struct {
	Value    []CustomModel `json:"value"`
	NextLink string        `json:"nextLink,omitempty"`
}

// CustomModelListView is the table-friendly view of a custom model.
type CustomModelListView struct {
	Name      string `table:"Name"`
	Version   string `table:"Version"`
	CreatedAt string `table:"Created"`
	CreatedBy string `table:"Created By"`
}

// ToListView converts a CustomModel to a table-friendly view.
func (m *CustomModel) ToListView() CustomModelListView {
	view := CustomModelListView{
		Name:    m.Name,
		Version: m.Version,
	}

	if m.SystemData != nil {
		view.CreatedAt = m.SystemData.CreatedAt
		view.CreatedBy = m.SystemData.CreatedBy
	}

	return view
}
