. (Join-Path $PSScriptRoot "modules.ps1")

# List each module's OWN direct dependencies with available updates.
#
# Run with GOWORK=off so the workspace build list is not unioned in. In
# workspace mode `go list -m all` reports every workspace module's dependencies
# from any module directory, so a blueprint-only dependency (e.g. hcl/v2) would
# wrongly appear under platform, which parses no HCL. With the workspace off,
# each module resolves through its own go.mod + replace directives, so the
# listing reflects that module alone.
Write-Host "Direct dependencies with available updates per module:"

$savedGoWork = $env:GOWORK
$env:GOWORK = "off"
try {
    Invoke-PerModule $Modules {
        go list -u -m -f '{{if not .Indirect}}{{.}}{{end}}' all
    }
}
finally {
    if ($null -eq $savedGoWork) {
        Remove-Item Env:\GOWORK -ErrorAction SilentlyContinue
    }
    else {
        $env:GOWORK = $savedGoWork
    }
}

Write-Host "--- sdk-dotnet ---"
Push-Location (Join-Path $RepoRoot "sdk-dotnet")
try {
    dotnet list Opdl.Sdk.slnx package --outdated
    if ($LASTEXITCODE -ne 0) {
        throw "task failed in module 'sdk-dotnet' (exit $LASTEXITCODE)"
    }
}
finally {
    Pop-Location
}
