# Run the platform's integration tests: everything the unit gate skips.
#
# The unit gate (taskfile/test.ps1) runs with -short, which skips the tests that
# bind sockets and start real fabric members. Those run here, without -short, so
# every test still compiles and lints in the fast gate but only the cheap ones
# run there.
#
# This is the in-process half of the resilience gate. The out-of-process half,
# which builds a deployment package and runs the built binary, is
# taskfile/scenarios.ps1.
. (Join-Path $PSScriptRoot "modules.ps1")

Write-Host "--- platform (integration tests) ---"
Push-Location (Join-Path $RepoRoot "platform")
try {
    gotestsum --format pkgname ./...
    if ($LASTEXITCODE -ne 0) {
        throw "platform integration tests failed (exit $LASTEXITCODE)"
    }
}
finally {
    Pop-Location
}

Write-Host "integration-tests done"
