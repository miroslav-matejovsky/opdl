# Run the black-box scenario suite. No build-tagged platform integration
# tests exist yet; add that step back here when the platform grows some.
. (Join-Path $PSScriptRoot "modules.ps1")

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
