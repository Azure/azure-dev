package appdetect

import (
	"encoding/xml"
	"testing"

	"github.com/stretchr/testify/assert"
)

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
			"detect.root.parent.when.project.not.found",
			&mavenProject{
				Parent: parent{
					GroupId:    "org.springframework.boot",
					ArtifactId: "spring-boot-starter-parent",
					Version:    "2.x",
				},
			},
			&mavenProject{
				Parent: parent{
					GroupId:    "org.test",
					ArtifactId: "test-parent",
					Version:    "3.x",
				},
			},
			"2.x",
		},
		{
			"detect.root.dependencyManagement.when.project.not.found",
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
							GroupId:    "org.test",
							ArtifactId: "test-dependencies",
							Version:    "3.x",
						},
					},
				},
			},
			"2.x",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version := detectSpringBootVersion(tt.currentRoot, tt.project)
			assert.Equal(t, tt.expectedVersion, version)
		})
	}
}

func TestReplaceAllPlaceholders(t *testing.T) {
	tests := []struct {
		name    string
		project mavenProject
		input   string
		output  string
	}{
		{
			"empty.input",
			mavenProject{
				Properties: Properties{
					Entries: []Property{
						{
							XMLName: xml.Name{
								Local: "version.spring-boot_2.x",
							},
							Value: "2.x",
						},
					},
				},
			},
			"",
			"",
		},
		{
			"empty.properties",
			mavenProject{
				Properties: Properties{
					Entries: []Property{},
				},
			},
			"org.springframework.boot:spring-boot-dependencies:${version.spring-boot_2.x}",
			"org.springframework.boot:spring-boot-dependencies:${version.spring-boot_2.x}",
		},
		{
			"dependency.version",
			mavenProject{
				Properties: Properties{
					Entries: []Property{
						{
							XMLName: xml.Name{
								Local: "version.spring-boot_2.x",
							},
							Value: "2.x",
						},
					},
				},
			},
			"org.springframework.boot:spring-boot-dependencies:${version.spring-boot_2.x}",
			"org.springframework.boot:spring-boot-dependencies:2.x",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := replaceAllPlaceholders(tt.project, tt.input)
			assert.Equal(t, tt.output, output)
		})
	}
}
