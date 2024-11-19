package appdetect

import (
	"encoding/xml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
)

func TestGetEnvironmentVariablePlaceholderHandledValue(t *testing.T) {
	tests := []struct {
		name                 string
		inputValue           string
		environmentVariables map[string]string
		expectedValue        string
	}{
		{
			"No environment variable placeholder",
			"valueOne",
			map[string]string{},
			"valueOne",
		},
		{
			"Has invalid environment variable placeholder",
			"${VALUE_ONE",
			map[string]string{},
			"${VALUE_ONE",
		},
		{
			"Has valid environment variable placeholder, but environment variable not set",
			"${VALUE_TWO}",
			map[string]string{},
			"",
		},
		{
			"Has valid environment variable placeholder, and environment variable set",
			"${VALUE_THREE}",
			map[string]string{"VALUE_THREE": "valueThree"},
			"valueThree",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.environmentVariables {
				err := os.Setenv(k, v)
				require.NoError(t, err)
			}
			handledValue := getEnvironmentVariablePlaceholderHandledValue(tt.inputValue)
			require.Equal(t, tt.expectedValue, handledValue)
		})
	}
}

func TestDetectSpringBootVersion(t *testing.T) {
	tests := []struct {
		name            string
		currentRoot     *mavenProject
		project         *mavenProject
		expectedVersion string
	}{
		{
			"unknown",
			nil,
			nil,
			UnknownSpringBootVersion,
		},
		{
			"project.parent",
			nil,
			&mavenProject{
				Parent: parent{
					GroupId:    "org.springframework.boot",
					ArtifactId: "spring-boot-starter-parent",
					Version:    "2.x",
				},
			},
			"2.x",
		},
		{
			"project.dependencyManagement",
			nil,
			&mavenProject{
				DependencyManagement: dependencyManagement{
					Dependencies: []dependency{
						{
							GroupId:    "org.springframework.boot",
							ArtifactId: "spring-boot-dependencies",
							Version:    "2.x",
						},
					},
				},
			},
			"2.x",
		},
		{
			"project.dependencyManagement.property",
			nil,
			&mavenProject{
				Properties: Properties{
					Entries: []Property{
						{
							XMLName: xml.Name{
								Local: "version.spring.boot",
							},
							Value: "2.x",
						},
					},
				},
				DependencyManagement: dependencyManagement{
					Dependencies: []dependency{
						{
							GroupId:    "org.springframework.boot",
							ArtifactId: "spring-boot-dependencies",
							Version:    "${version.spring.boot}",
						},
					},
				},
			},
			"2.x",
		},
		{
			"root.parent",
			&mavenProject{
				Parent: parent{
					GroupId:    "org.springframework.boot",
					ArtifactId: "spring-boot-starter-parent",
					Version:    "3.x",
				},
			},
			nil,
			"3.x",
		},
		{
			"root.dependencyManagement",
			&mavenProject{
				DependencyManagement: dependencyManagement{
					Dependencies: []dependency{
						{
							GroupId:    "org.springframework.boot",
							ArtifactId: "spring-boot-dependencies",
							Version:    "3.x",
						},
					},
				},
			},
			nil,
			"3.x",
		},
		{
			"root.dependencyManagement.property",
			nil,
			&mavenProject{
				Properties: Properties{
					Entries: []Property{
						{
							XMLName: xml.Name{
								Local: "version.spring.boot",
							},
							Value: "3.x",
						},
					},
				},
				DependencyManagement: dependencyManagement{
					Dependencies: []dependency{
						{
							GroupId:    "org.springframework.boot",
							ArtifactId: "spring-boot-dependencies",
							Version:    "${version.spring.boot}",
						},
					},
				},
			},
			"3.x",
		},
		{
			"both.root.and.project.parent",
			&mavenProject{
				Parent: parent{
					GroupId:    "org.springframework.boot",
					ArtifactId: "spring-boot-starter-parent",
					Version:    "2.x",
				},
			},
			&mavenProject{
				Parent: parent{
					GroupId:    "org.springframework.boot",
					ArtifactId: "spring-boot-starter-parent",
					Version:    "3.x",
				},
			},
			"3.x",
		},
		{
			"both.root.and.project.dependencyManagement",
			&mavenProject{
				DependencyManagement: dependencyManagement{
					Dependencies: []dependency{
						{
							GroupId:    "org.springframework.boot",
							ArtifactId: "spring-boot-dependencies",
							Version:    "2.x",
						},
					},
				},
			},
			&mavenProject{
				DependencyManagement: dependencyManagement{
					Dependencies: []dependency{
						{
							GroupId:    "org.springframework.boot",
							ArtifactId: "spring-boot-dependencies",
							Version:    "3.x",
						},
					},
				},
			},
			"3.x",
		},
		{
			"both.root.and.project.dependencyManagement.property",
			&mavenProject{
				Properties: Properties{
					Entries: []Property{
						{
							XMLName: xml.Name{
								Local: "version.spring.boot",
							},
							Value: "2.x",
						},
					},
				},
				DependencyManagement: dependencyManagement{
					Dependencies: []dependency{
						{
							GroupId:    "org.springframework.boot",
							ArtifactId: "spring-boot-dependencies",
							Version:    "${version.spring.boot}",
						},
					},
				},
			},
			&mavenProject{
				Properties: Properties{
					Entries: []Property{
						{
							XMLName: xml.Name{
								Local: "version.spring.boot",
							},
							Value: "3.x",
						},
					},
				},
				DependencyManagement: dependencyManagement{
					Dependencies: []dependency{
						{
							GroupId:    "org.springframework.boot",
							ArtifactId: "spring-boot-dependencies",
							Version:    "${version.spring.boot}",
						},
					},
				},
			},
			"3.x",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version := detectSpringBootVersion(tt.currentRoot, tt.project)
			assert.Equal(t, tt.expectedVersion, version)
		})
	}
}
