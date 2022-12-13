[cmdletbinding()]
param(
    $Path = "$PSScriptRoot/../..",
    $GhActionsOutput = $env:GITHUB_ACTIONS
)

function parseErrorLine($line) {
    $file, $errorCode, $title = ($line -split ':') | ForEach-Object { $_.Trim() }

    # Filename may have parentheses, use LastIndeOf to pick up only the last set
    # of parentheses after the filename
    $leftParen = $file.LastIndexOf('(')
    $rightParen = $file.LastIndexOf(')')

    # "/path/to/file.bicep(1,2)" -> "1,2"
    $filePosition = $file.Substring($leftParen + 1, $rightParen - $leftParen - 1)

    $line, $column = $filePosition -split ','

    return [PSCustomObject]@{
        # "/path/to/file.bicep(1,2)" -> "/path/to/file.bicep"
        File = $file.Substring(0, $leftParen);
        Line = $line;
        Column = $column;
        ErrorCode = $errorCode;
        Title = $title;
    }
}

$bicepFiles = Get-ChildItem "$Path/*.bicep" -Recurse -Force

# Running bicep in parallel reduce run time from ~52 seconds to ~11 seconds on a
# machine with 4 cores with hyper threading. No significant improvements seen
# when increasing `-ThrottleLimit`.
$outputs = $bicepFiles |
    ForEach-Object -Parallel {
        Write-Verbose "Linting $_..." -Verbose:$Verbose
        $err = $( $result = bicep build $_ ) 2>&1
        return [PSCustomObject]@{
            File = $_;
            Result = $result;
            ExitCode = $LASTEXITCODE;
            Errors = $err
        }
    }

$exitCode = 0
foreach ($result in $outputs) {
    if ($result.ExitCode -eq 0) {
        continue
    }

    Write-Host "Errors in $($result.File)"
    foreach ($line in $result.Errors) {
        Write-Host $line
        $exitCode = 1
        if ($GhActionsOutput) {
            $errorParts = parseErrorLine $line
            Write-Host "::error file=$($errorParts.File),line=$($errorParts.Line),col=$($errorParts.Column)::$($errorParts.ErrorCode) : $($errorParts.Title)"
        }
    }
}

exit $exitCode