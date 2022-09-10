param(
    [Parameter (Mandatory=$true)]
    [string] $BaseUrl,

    [string] $Version = '',
    [string] $ContainerPrefix = ''
)

$dockerfiles = Get-ChildItem test/Dockerfile.*
$exitCode = 0
foreach ($dockerfile in $dockerfiles) {
    Write-Host @"
docker build  . `
    -f $dockerfile `
    -t azd-test `
    --build-arg baseUrl="$BaseUrl" `
    --build-arg version="$Version" `
    --build-arg prefix="$ContainerPrefix"
"@
    docker build  . `
        -f $dockerfile `
        -t azd-test `
        --build-arg baseUrl="$BaseUrl" `
        --build-arg version="$Version" `
        --build-arg prefix="$ContainerPrefix"
    if ($LASTEXITCODE) {
        Write-Error "Could not build for $dockerfile"
        $exitCode = 1
    }
}

exit $exitCode
