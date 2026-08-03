# Runs the unit tests and writes a JUnit report.
#
# The pipeline publishes **/junitTestReport.xml from the extension directory,
# so the report has to be written under that name for results to show up in the
# build. gotestsum produces it; the go test fallback does not, so the fallback
# only runs when gotestsum is unavailable.
#
# The live integration tests are excluded: they carry the `live` build tag, so
# an untagged run does not compile them, and they additionally require
# AZURE_AI_EVAL_E2E_LIVE and a project endpoint.
#
# TODO before the first release: PR CI runs this script on windows, linux and
# darwin amd64, so the untagged tests are covered on all three. The live and
# hero suites are not -- being tagged, no pipeline has ever compiled them, and
# both have only ever run on Windows by hand. Run them once on linux, where
# they assume a path separator and shell out to `azd` and to a proxy address.

$gopath = go env GOPATH
$gotestsumBinary = "gotestsum"
if ($IsWindows) {
    $gotestsumBinary += ".exe"
}
$gotestsum = Join-Path $gopath "bin" $gotestsumBinary

Write-Host "Running unit tests..."

if (Test-Path $gotestsum) {
    & $gotestsum --format testname --junitfile junitTestReport.xml -- ./... -count=1
} else {
    Write-Host "gotestsum not found; falling back to go test (no JUnit report)." -ForegroundColor Yellow
    go test ./... -v -count=1
}

if ($LASTEXITCODE -ne 0) {
    Write-Host ""
    Write-Host "Tests failed with exit code: $LASTEXITCODE" -ForegroundColor Red
    exit $LASTEXITCODE
}

Write-Host ""
Write-Host "All tests passed!" -ForegroundColor Green
exit 0
