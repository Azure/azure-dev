package docker

import (
	"errors"
	"strings"
)

// ContainerImage represents a container image and its components
type ContainerImage struct {
	// The registry name
	Registry string `json:"registry,omitempty"`
	// The repository name or path
	Repository string `json:"repository,omitempty"`
	// The tag
	Tag string `json:"tag,omitempty"`
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
	imageWithTag := image
	slashParts := strings.Split(imageWithTag, "/")

	// Detect tags
	if len(slashParts) > 1 {
		imageWithTag = slashParts[len(slashParts)-1]
	}

	tagParts := strings.Split(imageWithTag, ":")
	if len(tagParts) > 2 {
		return nil, errors.New("invalid tag format")
	}

	if len(tagParts) == 2 {
		containerImage.Tag = tagParts[1]
	}

	allParts := slashParts[:len(slashParts)-1]
	allParts = append(allParts, tagParts[0])

	// Check if the parts contain a registry (parts[0] contains ".")
	if strings.Contains(allParts[0], ".") {
		containerImage.Registry = allParts[0]
		allParts = allParts[1:]
	}

	// Set the repository as the remaining parts joined by "/"
	containerImage.Repository = strings.Join(allParts, "/")

	if containerImage.Repository == "" {
		return nil, errors.New("empty repository")
	}

	return containerImage, nil
}
