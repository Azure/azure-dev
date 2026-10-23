$ErrorActionPreference = "Stop"

$origin = & git -C $PSScriptRoot remote get-url origin
if ($LASTEXITCODE -ne 0) {
    throw "Could not read the development checkout's origin remote."
}

$pattern = "^(?:https://github\.com/|git@github\.com:|ssh://git@github\.com/)" +
    "(?<owner>[A-Za-z0-9][A-Za-z0-9_.-]*)/(?<repository>[A-Za-z0-9][A-Za-z0-9_.-]*?)(?:\.git)?/?$"
if ($origin -notmatch $pattern) {
    throw "Development origin must be a GitHub HTTPS or SSH repository URL without embedded credentials."
}

"$($Matches.owner)/$($Matches.repository)"
