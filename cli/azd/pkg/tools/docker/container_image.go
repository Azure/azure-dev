package docker

import (
	"errors"
	"strings"
)

// ContainerImage represents a container image and its components
type ContainerImage struct {
	// The registry name
	Registry string
	// The repository name or path
	Repository string
	// The tag
	Tag string
}

// Local returns the local image name without registry
func (ci *ContainerImage) Local() string {
	builder := strings.Builder{}

	if ci.Repository != "" {
		builder.WriteString(ci.Repository)
	}

	if ci.Tag != "" {
		builder.WriteString(":")
		builder.WriteString(ci.Tag)
	}

	return builder.String()
}

// Remote returns the remote image name with registry when specified
func (ci *ContainerImage) Remote() string {
	builder := strings.Builder{}
	if ci.Registry != "" {
		builder.WriteString(ci.Registry)
		builder.WriteString("/")
	}

	if ci.Repository != "" {
		builder.WriteString(ci.Repository)
	}

	if ci.Tag != "" {
		builder.WriteString(":")
		builder.WriteString(ci.Tag)
	}

	return builder.String()
}

func ParseContainerImage(image string) (*ContainerImage, error) {
	// Check if the imageURL is empty
	if image == "" {
		return nil, errors.New("empty image URL provided")
	}

	containerImage := &ContainerImage{}

	// Detect tags
	tagParts := strings.Split(image, ":")
	if len(tagParts) > 2 {
		return containerImage, errors.New("invalid tag format")
	}

	if len(tagParts) == 2 {
		containerImage.Tag = tagParts[1]
		image = tagParts[0]
	}

	// Split the imageURL by "/"
	parts := strings.Split(image, "/")

	// Check if the parts contain a registry (parts[0] contains ".")
	if strings.Contains(parts[0], ".") {
		containerImage.Registry = parts[0]
		parts = parts[1:]
	}

	// Set the repository as the remaining parts joined by "/"
	containerImage.Repository = strings.Join(parts, "/")

	return containerImage, nil
}
