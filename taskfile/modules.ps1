# Shared helpers for the multi-module workspace tasks. Dot-source this file to
# get $RepoRoot, the module list, and Invoke-PerModule.

$ErrorActionPreference = "Stop"

# Repo root is the parent of the taskfile directory this script lives in.
$RepoRoot = Split-Path $PSScriptRoot -Parent

# Discover Go modules from go.work file, maintaining dependency order.
$goWorkPath = Join-Path $RepoRoot "go.work"
$Modules = @()
if (Test-Path $goWorkPath) {
    Get-Content $goWorkPath | ForEach-Object {
        if ($_ -match '^\s*use\s+\./(.+?)\s*$') {
            $Modules += $Matches[1]
        }
    }
}

# Discover modules that have a cmd/ entry point by checking folder existence.
$CmdModules = @()
foreach ($m in $Modules) {
    $cmdPath = Join-Path $RepoRoot $m "cmd"
    if (Test-Path $cmdPath -PathType Container) {
        $CmdModules += $m
    }
}

# Invoke-PerModule runs a script block in each named module directory, failing
# fast if any invocation returns a non-zero exit code.
function Invoke-PerModule {
    param(
        [string[]] $Names,
        [scriptblock] $Action
    )
    foreach ($m in $Names) {
        Write-Host "--- $m ---"
        Push-Location (Join-Path $RepoRoot $m)
        try {
            & $Action
            if ($LASTEXITCODE -ne 0) {
                throw "task failed in module '$m' (exit $LASTEXITCODE)"
            }
        }
        finally {
            Pop-Location
        }
    }
}
