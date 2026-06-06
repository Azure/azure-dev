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
	WeightType  string            `json:"weightType,omitempty"`
	BaseModel   string            `json:"baseModel,omitempty"`
	SystemData  *SystemData       `json:"systemData,omitempty"`
	Properties  map[string]any    `json:"properties,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
	CatalogInfo *CatalogInfo      `json:"catalogInfo,omitempty"`

	DerivedModelInformation *DerivedModelInformation `json:"derivedModelInformation,omitempty"`
	Source                  *ModelSource             `json:"source,omitempty"`
	ArtifactProfile         *ArtifactProfile         `json:"artifactProfile,omitempty"`
	LoRAConfig              *LoRAConfig              `json:"loraConfig,omitempty"`
	ProvisioningState       string                   `json:"provisioningState,omitempty"`
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

// ModelSource describes where a model originated from.
type ModelSource struct {
	SourceType        string `json:"sourceType,omitempty"`
	JobID             string `json:"jobId,omitempty"`
	HuggingFaceRepoID string `json:"huggingFaceRepoId,omitempty"`
	Revision          string `json:"revision,omitempty"`
}

// ArtifactProfile contains classification information about model artifacts.
type ArtifactProfile struct {
	Category string   `json:"category,omitempty"`
	Signals  []string `json:"signals,omitempty"`
}

// LoRAConfig contains adapter-specific metadata for LoRA models.
type LoRAConfig struct {
	Rank          *int     `json:"rank,omitempty"`
	Alpha         *int     `json:"alpha,omitempty"`
	TargetModules []string `json:"targetModules,omitempty"`
	Dropout       *float64 `json:"dropout,omitempty"`
}

// ListModelsResponse is the API response for listing custom models.
type ListModelsResponse struct {
	Value    []CustomModel `json:"value"`
	NextLink string        `json:"nextLink,omitempty"`
}

// CustomModelListView is the table-friendly view of a custom model.
type CustomModelListView struct {
	Name       string `table:"Name"`
	Version    string `table:"Version"`
	WeightType string `table:"Weight Type"`
	CreatedAt  string `table:"Created"`
	CreatedBy  string `table:"Created By"`
}

// ToListView converts a CustomModel to a table-friendly view.
func (m *CustomModel) ToListView() CustomModelListView {
	view := CustomModelListView{
		Name:       m.Name,
		Version:    m.Version,
		WeightType: m.WeightType,
	}

	if m.SystemData != nil {
		view.CreatedAt = m.SystemData.CreatedAt
		view.CreatedBy = m.SystemData.CreatedBy
	}

	return view
}
