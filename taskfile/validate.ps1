# Validate project blueprints in examples/. Validates all examples if no argument is provided.
. (Join-Path $PSScriptRoot "modules.ps1")

$projects = @($args | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
if ($projects.Count -eq 0) {
    $examplesDir = Join-Path $RepoRoot "examples"
    $projects = @(Get-ChildItem -Path $examplesDir -Directory | Sort-Object Name | ForEach-Object { $_.Name })
}

Push-Location (Join-Path $RepoRoot "builder")
try {
    foreach ($p in $projects) {
        Write-Host "--- $p ---"
        go run ./cmd validate $p
        if ($LASTEXITCODE -ne 0) {
            throw "validate failed for project '$p' (exit $LASTEXITCODE)"
        }
    }
}
finally {
    Pop-Location
}
Write-Host "validate done"
