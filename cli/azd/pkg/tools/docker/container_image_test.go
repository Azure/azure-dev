package docker

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_ParseContainerImage_Success(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected ContainerImage
	}{
		{
			name:  "image with registry and tag",
			input: "registry.example.com/my-image:1.0",
			expected: ContainerImage{
				Registry:   "registry.example.com",
				Repository: "my-image",
				Tag:        "1.0",
			},
		},
		{
			name:  "image with registry and no tag",
			input: "registry.example.com/my-image",
			expected: ContainerImage{
				Registry:   "registry.example.com",
				Repository: "my-image",
				Tag:        "",
			},
		},
		{
			name:  "image with tag and no registry",
			input: "my-image:1.0",
			expected: ContainerImage{
				Registry:   "", // no registry
				Repository: "my-image",
				Tag:        "1.0",
			},
		},
		{
			name:  "image with no registry or tag",
			input: "my-image",
			expected: ContainerImage{
				Registry:   "", // no registry
				Repository: "my-image",
				Tag:        "",
			},
		},
		{
			name:  "image with multi-part repository",
			input: "registry.example.com/my-image/foo/bar:1.0",
			expected: ContainerImage{
				Registry:   "registry.example.com",
				Repository: "my-image/foo/bar",
				Tag:        "1.0",
			},
		},
		{
			name:  "image with host and port",
			input: "registry.example.com:5000/my-image:1.0",
			expected: ContainerImage{
				Registry:   "registry.example.com:5000",
				Repository: "my-image",
				Tag:        "1.0",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual, err := ParseContainerImage(tt.input)
			require.NoError(t, err)
			require.Equal(t, tt.expected, *actual)
		})
	}
}

func Test_ParseContainerImage_Invalid(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "empty image",
			input: "",
		},
		{
			name:  "image with only tag",
			input: ":1.0",
		},
		{
			name:  "image with multiple tags",
			input: "my-image:1.0:latest",
		},
		{
			name:  "image with only registry",
			input: "registry.example.com",
		},
		{
			name:  "image with only registry and tag",
			input: "registry.example.com:1.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual, err := ParseContainerImage(tt.input)
			require.Error(t, err)
			require.Nil(t, actual)
		})
	}

}

func Test_ContainerImage_Local_And_Remote(t *testing.T) {
	tests := []struct {
		name           string
		input          ContainerImage
		expectedLocal  string
		expectedRemote string
	}{
		{
			name: "image with registry and tag",
			input: ContainerImage{
				Registry:   "registry.example.com",
				Repository: "my-image",
				Tag:        "1.0",
			},
			expectedRemote: "registry.example.com/my-image:1.0",
			expectedLocal:  "my-image:1.0",
		},
		{
			name: "image with registry and no tag",
			input: ContainerImage{
				Registry:   "registry.example.com",
				Repository: "my-image",
				Tag:        "",
			},
			expectedRemote: "registry.example.com/my-image",
			expectedLocal:  "my-image",
		},
		{
			name: "image with tag and no registry",
			input: ContainerImage{
				Registry:   "", // no registry
				Repository: "my-image",
				Tag:        "1.0",
			},
			expectedRemote: "my-image:1.0",
			expectedLocal:  "my-image:1.0",
		},
		{
			name: "image with no registry or tag",
			input: ContainerImage{
				Registry:   "", // no registry
				Repository: "my-image",
				Tag:        "",
			},
			expectedRemote: "my-image",
			expectedLocal:  "my-image",
		},
		{
			name: "image with multi-part repository",
			input: ContainerImage{
				Registry:   "registry.example.com",
				Repository: "my-image/foo/bar",
				Tag:        "1.0",
			},
			expectedRemote: "registry.example.com/my-image/foo/bar:1.0",
			expectedLocal:  "my-image/foo/bar:1.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actualRemote := tt.input.Remote()
			require.Equal(t, tt.expectedRemote, actualRemote)

			actualLocal := tt.input.Local()
			require.Equal(t, tt.expectedLocal, actualLocal)
		})
	}
}
