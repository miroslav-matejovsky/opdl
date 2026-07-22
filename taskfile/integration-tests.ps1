# Run the integration tests: everything the unit gate skips with -short.
#
# The unit gate (taskfile/test.ps1) runs with -short, which skips the tests that
# bind sockets, start real fabric members, or compile the platform. Those run
# here, without -short, so every test still compiles and lints in the fast gate
# but only the cheap ones run there.
#
# Two modules have such tests:
#   - platform: the in-process resilience tests that start real fabric members.
#   - builder: the build package's end-to-end test, which compiles the platform
#     and checks the packages it produces. Platform runs first so its build cache
#     is warm when the build test links its machine binaries.
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

Write-Host "--- builder (integration tests) ---"
Push-Location (Join-Path $RepoRoot "builder")
try {
    gotestsum --format pkgname ./...
    if ($LASTEXITCODE -ne 0) {
        throw "builder integration tests failed (exit $LASTEXITCODE)"
    }
}
finally {
    Pop-Location
}

Write-Host "integration-tests done"
