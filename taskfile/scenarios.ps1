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
    gotestsum --format pkgname ./...
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
    # Process failover must execute on every resilience gate. Cached results can
    # hide changes to fencing, signals, listeners, or child-process cleanup.
    #
    # The timeout is raised from the 10 minute default because it bounds the whole
    # binary, not one test. Scenarios run concurrently under a machine budget, so a
    # loaded or small host serializes them behind the budget rather than failing,
    # and the default leaves no room for that.
    gotestsum --format testname -- -count=1 -timeout 30m ./...
    if ($LASTEXITCODE -ne 0) {
        throw "scenarios failed (exit $LASTEXITCODE)"
    }
}
finally {
    Pop-Location
}

Write-Host "scenarios done"
