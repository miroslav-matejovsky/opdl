# Run the black-box scenario suite: build a deployment package, start the built
# binary, and drive it from outside the process.
#
# This is the out-of-process half of the resilience gate. The in-process half,
# the platform's own integration tests, is taskfile/integration-tests.ps1. The two are
# separate because they fail for different reasons and take very different times:
# a scenario failure means a packaged binary does not boot or does not recover,
# which is not something a platform test can tell you.
. (Join-Path $PSScriptRoot "modules.ps1")

Write-Host "--- scenarios ---"
Push-Location (Join-Path $RepoRoot "scenarios")
try {
    # Process failover must execute on every resilience gate. Cached results can
    # hide changes to ownership, signals, listeners, or child-process cleanup.
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
