$CliVersionFile = "$PSScriptRoot../../../../cli/version.txt"
$CliChangelogFile = "$PSScriptRoot../../../../cli/azd/CHANGELOG.md"

function CheckoutChangedFiles {
    git checkout $CliVersionFile
    git checkout $CliChangelogFile
}