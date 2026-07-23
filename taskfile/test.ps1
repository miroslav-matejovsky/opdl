# Run unit tests per module with gotestsum, tee output to .test-results/.
. (Join-Path $PSScriptRoot "modules.ps1")

$ts = (Get-Date -Format "yyyyMMdd-HHmmss")
$outDir = Join-Path $RepoRoot ".test-results"
if (-not (Test-Path $outDir)) {
    New-Item -ItemType Directory -Path $outDir | Out-Null
}

foreach ($m in $Modules) {
    Write-Host "--- $m ---"
    $outFile = Join-Path $outDir "unit-$m-$ts.log"
    Push-Location (Join-Path $RepoRoot $m)
    try {
        gotestsum --hide-summary=skipped --format pkgname -- -short ./... 2>&1 | Tee-Object -FilePath $outFile
        if ($LASTEXITCODE -ne 0) {
            throw "tests failed in module '$m' (exit $LASTEXITCODE)"
        }
    }
    finally {
        Pop-Location
    }
}
Write-Host "test done"
