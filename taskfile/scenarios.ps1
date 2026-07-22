# Run the black-box scenario suite: build a deployment package, start the built
# binary, and drive it from outside the process.
#
# This is the out-of-process half of the resilience gate. The in-process half,
# the platform's own integration tests, is taskfile/integration-tests.ps1. The two are
# separate because they fail for different reasons and take very different times:
# a scenario failure means a packaged binary does not boot or does not recover,
# which is not something a platform test can tell you.
#
# The suite is a program, not a test binary, so this script only launches it.
# Parallelism, the timeout, selection, and caching are the command's own flags
# and defaults; see scenarios/cmd/main.go. Keeping them there rather than here
# means running the suite by hand behaves exactly like running it from the task.
. (Join-Path $PSScriptRoot "modules.ps1")

Write-Host "--- scenarios ---"
Push-Location (Join-Path $RepoRoot "scenarios")
try {
    go run ./cmd -v
    if ($LASTEXITCODE -ne 0) {
        throw "scenarios failed (exit $LASTEXITCODE)"
    }
}
finally {
    Pop-Location
}

Write-Host "scenarios done"
