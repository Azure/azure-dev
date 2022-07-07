param(
    [string] $Version,
    [string] $SourceVersion
)
go build -ldflags="-X 'github.com/azure/azure-dev/cli/azd/internal.Version=$Version (commit $SourceVersion)'"