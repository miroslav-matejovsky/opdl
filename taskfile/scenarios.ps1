# Run the resilience gate: the platform's integration tests and the black-box
# scenario suite.
#
# The unit gate (taskfile/test.ps1) runs with -short, which skips the tests that
# bind sockets and start real fabric members. Those run here, without -short, so
# every test still compiles and lints in the fast gate but only the cheap ones
# run there.
. (Join-Path $PSScriptRoot "modules.ps1")

Write-Host "--- platform (integration tests) ---"
Push-Location (Join-Path $RepoRoot "platform")
try {
    gotestsum --format testname ./...
    if ($LASTEXITCODE -ne 0) {
        throw "platform integration tests failed (exit $LASTEXITCODE)"
    }
}
finally {
    Pop-Location
}

Write-Host "--- scenarios ---"
Push-Location (Join-Path $RepoRoot "scenarios")
try {
    gotestsum --format testname ./...
    if ($LASTEXITCODE -ne 0) {
        throw "scenarios failed (exit $LASTEXITCODE)"
    }
}
finally {
    Pop-Location
}

Write-Host "scenarios done"
