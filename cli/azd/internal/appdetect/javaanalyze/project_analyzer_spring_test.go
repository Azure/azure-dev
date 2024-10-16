package javaanalyze

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnalyzeSpringProject(t *testing.T) {
	var project = analyzeSpringProject(filepath.Join("testdata", "project-one"))
	require.Equal(t, "", project.applicationProperties["not.exist"])
	require.Equal(t, "jdbc:h2:mem:testdb", project.applicationProperties["spring.datasource.url"])

	project = analyzeSpringProject(filepath.Join("testdata", "project-two"))
	require.Equal(t, "", project.applicationProperties["not.exist"])
	require.Equal(t, "jdbc:h2:mem:testdb", project.applicationProperties["spring.datasource.url"])

	project = analyzeSpringProject(filepath.Join("testdata", "project-three"))
	require.Equal(t, "", project.applicationProperties["not.exist"])
	require.Equal(t, "HTML", project.applicationProperties["spring.thymeleaf.mode"])

	project = analyzeSpringProject(filepath.Join("testdata", "project-four"))
	require.Equal(t, "", project.applicationProperties["not.exist"])
	require.Equal(t, "mysql", project.applicationProperties["database"])
}
