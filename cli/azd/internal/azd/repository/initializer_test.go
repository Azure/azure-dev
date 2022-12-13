package repository

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewInitializer(t *testing.T) {
	type args struct {
		azdCtx  *azdcontext.AzdContext
		console input.Console
		gitCli  git.GitCli
	}
	tests := []struct {
		name string
		args args
		want Initializer
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewInitializer(tt.args.azdCtx, tt.args.console, tt.args.gitCli); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewInitializer() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_initializer_Initialize(t *testing.T) {
	type args struct {
		ctx            context.Context
		templateUrl    string
		templateBranch string
	}
	tests := []struct {
		name    string
		i       *initializer
		args    args
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.i.Initialize(tt.args.ctx, tt.args.templateUrl, tt.args.templateBranch); (err != nil) != tt.wantErr {
				t.Errorf("initializer.Initialize() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_initializer_InitializeEmpty(t *testing.T) {
	type args struct {
		ctx context.Context
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			azdctx := &azdcontext.AzdContext{}
			console := mockinput.NewMockConsole()
			i := NewInitializer(azdctx, console, git.NewGitCli(mockexec.NewMockCommandRunner()))
			if err := i.InitializeEmpty(tt.args.ctx); (err != nil) != tt.wantErr {
				t.Errorf("initializer.InitializeEmpty() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_determineDuplicates(t *testing.T) {
	type args struct {
		sourceFiles []string
		targetFiles []string
	}
	tests := []struct {
		name     string
		args     args
		expected []string
	}{
		{"NoDuplicates", args{[]string{"a.txt", "b.txt", "dir1/a.txt"}, []string{"c.txt", "d.txt", "dir2/a.txt"}}, []string{}},
		{"Duplicates", args{
			[]string{
				"a.txt", "b.txt", "c.txt",
				"dir1/a.txt",
				"dir1/dir2/b.txt",
				"dir1/dir2/d.txt"},
			[]string{
				"a.txt", "c.txt",
				"dir1/a.txt",
				"dir1/c.txt",
				"dir1/dir2/b.txt"}},
			[]string{
				"a.txt", "c.txt",
				"dir1/a.txt",
				"dir1/dir2/b.txt"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := t.TempDir()
			target := t.TempDir()

			createFiles(t, source, tt.args.sourceFiles)
			createFiles(t, target, tt.args.targetFiles)

			duplicates, err := determineDuplicates(source, target)

			expected := []string{}
			for _, expectedFile := range tt.expected {
				expected = append(expected, filepath.Clean(expectedFile))
			}

			assert.NoError(t, err)
			assert.ElementsMatch(t, duplicates, expected)
		})
	}
}

func createFiles(t *testing.T, dir string, files []string) {
	for _, file := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, file)), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, file), []byte{}, 0644))
	}
}
