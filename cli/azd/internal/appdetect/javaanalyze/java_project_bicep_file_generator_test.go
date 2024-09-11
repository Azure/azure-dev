package javaanalyze

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestGenerateBicepFilesForJavaProject(t *testing.T) {
	javaProject := JavaProject{
		Services: []ServiceConfig{},
		Resources: []Resource{
			{
				Name:            "mysql_one",
				Type:            "mysql",
				BicepParameters: nil,
				BicepProperties: nil,
			},
		},
		ServiceBindings: []ServiceBinding{},
	}
	dir := t.TempDir()
	err := GenerateBicepFilesForJavaProject(dir, javaProject)
	require.NoError(t, err)
}
