# Dead-code detection, in two complementary layers.
. (Join-Path $PSScriptRoot "modules.ps1")

# Layer 1: unreachable functions.
# Starting from each module's commands and package test executables, report
# functions that no execution or test path can reach. Test roots matter for
# reusable packages whose public API is exercised outside command flow.
Invoke-PerModule -Names $CmdModules -Action {
    $out = go run golang.org/x/tools/cmd/deadcode@latest -test ./... 2>&1
    if ($LASTEXITCODE -ne 0) {
        $out | Out-String -Stream | ForEach-Object { Write-Host $_ }
        throw "deadcode failed"
    }
    if ($out) {
        Write-Host "dead code found:"
        $out | Out-String -Stream | ForEach-Object { Write-Host $_ }
        throw "dead code found"
    }
}

# Layer 2: unimported packages in library modules.
# The reachability check above only sees packages that something imports; a
# package that NOTHING imports (an unused contract package like command) is
# invisible to it. A library module exists solely to be imported by other
# modules, so any package in it with zero importers anywhere in the workspace is
# dead. Detect that class explicitly.

# Library modules are the local replace targets declared across every go.mod
# (`replace ... => ../<dir>`): the modules other modules depend on.
$libraryModules = New-Object System.Collections.Generic.HashSet[string]
foreach ($m in $Modules) {
    $goMod = Join-Path $RepoRoot $m "go.mod"
    if (Test-Path $goMod) {
        foreach ($line in Get-Content $goMod) {
            if ($line -match '=>\s*\.\./([^\s]+)') { [void]$libraryModules.Add($Matches[1]) }
        }
    }
}

# Collect every import path used anywhere in the workspace, tests included.
$used = New-Object System.Collections.Generic.HashSet[string]
foreach ($m in $Modules) {
    Push-Location (Join-Path $RepoRoot $m)
    try {
        $raw = go list -f '{{range .Imports}}{{.}} {{end}}{{range .TestImports}}{{.}} {{end}}{{range .XTestImports}}{{.}} {{end}}' ./... 2>&1
        if ($LASTEXITCODE -ne 0) {
            $raw | Out-String -Stream | ForEach-Object { Write-Host $_ }
            throw "go list failed in '$m'"
        }
        foreach ($tok in (($raw -join ' ') -split '\s+')) {
            if ($tok) { [void]$used.Add($tok) }
        }
    }
    finally { Pop-Location }
}

# A library-module package that no one imports is dead. Skip main packages and
# documentation-only packages (a lone doc.go carries no code to be dead).
$orphans = @()
foreach ($lib in $libraryModules) {
    $libDir = Join-Path $RepoRoot $lib
    if (-not (Test-Path $libDir)) { continue }
    Write-Host "--- $lib (imports) ---"
    Push-Location $libDir
    try {
        $rows = go list -f '{{.ImportPath}}|{{.Name}}|{{join .GoFiles ","}}' ./... 2>&1
        if ($LASTEXITCODE -ne 0) {
            $rows | Out-String -Stream | ForEach-Object { Write-Host $_ }
            throw "go list failed in '$lib'"
        }
        foreach ($row in $rows) {
            $parts = "$row" -split '\|', 3
            $importPath = $parts[0]
            $name = if ($parts.Count -ge 2) { $parts[1] } else { '' }
            $goFiles = if ($parts.Count -ge 3) { $parts[2] } else { '' }
            if ($name -eq 'main') { continue }
            if ($goFiles -eq 'doc.go') { continue }
            if (-not $used.Contains($importPath)) { $orphans += $importPath }
        }
    }
    finally { Pop-Location }
}

if ($orphans) {
    Write-Host "unimported (dead) packages in library modules:"
    $orphans | Sort-Object -Unique | ForEach-Object { Write-Host "  $_" }
    throw "unimported packages found"
}

Write-Host "no dead code found"
