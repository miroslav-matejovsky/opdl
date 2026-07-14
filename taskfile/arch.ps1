# Architecture linting. Run go-arch-lint in every module that ships an
# architecture spec (.go-arch-lint.yml), enforcing the package dependency rules
# declared there. Uses `go run ...@latest` so no prior tool install is required,
# matching the deadcode task.
. (Join-Path $PSScriptRoot "modules.ps1")

$ran = $false
foreach ($m in $Modules) {
    $moduleDir = Join-Path $RepoRoot $m
    $spec = Join-Path $moduleDir ".go-arch-lint.yml"
    if (-not (Test-Path $spec)) { continue }

    $ran = $true
    Write-Host "--- $m ---"
    Push-Location $moduleDir
    try {
        go run github.com/fe3dback/go-arch-lint@latest check --project-path $moduleDir
        if ($LASTEXITCODE -ne 0) {
            throw "go-arch-lint failed in module '$m'"
        }
    }
    finally {
        Pop-Location
    }
}

if (-not $ran) {
    Write-Host "no architecture specs (.go-arch-lint.yml) found"
}
Write-Host "arch done"
