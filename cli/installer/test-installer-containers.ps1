param(
    [string] $BaseUrl='https://azure-dev.azureedge.net/azd/standalone/release',
    [string] $Version = 'latest',
    [string] $ContainerPrefix = '',
    [string] $AdditionalBuildArgs = '--no-cache',
    [string] $AdditionalRunArgs = ''
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
    $AdditionalBuildArgs
"@
    docker build  . `
        -f $dockerfile `
        -t azd-test `
        --build-arg baseUrl="$BaseUrl" `
        --build-arg version="$Version" `
        --build-arg prefix="$ContainerPrefix" `
        $AdditionalBuildArgs
    if ($LASTEXITCODE) {
        Write-Error "Could not build for $dockerfile"

        # Build failed. Set exit code to error and move on to build the next
        # test container
        $exitCode = 1
        continue
    }

    docker run $AdditionalRunArgs -t azd-test
    if ($LASTEXITCODE) {
        Write-Error "Validation run failed for $dockerfile"
        $exitCode = 1
        continue
    }
}

exit $exitCode
