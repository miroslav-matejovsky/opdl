. (Join-Path $PSScriptRoot "modules.ps1")
$LintFile = Join-Path $RepoRoot ".golangci.yml"
# verify that the lint config file exists, otherwise golangci-lint will fail with a confusing error.
if (-not (Test-Path $LintFile)) {
  throw "lint config file not found: $LintFile"
}
Write-Host "Running golangci-lint per module. This may take a moment..."
Invoke-PerModule -Names $Modules -Action { golangci-lint run --config $LintFile ./... }
Write-Host "lint done"
