# Run build-tagged platform integration tests and the black-box scenario suite.
. (Join-Path $PSScriptRoot "modules.ps1")

Write-Host "--- platform integration ---"
Push-Location (Join-Path $RepoRoot "platform")
try {
    gotestsum --format pkgname -- -tags integration ./...
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
    gotestsum --format pkgname ./...
    if ($LASTEXITCODE -ne 0) {
        throw "scenarios failed (exit $LASTEXITCODE)"
    }
}
finally {
    Pop-Location
}

Write-Host "scenarios done"
