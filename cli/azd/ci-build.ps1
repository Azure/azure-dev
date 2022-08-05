param(
    [string] $Version,
    [string] $SourceVersion
)
azd version
go build -ldflags="-X 'github.com/azure/azure-dev/cli/azd/internal.Version=$Version (commit $SourceVersion)'"
