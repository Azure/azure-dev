package appdetect

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCreateEffectivePom(t *testing.T) {
	tests := []struct {
		name     string
		testPoms []testPom
		expected []dependency
	}{
		{
			name: "Test with two dependencies",
			testPoms: []testPom{
				{
					pomFilePath: "pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project</artifactId>
							<version>1.0.0</version>
							<dependencies>
								<dependency>
									<groupId>org.springframework</groupId>
									<artifactId>spring-core</artifactId>
									<version>5.3.8</version>
									<scope>compile</scope>
								</dependency>
								<dependency>
									<groupId>junit</groupId>
									<artifactId>junit</artifactId>
									<version>4.13.2</version>
									<scope>test</scope>
								</dependency>
							</dependencies>
						</project>
						`,
				},
			},
			expected: []dependency{
				{
					GroupId:    "org.springframework",
					ArtifactId: "spring-core",
					Version:    "5.3.8",
					Scope:      "compile",
				},
				{
					GroupId:    "junit",
					ArtifactId: "junit",
					Version:    "4.13.2",
					Scope:      "test",
				},
			},
		},
		{
			name: "Test with no dependencies",
			testPoms: []testPom{
				{
					pomFilePath: "pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project</artifactId>
							<version>1.0.0</version>
							<dependencies>
							</dependencies>
						</project>
						`,
				},
			},
			expected: []dependency{},
		},
		{
			name: "Test with one dependency which version is decided by dependencyManagement",
			testPoms: []testPom{
				{
					pomFilePath: "pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project</artifactId>
							<version>1.0.0</version>
							<dependencies>
								<dependency>
									<groupId>org.slf4j</groupId>
									<artifactId>slf4j-api</artifactId>
								</dependency>
							</dependencies>
							<dependencyManagement>
								<dependencies>
									<dependency>
										<groupId>org.springframework.boot</groupId>
										<artifactId>spring-boot-dependencies</artifactId>
										<version>3.0.0</version>
										<type>pom</type>
										<scope>import</scope>
									</dependency>
								</dependencies>
							</dependencyManagement>
						</project>
						`,
				},
			},
			expected: []dependency{
				{
					GroupId:    "org.slf4j",
					ArtifactId: "slf4j-api",
					Version:    "2.0.4",
					Scope:      "compile",
				},
			},
		},
		{
			name: "Test with one dependency which version is decided by parent",
			testPoms: []testPom{
				{
					pomFilePath: "pom.xml",
					pomContentString: `
						<project>
							<parent>
								<groupId>org.springframework.boot</groupId>
								<artifactId>spring-boot-starter-parent</artifactId>
								<version>3.0.0</version>
								<relativePath/> <!-- lookup parent from repository -->
							</parent>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project</artifactId>
							<version>1.0.0</version>
							<dependencies>
								<dependency>
									<groupId>org.slf4j</groupId>
									<artifactId>slf4j-api</artifactId>
								</dependency>
							</dependencies>
						</project>
						`,
				},
			},
			expected: []dependency{
				{
					GroupId:    "org.slf4j",
					ArtifactId: "slf4j-api",
					Version:    "2.0.4",
					Scope:      "compile",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workingDir, err := prepareTestPomFiles(tt.testPoms)
			if err != nil {
				t.Fatalf("%v", err)
			}
			for _, testPom := range tt.testPoms {
				pomFilePath := filepath.Join(workingDir, testPom.pomFilePath)

				effectivePom, err := createEffectivePom(pomFilePath)
				if err != nil {
					t.Fatalf("createEffectivePom failed: %v", err)
				}

				if len(effectivePom.Dependencies) != len(tt.expected) {
					t.Fatalf("Expected: %d\nActual: %d", len(tt.expected), len(effectivePom.Dependencies))
				}

				for i, dep := range effectivePom.Dependencies {
					if dep != tt.expected[i] {
						t.Errorf("\nExpected: %s\nActual:   %s", tt.expected[i], dep)
					}
				}
			}
		})
	}
}

func TestCreatePropertyMapAccordingToProjectProperty(t *testing.T) {
	tests := []struct {
		name      string
		pomString string
		expected  map[string]string
	}{
		{
			name: "Test createPropertyMapAccordingToProjectProperty",
			pomString: `
				<project>
					<modelVersion>4.0.0</modelVersion>
					<groupId>com.example</groupId>
					<artifactId>example-project</artifactId>
					<version>1.0.0</version>
					<properties>
						<version.spring.boot>3.3.5</version.spring.boot>
						<version.spring.cloud>2023.0.3</version.spring.cloud>
						<version.spring.cloud.azure>5.18.0</version.spring.cloud.azure>
					</properties>
				</project>
				`,
			expected: map[string]string{
				"version.spring.boot":        "3.3.5",
				"version.spring.cloud":       "2023.0.3",
				"version.spring.cloud.azure": "5.18.0",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pom, err := unmarshalPomFromString(tt.pomString)
			if err != nil {
				t.Fatalf("Failed to unmarshal string: %v", err)
			}
			createPropertyMapAccordingToProjectProperty(&pom)
			if !reflect.DeepEqual(pom.propertyMap, tt.expected) {
				t.Fatalf("\nExpected: %s\nActual:   %s", tt.expected, pom.propertyMap)
			}
		})
	}
}

func TestReplacePropertyPlaceHolder(t *testing.T) {
	var tests = []struct {
		name     string
		inputPom pom
		expected pom
	}{
		{
			name: "Test replacePropertyPlaceHolder",
			inputPom: pom{
				GroupId:    "sampleGroupId",
				ArtifactId: "sampleArtifactId",
				Version:    "1.0.0",
				DependencyManagement: dependencyManagement{
					Dependencies: []dependency{
						{
							GroupId:    "groupIdOne",
							ArtifactId: "artifactIdOne",
							Version:    "${version.spring.boot}",
							Scope:      DependencyScopeCompile,
						},
					},
				},
				Dependencies: []dependency{
					{
						GroupId:    "groupIdTwo",
						ArtifactId: "artifactIdTwo",
						Version:    "${version.spring.cloud}",
						Scope:      DependencyScopeCompile,
					},
					{
						GroupId:    "${project.groupId}",
						ArtifactId: "artifactIdThree",
						Version:    "${project.version}",
						Scope:      DependencyScopeCompile,
					},
				},
				Build: build{
					Plugins: []plugin{
						{
							GroupId:    "groupIdFour",
							ArtifactId: "artifactIdFour",
							Version:    "${version.spring.cloud.azure}",
						},
					},
				},
				propertyMap: map[string]string{
					"version.spring.boot":        "3.3.5",
					"version.spring.cloud":       "2023.0.3",
					"version.spring.cloud.azure": "5.18.0",
					"another.property":           "${version.spring.cloud.azure}",
				},
				dependencyManagementMap: map[string]string{
					"groupIdOne:artifactIdOne:compile": "${version.spring.boot}",
				},
			},
			expected: pom{
				GroupId:    "sampleGroupId",
				ArtifactId: "sampleArtifactId",
				Version:    "1.0.0",
				DependencyManagement: dependencyManagement{
					Dependencies: []dependency{
						{
							GroupId:    "groupIdOne",
							ArtifactId: "artifactIdOne",
							Version:    "3.3.5",
							Scope:      DependencyScopeCompile,
						},
					},
				},
				Dependencies: []dependency{
					{
						GroupId:    "groupIdTwo",
						ArtifactId: "artifactIdTwo",
						Version:    "2023.0.3",
						Scope:      DependencyScopeCompile,
					},
					{
						GroupId:    "sampleGroupId",
						ArtifactId: "artifactIdThree",
						Version:    "1.0.0",
						Scope:      DependencyScopeCompile,
					},
				},
				Build: build{
					Plugins: []plugin{
						{
							GroupId:    "groupIdFour",
							ArtifactId: "artifactIdFour",
							Version:    "5.18.0",
						},
					},
				},
				propertyMap: map[string]string{
					"version.spring.boot":        "3.3.5",
					"version.spring.cloud":       "2023.0.3",
					"version.spring.cloud.azure": "5.18.0",
					"another.property":           "5.18.0",
					"project.groupId":            "sampleGroupId",
					"project.version":            "1.0.0",
				},
				dependencyManagementMap: map[string]string{
					"groupIdOne:artifactIdOne:compile": "3.3.5",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addCommonPropertiesLikeProjectGroupIdAndProjectVersionToPropertyMap(&tt.inputPom)
			replacePropertyPlaceHolderInPropertyMap(&tt.inputPom)
			replacePropertyPlaceHolderInGroupId(&tt.inputPom)
			createDependencyManagementMap(&tt.inputPom)
			replacePropertyPlaceHolderInVersion(&tt.inputPom)
			if !reflect.DeepEqual(tt.inputPom, tt.expected) {
				t.Fatalf("\nExpected: %s\nActual:   %s", tt.expected, tt.inputPom)
			}
		})
	}
}

func TestCreateDependencyManagementMap(t *testing.T) {
	var tests = []struct {
		name     string
		inputPom pom
		expected pom
	}{
		{
			name: "Test createDependencyManagementMap",
			inputPom: pom{
				DependencyManagement: dependencyManagement{
					Dependencies: []dependency{
						{
							GroupId:    "groupIdOne",
							ArtifactId: "artifactIdOne",
							Version:    "1.0.0",
							Scope:      DependencyScopeCompile,
						},
					},
				},
				Dependencies: []dependency{
					{
						GroupId:    "groupIdOne",
						ArtifactId: "artifactIdOne",
						Scope:      DependencyScopeCompile,
					},
				},
			},
			expected: pom{
				DependencyManagement: dependencyManagement{
					Dependencies: []dependency{
						{
							GroupId:    "groupIdOne",
							ArtifactId: "artifactIdOne",
							Version:    "1.0.0",
							Scope:      DependencyScopeCompile,
						},
					},
				},
				Dependencies: []dependency{
					{
						GroupId:    "groupIdOne",
						ArtifactId: "artifactIdOne",
						Scope:      DependencyScopeCompile,
					},
				},
				dependencyManagementMap: map[string]string{
					"groupIdOne:artifactIdOne:compile": "1.0.0",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			createDependencyManagementMap(&tt.inputPom)
			if !reflect.DeepEqual(tt.inputPom, tt.expected) {
				t.Fatalf("\nExpected: %s\nActual:   %s", tt.expected, tt.inputPom)
			}
		})
	}
}

func TestUpdateDependencyVersionAccordingToDependencyManagement(t *testing.T) {
	var tests = []struct {
		name     string
		inputPom pom
		expected pom
	}{
		{
			name: "Test updateDependencyVersionAccordingToDependencyManagement",
			inputPom: pom{
				Dependencies: []dependency{
					{
						GroupId:    "groupIdOne",
						ArtifactId: "artifactIdOne",
						Scope:      DependencyScopeCompile,
					},
				},
				dependencyManagementMap: map[string]string{
					"groupIdOne:artifactIdOne:compile": "1.0.0",
				},
			},
			expected: pom{
				Dependencies: []dependency{
					{
						GroupId:    "groupIdOne",
						ArtifactId: "artifactIdOne",
						Version:    "1.0.0",
						Scope:      DependencyScopeCompile,
					},
				},
				dependencyManagementMap: map[string]string{
					"groupIdOne:artifactIdOne:compile": "1.0.0",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updateDependencyVersionAccordingToDependencyManagement(&tt.inputPom)
			if !reflect.DeepEqual(tt.inputPom, tt.expected) {
				t.Fatalf("\nExpected: %s\nActual:   %s", tt.expected, tt.inputPom)
			}
		})
	}
}

func TestGetRemoteMavenRepositoryUrl(t *testing.T) {
	var tests = []struct {
		name       string
		groupId    string
		artifactId string
		version    string
		expected   string
	}{
		{
			name:       "spring-boot-starter-parent",
			groupId:    "org.springframework.boot",
			artifactId: "spring-boot-starter-parent",
			version:    "3.4.0",
			expected: "https://repo.maven.apache.org/maven2/org/springframework/boot/spring-boot-starter-parent/3.4.0/" +
				"spring-boot-starter-parent-3.4.0.pom",
		},
		{
			name:       "spring-boot-dependencies",
			groupId:    "org.springframework.boot",
			artifactId: "spring-boot-dependencies",
			version:    "3.4.0",
			expected: "https://repo.maven.apache.org/maven2/org/springframework/boot/spring-boot-dependencies/3.4.0/" +
				"spring-boot-dependencies-3.4.0.pom",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := getRemoteMavenRepositoryUrl(tt.groupId, tt.artifactId, tt.version)
			if !reflect.DeepEqual(actual, tt.expected) {
				t.Fatalf("\nExpected: %s\nActual:   %s", tt.expected, actual)
			}
		})
	}
}

func TestGetSimulatedEffectivePomFromRemoteMavenRepository(t *testing.T) {
	var tests = []struct {
		name       string
		groupId    string
		artifactId string
		version    string
		expected   int
	}{
		{
			name:       "spring-boot-starter-parent",
			groupId:    "org.springframework.boot",
			artifactId: "spring-boot-starter-parent",
			version:    "3.4.0",
			expected:   1496,
		},
		{
			name:       "spring-boot-dependencies",
			groupId:    "org.springframework.boot",
			artifactId: "spring-boot-dependencies",
			version:    "3.4.0",
			expected:   1496,
		},
		{
			name:       "kotlin-bom",
			groupId:    "org.jetbrains.kotlin",
			artifactId: "kotlin-bom",
			version:    "1.9.25",
			expected:   23,
		},
		{
			name:       "infinispan-bom",
			groupId:    "org.infinispan",
			artifactId: "infinispan-bom",
			version:    "15.0.11.Final",
			expected:   65,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pom, err := getSimulatedEffectivePomFromRemoteMavenRepository(tt.groupId, tt.artifactId, tt.version)
			if err != nil {
				t.Fatalf("Failed to create temp directory: %v", err)
			}
			for _, value := range pom.dependencyManagementMap {
				if isVariable(value) {
					t.Fatalf("Unresolved property: value = %s", value)
				}
			}
			actual := len(pom.dependencyManagementMap)
			if !reflect.DeepEqual(actual, tt.expected) {
				t.Fatalf("\nExpected: %d\nActual:   %d", tt.expected, actual)
			}
		})
	}
}

func TestMakePathFitCurrentOs(t *testing.T) {
	var tests = []struct {
		name  string
		input string
	}{
		{
			name:  "linux",
			input: "/home/user/example/file.txt",
		},
		{
			name:  "windows",
			input: "C:\\Users\\example\\Work",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := makePathFitCurrentOs(tt.input)
			strings.Contains(actual, string(os.PathSeparator))
		})
	}
}

func TestGetParentPomFilePath(t *testing.T) {
	var tests = []struct {
		name     string
		input    pom
		expected string
	}{
		{
			name: "relativePath not set",
			input: pom{
				pomFilePath: "/home/user/example-user/" +
					"example-project-grandparent/example-project-parent/example-project-module-one/pom.xml",
			},
			expected: makePathFitCurrentOs("/home/user/example-user/" +
				"example-project-grandparent/example-project-parent/pom.xml"),
		},
		{
			name: "relativePath set to grandparent folder",
			input: pom{
				pomFilePath: "/home/user/example-user/" +
					"example-project-grandparent/example-project-parent/example-project-module-one/pom.xml",
				Parent: parent{
					RelativePath: "../../pom.xml",
				},
			},
			expected: makePathFitCurrentOs("/home/user/example-user/example-project-grandparent/pom.xml"),
		},
		{
			name: "relativePath set to another file name",
			input: pom{
				pomFilePath: "/home/user/example-user/" +
					"example-project-grandparent/example-project-parent/example-project-module-one/pom.xml",
				Parent: parent{
					RelativePath: "../another-pom.xml",
				},
			},
			expected: makePathFitCurrentOs("/home/user/example-user/" +
				"example-project-grandparent/example-project-parent/another-pom.xml"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := getParentPomFilePath(tt.input)
			if !reflect.DeepEqual(actual, tt.expected) {
				t.Fatalf("\nExpected: %s\nActual:   %s", tt.expected, actual)
			}
		})
	}
}

func TestAbsorbPropertyMap(t *testing.T) {
	var tests = []struct {
		name            string
		input           pom
		toBeAbsorbedPom pom
		expected        pom
	}{
		{
			name: "relativePath not set",
			input: pom{
				GroupId:    "sampleGroupId",
				ArtifactId: "sampleArtifactId",
				Version:    "1.0.0",
				DependencyManagement: dependencyManagement{
					Dependencies: []dependency{
						{
							GroupId:    "groupIdOne",
							ArtifactId: "artifactIdOne",
							Version:    "${version.spring.boot}",
							Scope:      DependencyScopeCompile,
						},
					},
				},
				Dependencies: []dependency{
					{
						GroupId:    "groupIdTwo",
						ArtifactId: "artifactIdTwo",
						Version:    "${version.spring.cloud}",
						Scope:      DependencyScopeCompile,
					},
					{
						GroupId:    "groupIdThree",
						ArtifactId: "artifactIdThree",
						Version:    "${another.property}",
						Scope:      DependencyScopeCompile,
					},
				},
				Build: build{
					Plugins: []plugin{
						{
							GroupId:    "groupIdFour",
							ArtifactId: "artifactIdFour",
							Version:    "${version.spring.cloud.azure}",
						},
					},
				},
				propertyMap: map[string]string{
					"another.property": "${version.spring.cloud.azure}",
				},
				dependencyManagementMap: map[string]string{
					"groupIdOne:artifactIdOne:compile": "${version.spring.boot}",
				},
			},
			toBeAbsorbedPom: pom{
				GroupId:    "sampleGroupId",
				ArtifactId: "sampleArtifactIdToBeAbsorbed",
				Version:    "1.0.0",
				propertyMap: map[string]string{
					"version.spring.boot":        "3.3.5",
					"version.spring.cloud":       "2023.0.3",
					"version.spring.cloud.azure": "5.18.0",
				},
			},
			expected: pom{
				GroupId:    "sampleGroupId",
				ArtifactId: "sampleArtifactId",
				Version:    "1.0.0",
				DependencyManagement: dependencyManagement{
					Dependencies: []dependency{
						{
							GroupId:    "groupIdOne",
							ArtifactId: "artifactIdOne",
							Version:    "3.3.5",
							Scope:      DependencyScopeCompile,
						},
					},
				},
				Dependencies: []dependency{
					{
						GroupId:    "groupIdTwo",
						ArtifactId: "artifactIdTwo",
						Version:    "2023.0.3",
						Scope:      DependencyScopeCompile,
					},
					{
						GroupId:    "groupIdThree",
						ArtifactId: "artifactIdThree",
						Version:    "5.18.0",
						Scope:      DependencyScopeCompile,
					},
				},
				Build: build{
					Plugins: []plugin{
						{
							GroupId:    "groupIdFour",
							ArtifactId: "artifactIdFour",
							Version:    "5.18.0",
						},
					},
				},
				propertyMap: map[string]string{
					"version.spring.boot":        "3.3.5",
					"version.spring.cloud":       "2023.0.3",
					"version.spring.cloud.azure": "5.18.0",
					"another.property":           "5.18.0",
				},
				dependencyManagementMap: map[string]string{
					"groupIdOne:artifactIdOne:compile": "3.3.5",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			absorbPropertyMap(&tt.input, tt.toBeAbsorbedPom.propertyMap, false)
			if !reflect.DeepEqual(tt.input, tt.expected) {
				t.Fatalf("\nExpected: %s\nActual:   %s", tt.expected, tt.input)
			}
		})
	}
}

func TestAbsorbDependencyManagement(t *testing.T) {
	var tests = []struct {
		name            string
		input           pom
		toBeAbsorbedPom pom
		expected        pom
	}{
		{
			name: "test absorbDependencyManagement",
			input: pom{
				GroupId:    "sampleGroupId",
				ArtifactId: "sampleArtifactId",
				Version:    "1.0.0",
				Dependencies: []dependency{
					{
						GroupId:    "groupIdOne",
						ArtifactId: "artifactIdOne",
						Scope:      "compile",
					},
				},
				dependencyManagementMap: map[string]string{},
			},
			toBeAbsorbedPom: pom{
				GroupId:    "sampleGroupId",
				ArtifactId: "sampleArtifactIdToBeAbsorbed",
				Version:    "1.0.0",
				DependencyManagement: dependencyManagement{
					Dependencies: []dependency{
						{
							GroupId:    "groupIdOne",
							ArtifactId: "artifactIdOne",
							Version:    "1.0.0",
							Scope:      "compile",
						},
					},
				},
				dependencyManagementMap: map[string]string{
					"groupIdOne:artifactIdOne:compile": "1.0.0",
				},
			},
			expected: pom{
				GroupId:    "sampleGroupId",
				ArtifactId: "sampleArtifactId",
				Version:    "1.0.0",
				DependencyManagement: dependencyManagement{
					Dependencies: []dependency{
						{
							GroupId:    "groupIdOne",
							ArtifactId: "artifactIdOne",
							Version:    "1.0.0",
							Scope:      "compile",
						},
					},
				},
				Dependencies: []dependency{
					{
						GroupId:    "groupIdOne",
						ArtifactId: "artifactIdOne",
						Scope:      "compile",
					},
				},
				dependencyManagementMap: map[string]string{
					"groupIdOne:artifactIdOne:compile": "1.0.0",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			absorbDependencyManagement(&tt.input, tt.toBeAbsorbedPom.dependencyManagementMap, false)
			if !reflect.DeepEqual(tt.input, tt.expected) {
				t.Fatalf("\nExpected: %s\nActual:   %s", tt.expected, tt.input)
			}
		})
	}
}

func TestAbsorbDependency(t *testing.T) {
	var tests = []struct {
		name            string
		input           pom
		toBeAbsorbedPom pom
		expected        pom
	}{
		{
			name: "absorb 2 dependencies",
			input: pom{
				GroupId:      "sampleGroupId",
				ArtifactId:   "sampleArtifactId",
				Version:      "1.0.0",
				Dependencies: []dependency{},
			},
			toBeAbsorbedPom: pom{
				GroupId:    "sampleGroupId",
				ArtifactId: "sampleArtifactIdToBeAbsorbed",
				Version:    "1.0.0",
				Dependencies: []dependency{
					{
						GroupId:    "groupIdOne",
						ArtifactId: "artifactIdOne",
						Version:    "1.0.0",
						Scope:      "compile",
					},
					{
						GroupId:    "groupIdTwo",
						ArtifactId: "artifactIdTwo",
						Version:    "1.0.0",
						Scope:      "test",
					},
				},
			},
			expected: pom{
				GroupId:    "sampleGroupId",
				ArtifactId: "sampleArtifactId",
				Version:    "1.0.0",
				Dependencies: []dependency{
					{
						GroupId:    "groupIdOne",
						ArtifactId: "artifactIdOne",
						Version:    "1.0.0",
						Scope:      "compile",
					},
					{
						GroupId:    "groupIdTwo",
						ArtifactId: "artifactIdTwo",
						Version:    "1.0.0",
						Scope:      "test",
					},
				},
			},
		},
		{
			name: "absorb 1 dependency and skip 1 dependency",
			input: pom{
				GroupId:    "sampleGroupId",
				ArtifactId: "sampleArtifactId",
				Version:    "1.0.0",
				Dependencies: []dependency{
					{
						GroupId:    "groupIdOne",
						ArtifactId: "artifactIdOne",
						Version:    "2.0.0",
						Scope:      "compile",
					},
				},
			},
			toBeAbsorbedPom: pom{
				GroupId:    "sampleGroupId",
				ArtifactId: "sampleArtifactIdToBeAbsorbed",
				Version:    "1.0.0",
				Dependencies: []dependency{
					{
						GroupId:    "groupIdOne",
						ArtifactId: "artifactIdOne",
						Version:    "1.0.0",
						Scope:      "compile",
					},
					{
						GroupId:    "groupIdTwo",
						ArtifactId: "artifactIdTwo",
						Version:    "1.0.0",
						Scope:      "test",
					},
				},
			},
			expected: pom{
				GroupId:    "sampleGroupId",
				ArtifactId: "sampleArtifactId",
				Version:    "1.0.0",
				Dependencies: []dependency{
					{
						GroupId:    "groupIdOne",
						ArtifactId: "artifactIdOne",
						Version:    "2.0.0", // keep original value
						Scope:      "compile",
					},
					{
						GroupId:    "groupIdTwo",
						ArtifactId: "artifactIdTwo",
						Version:    "1.0.0",
						Scope:      "test",
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			absorbDependencies(&tt.input, tt.toBeAbsorbedPom.Dependencies)
			if !reflect.DeepEqual(tt.input, tt.expected) {
				t.Fatalf("\nExpected: %s\nActual:   %s", tt.expected, tt.input)
			}
		})
	}
}

func TestAbsorbBuildPlugin(t *testing.T) {
	var tests = []struct {
		name            string
		input           pom
		toBeAbsorbedPom pom
		expected        pom
	}{
		{
			name: "absorb 2 plugins",
			input: pom{
				GroupId:    "sampleGroupId",
				ArtifactId: "sampleArtifactId",
				Version:    "1.0.0",
			},
			toBeAbsorbedPom: pom{
				GroupId:    "sampleGroupId",
				ArtifactId: "sampleArtifactIdToBeAbsorbed",
				Version:    "1.0.0",
				Build: build{
					Plugins: []plugin{
						{
							GroupId:    "groupIdOne",
							ArtifactId: "artifactIdOne",
							Version:    "1.0.0",
						},
						{
							GroupId:    "groupIdTwo",
							ArtifactId: "artifactIdTwo",
							Version:    "1.0.0",
						},
					},
				},
			},
			expected: pom{
				GroupId:    "sampleGroupId",
				ArtifactId: "sampleArtifactId",
				Version:    "1.0.0",
				Build: build{
					Plugins: []plugin{
						{
							GroupId:    "groupIdOne",
							ArtifactId: "artifactIdOne",
							Version:    "1.0.0",
						},
						{
							GroupId:    "groupIdTwo",
							ArtifactId: "artifactIdTwo",
							Version:    "1.0.0",
						},
					},
				},
			},
		},
		{
			name: "absorb 1 plugin and skip 1 plugin",
			input: pom{
				GroupId:    "sampleGroupId",
				ArtifactId: "sampleArtifactId",
				Version:    "1.0.0",
				Build: build{
					Plugins: []plugin{
						{
							GroupId:    "groupIdOne",
							ArtifactId: "artifactIdOne",
							Version:    "2.0.0",
						},
					},
				},
			},
			toBeAbsorbedPom: pom{
				GroupId:    "sampleGroupId",
				ArtifactId: "sampleArtifactIdToBeAbsorbed",
				Version:    "1.0.0",
				Build: build{
					Plugins: []plugin{
						{
							GroupId:    "groupIdOne",
							ArtifactId: "artifactIdOne",
							Version:    "1.0.0",
						},
						{
							GroupId:    "groupIdTwo",
							ArtifactId: "artifactIdTwo",
							Version:    "1.0.0",
						},
					},
				},
			},
			expected: pom{
				GroupId:    "sampleGroupId",
				ArtifactId: "sampleArtifactId",
				Version:    "1.0.0",
				Build: build{
					Plugins: []plugin{
						{
							GroupId:    "groupIdOne",
							ArtifactId: "artifactIdOne",
							Version:    "2.0.0", // keep original value
						},
						{
							GroupId:    "groupIdTwo",
							ArtifactId: "artifactIdTwo",
							Version:    "1.0.0",
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			absorbBuildPlugins(&tt.input, tt.toBeAbsorbedPom.Build.Plugins)
			if !reflect.DeepEqual(tt.input, tt.expected) {
				t.Fatalf("\nExpected: %s\nActual:   %s", tt.expected, tt.input)
			}
		})
	}
}

func TestCreateSimulatedEffectivePom(t *testing.T) {
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		t.Skip("Skip TestCreateSimulatedEffectivePom in GitHub Actions because it will time out.")
	}
	var tests = []struct {
		name     string
		testPoms []testPom
	}{
		{
			name: "No parent",
			testPoms: []testPom{
				{
					pomFilePath: "pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project</artifactId>
							<version>1.0.0</version>
							<dependencies>
								<dependency>
									<groupId>org.springframework</groupId>
									<artifactId>spring-core</artifactId>
									<version>5.3.8</version>
									<scope>compile</scope>
								</dependency>
								<dependency>
									<groupId>junit</groupId>
									<artifactId>junit</artifactId>
									<version>4.13.2</version>
									<scope>test</scope>
								</dependency>
							</dependencies>
						</project>
						`,
				},
			},
		},
		{
			name: "Self-defined parent",
			testPoms: []testPom{
				{
					pomFilePath: "./pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project-parent</artifactId>
							<version>1.0.0</version>
							<packaging>pom</packaging>
							<dependencyManagement>
								<dependencies>
									<dependency>
										<groupId>org.springframework</groupId>
										<artifactId>spring-core</artifactId>
										<version>5.3.8</version>
										<scope>compile</scope>
									</dependency>
									<dependency>
										<groupId>junit</groupId>
										<artifactId>junit</artifactId>
										<version>4.13.2</version>
										<scope>test</scope>
									</dependency>
								</dependencies>
							</dependencyManagement>
						</project>
						`,
				},
				{
					pomFilePath: "./module-one/pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project-module-one</artifactId>
							<version>1.0.0</version>
							<parent>
								<groupId>com.example</groupId>
								<artifactId>example-project-parent</artifactId>
								<version>1.0.0</version>
							    <relativePath>../pom.xml</relativePath>
							</parent>
							<dependencies>
								<dependency>
									<groupId>org.springframework</groupId>
									<artifactId>spring-core</artifactId>
									<scope>compile</scope>
								</dependency>
								<dependency>
									<groupId>junit</groupId>
									<artifactId>junit</artifactId>
									<scope>test</scope>
								</dependency>
							</dependencies>
						</project>
						`,
				},
			},
		},
		{
			name: "S-defined parent in grandparent folder",
			testPoms: []testPom{
				{
					pomFilePath: "./pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project-parent</artifactId>
							<version>1.0.0</version>
							<packaging>pom</packaging>
							<dependencyManagement>
								<dependencies>
									<dependency>
										<groupId>org.springframework</groupId>
										<artifactId>spring-core</artifactId>
										<version>5.3.8</version>
										<scope>compile</scope>
									</dependency>
									<dependency>
										<groupId>junit</groupId>
										<artifactId>junit</artifactId>
										<version>4.13.2</version>
										<scope>test</scope>
									</dependency>
								</dependencies>
							</dependencyManagement>
						</project>
						`,
				},
				{
					pomFilePath: "./modules/module-one/pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project-module-one</artifactId>
							<version>1.0.0</version>
							<parent>
								<groupId>com.example</groupId>
								<artifactId>example-project-parent</artifactId>
								<version>1.0.0</version>
							    <relativePath>../../pom.xml</relativePath>
							</parent>
							<dependencies>
								<dependency>
									<groupId>org.springframework</groupId>
									<artifactId>spring-core</artifactId>
									<scope>compile</scope>
								</dependency>
								<dependency>
									<groupId>junit</groupId>
									<artifactId>junit</artifactId>
									<scope>test</scope>
								</dependency>
							</dependencies>
						</project>
						`,
				},
			},
		},
		{
			name: "Set spring-boot-starter-parent as parent",
			testPoms: []testPom{
				{
					pomFilePath: "./pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project-grandparent</artifactId>
							<version>1.0.0</version>
							<packaging>pom</packaging>
							<parent>
								<groupId>org.springframework.boot</groupId>
								<artifactId>spring-boot-starter-parent</artifactId>
								<version>3.0.0</version>
								<relativePath/> <!-- lookup parent from repository -->
							</parent>
							<dependencies>
								<dependency>
									<groupId>org.springframework</groupId>
									<artifactId>spring-core</artifactId>
									<scope>compile</scope>
								</dependency>
								<dependency>
									<groupId>junit</groupId>
									<artifactId>junit</artifactId>
									<scope>test</scope>
								</dependency>
							</dependencies>
						</project>
						`,
				},
			},
		},
		{
			name: "Set spring-boot-starter-parent as grandparent's parent",
			testPoms: []testPom{
				{
					pomFilePath: "./pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project-grandparent</artifactId>
							<version>1.0.0</version>
							<packaging>pom</packaging>
							<parent>
								<groupId>org.springframework.boot</groupId>
								<artifactId>spring-boot-starter-parent</artifactId>
								<version>3.0.0</version>
								<relativePath/> <!-- lookup parent from repository -->
							</parent>
						</project>
						`,
				},
				{
					pomFilePath: "./modules/pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project-parent</artifactId>
							<version>1.0.0</version>
							<packaging>pom</packaging>
							<parent>
								<groupId>com.example</groupId>
								<artifactId>example-project-grandparent</artifactId>
								<version>1.0.0</version>
							    <relativePath>../pom.xml</relativePath>
							</parent>
						</project>
						`,
				},
				{
					pomFilePath: "./modules/module-one/pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project-module-one</artifactId>
							<version>1.0.0</version>
							<parent>
								<groupId>com.example</groupId>
								<artifactId>example-project-parent</artifactId>
								<version>1.0.0</version>
							    <relativePath>../pom.xml</relativePath>
							</parent>
							<dependencies>
								<dependency>
									<groupId>org.springframework</groupId>
									<artifactId>spring-core</artifactId>
									<scope>compile</scope>
								</dependency>
								<dependency>
									<groupId>junit</groupId>
									<artifactId>junit</artifactId>
									<scope>test</scope>
								</dependency>
							</dependencies>
						</project>
						`,
				},
			},
		},
		{
			name: "Import spring-boot-dependencies in grandparent",
			testPoms: []testPom{
				{
					pomFilePath: "./pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project-grandparent</artifactId>
							<version>1.0.0</version>
							<packaging>pom</packaging>
							<dependencyManagement>
								<dependencies>
									<dependency>
										<groupId>org.springframework.boot</groupId>
										<artifactId>spring-boot-dependencies</artifactId>
										<version>3.0.0</version>
										<type>pom</type>
										<scope>import</scope>
									</dependency>
								</dependencies>
							</dependencyManagement>
						</project>
						`,
				},
				{
					pomFilePath: "./modules/pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project-parent</artifactId>
							<version>1.0.0</version>
							<packaging>pom</packaging>
							<parent>
								<groupId>com.example</groupId>
								<artifactId>example-project-grandparent</artifactId>
								<version>1.0.0</version>
							    <relativePath>../pom.xml</relativePath>
							</parent>
						</project>
						`,
				},
				{
					pomFilePath: "./modules/module-one/pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project-module-one</artifactId>
							<version>1.0.0</version>
							<parent>
								<groupId>com.example</groupId>
								<artifactId>example-project-parent</artifactId>
								<version>1.0.0</version>
							    <relativePath>../pom.xml</relativePath>
							</parent>
							<dependencies>
								<dependency>
									<groupId>org.springframework</groupId>
									<artifactId>spring-core</artifactId>
									<scope>compile</scope>
								</dependency>
								<dependency>
									<groupId>junit</groupId>
									<artifactId>junit</artifactId>
									<scope>test</scope>
								</dependency>
							</dependencies>
						</project>
						`,
				},
			},
		},
		{
			name: "Override version in dependencies",
			testPoms: []testPom{
				{
					pomFilePath: "./pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project-grandparent</artifactId>
							<version>1.0.0</version>
							<packaging>pom</packaging>
							<dependencyManagement>
								<dependencies>
									<dependency>
										<groupId>org.springframework.boot</groupId>
										<artifactId>spring-boot-dependencies</artifactId>
										<version>3.0.0</version>
										<type>pom</type>
										<scope>import</scope>
									</dependency>
								</dependencies>
							</dependencyManagement>
						</project>
						`,
				},
				{
					pomFilePath: "./modules/pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project-parent</artifactId>
							<version>1.0.0</version>
							<packaging>pom</packaging>
							<parent>
								<groupId>com.example</groupId>
								<artifactId>example-project-grandparent</artifactId>
								<version>1.0.0</version>
							    <relativePath>../pom.xml</relativePath>
							</parent>
						</project>
						`,
				},
				{
					pomFilePath: "./modules/module-one/pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project-module-one</artifactId>
							<version>1.0.0</version>
							<parent>
								<groupId>com.example</groupId>
								<artifactId>example-project-parent</artifactId>
								<version>1.0.0</version>
							    <relativePath>../pom.xml</relativePath>
							</parent>
							<dependencies>
								<dependency>
									<groupId>org.springframework</groupId>
									<artifactId>spring-core</artifactId>
									<scope>compile</scope>
								</dependency>
								<dependency>
									<groupId>junit</groupId>
									<artifactId>junit</artifactId>
									<version>4.13.0</version>
									<scope>test</scope>
								</dependency>
							</dependencies>
						</project>
						`,
				},
			},
		},
		{
			name: "Override version in dependencyManagement",
			testPoms: []testPom{
				{
					pomFilePath: "./pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project-grandparent</artifactId>
							<version>1.0.0</version>
							<packaging>pom</packaging>
							<dependencyManagement>
								<dependencies>
									<dependency>
										<groupId>org.springframework.boot</groupId>
										<artifactId>spring-boot-dependencies</artifactId>
										<version>3.0.0</version>
										<type>pom</type>
										<scope>import</scope>
									</dependency>
								</dependencies>
							</dependencyManagement>
						</project>
						`,
				},
				{
					pomFilePath: "./modules/pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project-parent</artifactId>
							<version>1.0.0</version>
							<packaging>pom</packaging>
							<parent>
								<groupId>com.example</groupId>
								<artifactId>example-project-grandparent</artifactId>
								<version>1.0.0</version>
							    <relativePath>../pom.xml</relativePath>
							</parent>
						</project>
						`,
				},
				{
					pomFilePath: "./modules/module-one/pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project-module-one</artifactId>
							<version>1.0.0</version>
							<parent>
								<groupId>com.example</groupId>
								<artifactId>example-project-parent</artifactId>
								<version>1.0.0</version>
							    <relativePath>../pom.xml</relativePath>
							</parent>
							<dependencyManagement>
								<dependencies>
									<dependency>
										<groupId>junit</groupId>
										<artifactId>junit</artifactId>
										<version>4.13.0</version>
										<scope>test</scope>
									</dependency>
								</dependencies>
							</dependencyManagement>
							<dependencies>
								<dependency>
									<groupId>org.springframework</groupId>
									<artifactId>spring-core</artifactId>
									<scope>compile</scope>
								</dependency>
								<dependency>
									<groupId>junit</groupId>
									<artifactId>junit</artifactId>
									<scope>test</scope>
								</dependency>
							</dependencies>
						</project>
						`,
				},
			},
		},
		{
			name: "Version different in dependencyManagement of grandparent & parent & leaf pom",
			testPoms: []testPom{
				{
					pomFilePath: "./pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project-grandparent</artifactId>
							<version>1.0.0</version>
							<packaging>pom</packaging>
							<dependencyManagement>
								<dependencies>
									<dependency>
										<groupId>org.springframework.boot</groupId>
										<artifactId>spring-boot-dependencies</artifactId>
										<version>3.0.0</version>
										<type>pom</type>
										<scope>import</scope>
									</dependency>
								</dependencies>
							</dependencyManagement>
						</project>
						`,
				},
				{
					pomFilePath: "./modules/pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project-parent</artifactId>
							<version>1.0.0</version>
							<packaging>pom</packaging>
							<parent>
								<groupId>com.example</groupId>
								<artifactId>example-project-grandparent</artifactId>
								<version>1.0.0</version>
							    <relativePath>../pom.xml</relativePath>
							</parent>
							<dependencyManagement>
								<dependencies>
									<dependency>
										<groupId>junit</groupId>
										<artifactId>junit</artifactId>
										<version>4.13.1</version>
										<scope>test</scope>
									</dependency>
								</dependencies>
							</dependencyManagement>
						</project>
						`,
				},
				{
					pomFilePath: "./modules/module-one/pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project-module-one</artifactId>
							<version>1.0.0</version>
							<parent>
								<groupId>com.example</groupId>
								<artifactId>example-project-parent</artifactId>
								<version>1.0.0</version>
							    <relativePath>../pom.xml</relativePath>
							</parent>
							<dependencyManagement>
								<dependencies>
									<dependency>
										<groupId>junit</groupId>
										<artifactId>junit</artifactId>
										<version>4.13.0</version>
										<scope>test</scope>
									</dependency>
								</dependencies>
							</dependencyManagement>
							<dependencies>
								<dependency>
									<groupId>org.springframework</groupId>
									<artifactId>spring-core</artifactId>
									<scope>compile</scope>
								</dependency>
								<dependency>
									<groupId>junit</groupId>
									<artifactId>junit</artifactId>
									<scope>test</scope>
								</dependency>
							</dependencies>
						</project>
						`,
				},
			},
		},
		{
			name: "Scope not set in leaf pom",
			testPoms: []testPom{
				{
					pomFilePath: "./pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project-grandparent</artifactId>
							<version>1.0.0</version>
							<packaging>pom</packaging>
							<dependencyManagement>
								<dependencies>
									<dependency>
										<groupId>org.springframework.boot</groupId>
										<artifactId>spring-boot-dependencies</artifactId>
										<version>3.0.0</version>
										<type>pom</type>
										<scope>import</scope>
									</dependency>
								</dependencies>
							</dependencyManagement>
						</project>
						`,
				},
				{
					pomFilePath: "./modules/pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project-parent</artifactId>
							<version>1.0.0</version>
							<packaging>pom</packaging>
							<parent>
								<groupId>com.example</groupId>
								<artifactId>example-project-grandparent</artifactId>
								<version>1.0.0</version>
							    <relativePath>../pom.xml</relativePath>
							</parent>
						</project>
						`,
				},
				{
					pomFilePath: "./modules/module-one/pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project-module-one</artifactId>
							<version>1.0.0</version>
							<parent>
								<groupId>com.example</groupId>
								<artifactId>example-project-parent</artifactId>
								<version>1.0.0</version>
							    <relativePath>../pom.xml</relativePath>
							</parent>
							<dependencies>
								<dependency>
									<groupId>org.springframework</groupId>
									<artifactId>spring-core</artifactId>
								</dependency>
								<dependency>
									<groupId>junit</groupId>
									<artifactId>junit</artifactId>
									<scope>test</scope>
								</dependency>
							</dependencies>
						</project>
						`,
				},
			},
		},
		{
			name: "Set spring-boot-maven-plugin in grandparent",
			testPoms: []testPom{
				{
					pomFilePath: "./pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project-grandparent</artifactId>
							<version>1.0.0</version>
							<packaging>pom</packaging>
							<properties>
								<version.spring.boot>3.3.5</version.spring.boot>
							</properties>
							<dependencyManagement>
								<dependencies>
									<dependency>
										<groupId>org.springframework.boot</groupId>
										<artifactId>spring-boot-dependencies</artifactId>
										<version>${version.spring.boot}</version>
										<type>pom</type>
										<scope>import</scope>
									</dependency>
								</dependencies>
							</dependencyManagement>
							<build>
								<plugins>
									<plugin>
										<groupId>org.springframework.boot</groupId>
										<artifactId>spring-boot-maven-plugin</artifactId>
										<version>${version.spring.boot}</version>
										<executions>
											<execution>
												<goals>
													<goal>repackage</goal>
												</goals>
											</execution>
										</executions>
									</plugin>
								</plugins>
							</build>
						</project>
						`,
				},
				{
					pomFilePath: "./modules/pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project-parent</artifactId>
							<version>1.0.0</version>
							<packaging>pom</packaging>
							<parent>
								<groupId>com.example</groupId>
								<artifactId>example-project-grandparent</artifactId>
								<version>1.0.0</version>
							    <relativePath>../pom.xml</relativePath>
							</parent>
						</project>
						`,
				},
				{
					pomFilePath: "./modules/module-one/pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project-module-one</artifactId>
							<version>1.0.0</version>
							<parent>
								<groupId>com.example</groupId>
								<artifactId>example-project-parent</artifactId>
								<version>1.0.0</version>
							    <relativePath>../pom.xml</relativePath>
							</parent>
							<dependencies>
								<dependency>
									<groupId>org.springframework</groupId>
									<artifactId>spring-core</artifactId>
								</dependency>
								<dependency>
									<groupId>junit</groupId>
									<artifactId>junit</artifactId>
									<scope>test</scope>
								</dependency>
							</dependencies>
						</project>
						`,
				},
			},
		},
		{
			name: "Set profiles and set activeByDefault = true",
			testPoms: []testPom{
				{
					pomFilePath: "./pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>

							<parent>
								<groupId>org.springframework.boot</groupId>
								<artifactId>spring-boot-starter-parent</artifactId>
								<version>3.2.3</version>
							</parent>

							<groupId>com.example</groupId>
							<artifactId>example-project</artifactId>
							<version>1.0.0</version>

							<properties>
								<spring-cloud.version>2023.0.0</spring-cloud.version>
							</properties>

							<dependencyManagement>
								<dependencies>
									<dependency>
										<groupId>org.springframework.cloud</groupId>
										<artifactId>spring-cloud-dependencies</artifactId>
										<version>${spring-cloud.version}</version>
										<type>pom</type>
										<scope>import</scope>
									</dependency>
								</dependencies>
							</dependencyManagement>

							<profiles>
								<profile>
									<id>default</id>
									<activation>
										<activeByDefault>true</activeByDefault>
									</activation>
									<dependencies>
										<dependency>
											<groupId>org.springframework.cloud</groupId>
											<artifactId>spring-cloud-starter-netflix-eureka-client</artifactId>
										</dependency>
									</dependencies>
								</profile>
							</profiles>
						</project>
						`,
				},
			},
		},
		{
			name: "Set profiles and set activeByDefault = false",
			testPoms: []testPom{
				{
					pomFilePath: "./pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>

							<parent>
								<groupId>org.springframework.boot</groupId>
								<artifactId>spring-boot-starter-parent</artifactId>
								<version>3.2.3</version>
							</parent>

							<groupId>com.example</groupId>
							<artifactId>example-project</artifactId>
							<version>1.0.0</version>

							<properties>
								<spring-cloud.version>2023.0.0</spring-cloud.version>
							</properties>

							<dependencyManagement>
								<dependencies>
									<dependency>
										<groupId>org.springframework.cloud</groupId>
										<artifactId>spring-cloud-dependencies</artifactId>
										<version>${spring-cloud.version}</version>
										<type>pom</type>
										<scope>import</scope>
									</dependency>
								</dependencies>
							</dependencyManagement>

							<profiles>
								<profile>
									<id>default</id>
									<activation>
										<activeByDefault>false</activeByDefault>
									</activation>
									<dependencies>
										<dependency>
											<groupId>org.springframework.cloud</groupId>
											<artifactId>spring-cloud-starter-netflix-eureka-client</artifactId>
										</dependency>
									</dependencies>
								</profile>
							</profiles>
						</project>
						`,
				},
			},
		},
		{
			name: "Override properties in profile",
			testPoms: []testPom{
				{
					pomFilePath: "./pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>

							<parent>
								<groupId>org.springframework.boot</groupId>
								<artifactId>spring-boot-starter-parent</artifactId>
								<version>3.2.3</version>
							</parent>

							<groupId>com.example</groupId>
							<artifactId>example-project</artifactId>
							<version>1.0.0</version>

							<properties>
								<spring-cloud.version>2023.0.0</spring-cloud.version>
							</properties>

							<dependencyManagement>
								<dependencies>
									<dependency>
										<groupId>org.springframework.cloud</groupId>
										<artifactId>spring-cloud-dependencies</artifactId>
										<version>${spring-cloud.version}</version>
										<type>pom</type>
										<scope>import</scope>
									</dependency>
								</dependencies>
							</dependencyManagement>

							<profiles>
								<profile>
									<id>default</id>
									<activation>
										<activeByDefault>true</activeByDefault>
									</activation>
									<properties>
										<spring-cloud.version>2023.0.4</spring-cloud.version>
									</properties>
									<dependencies>
										<dependency>
											<groupId>org.springframework.cloud</groupId>
											<artifactId>spring-cloud-starter-netflix-eureka-client</artifactId>
										</dependency>
									</dependencies>
								</profile>
							</profiles>
						</project>
						`,
				},
			},
		},
		{
			name: "Add build section in profile",
			testPoms: []testPom{
				{
					pomFilePath: "./pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>

							<parent>
								<groupId>org.springframework.boot</groupId>
								<artifactId>spring-boot-starter-parent</artifactId>
								<version>3.3.5</version>
							</parent>

							<groupId>com.example</groupId>
							<artifactId>example-project</artifactId>
							<version>1.0.0</version>

							<profiles>
								<profile>
									<id>default</id>
									<activation>
										<activeByDefault>true</activeByDefault>
									</activation>
									<build>
										<plugins>
											<plugin>
												<groupId>org.springframework.boot</groupId>
												<artifactId>spring-boot-maven-plugin</artifactId>
												<version>3.3.5</version>
												<executions>
													<execution>
														<goals>
															<goal>repackage</goal>
														</goals>
													</execution>
												</executions>
											</plugin>
										</plugins>
									</build>
								</profile>
							</profiles>
						</project>
						`,
				},
			},
		},
		{
			name: "Add dependencyManagement section in profile",
			testPoms: []testPom{
				{
					pomFilePath: "./pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>

							<groupId>com.example</groupId>
							<artifactId>example-project</artifactId>
							<version>1.0.0</version>

							<dependencyManagement>
								<dependencies>
									<dependency>
										<groupId>org.springframework.boot</groupId>
										<artifactId>spring-boot-dependencies</artifactId>
										<version>3.0.0</version>
										<type>pom</type>
										<scope>import</scope>
									</dependency>
								</dependencies>
							</dependencyManagement>
							<dependencies>
								<dependency>
									<groupId>org.springframework</groupId>
									<artifactId>spring-core</artifactId>
									<scope>compile</scope>
								</dependency>
								<dependency>
									<groupId>junit</groupId>
									<artifactId>junit</artifactId>
									<scope>test</scope>
								</dependency>
							</dependencies>

							<profiles>
								<profile>
									<id>default</id>
									<activation>
										<activeByDefault>true</activeByDefault>
									</activation>
									<dependencyManagement>
										<dependencies>
											<dependency>
												<groupId>org.springframework</groupId>
												<artifactId>spring-core</artifactId>
												<version>5.3.8</version>
												<scope>compile</scope>
											</dependency>
											<dependency>
												<groupId>junit</groupId>
												<artifactId>junit</artifactId>
												<version>4.13.2</version>
												<scope>test</scope>
											</dependency>
										</dependencies>
									</dependencyManagement>
								</profile>
							</profiles>
						</project>
						`,
				},
			},
		},
		{
			name: "Add dependencyManagement and dependencies section in profile",
			testPoms: []testPom{
				{
					pomFilePath: "./pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>

							<groupId>com.example</groupId>
							<artifactId>example-project</artifactId>
							<version>1.0.0</version>

							<dependencyManagement>
								<dependencies>
									<dependency>
										<groupId>org.springframework.boot</groupId>
										<artifactId>spring-boot-dependencies</artifactId>
										<version>3.0.0</version>
										<type>pom</type>
										<scope>import</scope>
									</dependency>
								</dependencies>
							</dependencyManagement>

							<profiles>
								<profile>
									<id>default</id>
									<activation>
										<activeByDefault>true</activeByDefault>
									</activation>
									<dependencyManagement>
										<dependencies>
											<dependency>
												<groupId>org.springframework</groupId>
												<artifactId>spring-core</artifactId>
												<version>5.3.8</version>
												<scope>compile</scope>
											</dependency>
											<dependency>
												<groupId>junit</groupId>
												<artifactId>junit</artifactId>
												<version>4.13.2</version>
												<scope>test</scope>
											</dependency>
										</dependencies>
									</dependencyManagement>
									<dependencies>
										<dependency>
											<groupId>org.springframework</groupId>
											<artifactId>spring-core</artifactId>
											<scope>compile</scope>
										</dependency>
										<dependency>
											<groupId>junit</groupId>
											<artifactId>junit</artifactId>
											<scope>test</scope>
										</dependency>
									</dependencies>
								</profile>
							</profiles>
						</project>
						`,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			workingDir, err := prepareTestPomFiles(tt.testPoms)
			if err != nil {
				t.Fatalf("%v", err)
			}
			for _, testPom := range tt.testPoms {
				pomFilePath := filepath.Join(workingDir, testPom.pomFilePath)
				effectivePom, err := createEffectivePom(pomFilePath)
				if err != nil {
					t.Fatalf("%v", err)
				}
				simulatedEffectivePom, err := createSimulatedEffectivePom(pomFilePath)
				if err != nil {
					t.Fatalf("%v", err)
				}
				if !reflect.DeepEqual(effectivePom.Dependencies, simulatedEffectivePom.Dependencies) {
					t.Fatalf("\neffectivePom.Dependencies:          %s\nsimulatedEffectivePom.Dependencies:   %s",
						effectivePom.Dependencies, simulatedEffectivePom.Dependencies)
				}
				removeDefaultMavenPluginsInEffectivePom(&effectivePom)
				if !reflect.DeepEqual(effectivePom.Build.Plugins, simulatedEffectivePom.Build.Plugins) {
					t.Fatalf("\neffectivePom.Build.Plugins:          %s\nsimulatedEffectivePom.Build.Plugins:   %s",
						effectivePom.Build.Plugins, simulatedEffectivePom.Build.Plugins)
				}
			}
		})
	}
}

func removeDefaultMavenPluginsInEffectivePom(effectivePom *pom) {
	var newPlugins []plugin
	for _, plugin := range effectivePom.Build.Plugins {
		if strings.HasPrefix(plugin.ArtifactId, "maven-") &&
			strings.HasSuffix(plugin.ArtifactId, "-plugin") {
			continue
		}
		newPlugins = append(newPlugins, plugin)
	}
	effectivePom.Build.Plugins = newPlugins
}

type testPom struct {
	pomFilePath      string
	pomContentString string
}

func prepareTestPomFiles(testPoms []testPom) (string, error) {
	tempDir, err := os.MkdirTemp("", "prepareTestPomFiles")
	if err != nil {
		return "", err
	}
	for _, testPom := range testPoms {
		pomPath := filepath.Join(tempDir, testPom.pomFilePath)
		err := os.MkdirAll(filepath.Dir(pomPath), 0755)
		if err != nil {
			return "", err
		}
		err = os.WriteFile(pomPath, []byte(testPom.pomContentString), 0600)
		if err != nil {
			return "", err
		}
	}
	return tempDir, nil
}
