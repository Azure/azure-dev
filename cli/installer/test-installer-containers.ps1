param(
    [Parameter (Mandatory=$true)]
    [string] $BaseUrl,

    [string] $Version = ''
)

$dockerfiles = Get-ChildItem test/Dockerfile.*
$exitCode = 0
foreach ($dockerfile in $dockerfiles) {
    docker build  . -f $dockerfile -t azd-test --build-arg baseUrl="$BaseUrl" --build-arg version="$Version"
    if ($LASTEXITCODE) {
        Write-Error "Could not build for $dockerfile"
        $exitCode = 1
    }
}

exit $exitCode
