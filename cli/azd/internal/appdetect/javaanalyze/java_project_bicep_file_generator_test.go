package javaanalyze

import (
	"fmt"
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
	fmt.Printf("dir:%s\n", dir)
	err := GenerateBicepFilesForJavaProject(dir, javaProject)
	require.NoError(t, err)
}
