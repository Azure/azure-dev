param(
    [string] $BaseUrl='https://azure-dev.azureedge.net/azd/standalone/release',
    [string] $Version = 'latest',
    [string] $ContainerPrefix = '',
    [string] $AdditionalArgs = '--no-cache'
)
Write-Output "Docker version:"
docker -v

$dockerfiles = Get-ChildItem test/Dockerfile.*
$exitCode = 0
foreach ($dockerfile in $dockerfiles) {
    Write-Output @"
docker build  . `
    -f $dockerfile `
    -t azd-test `
    --build-arg baseUrl="$BaseUrl" `
    --build-arg version="$Version" `
    --build-arg prefix="$ContainerPrefix" `
    $AdditionalArgs
"@
    docker build  . `
        -f $dockerfile `
        -t azd-test `
        --build-arg baseUrl="$BaseUrl" `
        --build-arg version="$Version" `
        --build-arg prefix="$ContainerPrefix" `
        $AdditionalArgs
    if ($LASTEXITCODE) {
        Write-Error "Could not build for $dockerfile"
        $exitCode = 1

        # Build failed, don't execute the container becuase we'll be executing
        # the last successfully built container with the name azd-test
        continue
    }

    docker run -t azd-test
}

exit $exitCode
