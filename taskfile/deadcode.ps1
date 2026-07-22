# Dead-code detection, in two complementary layers.
. (Join-Path $PSScriptRoot "modules.ps1")

$deadcodeTool = Get-Command deadcode -ErrorAction SilentlyContinue

# Collect the packages reachable from the command modules. The workspace uses
# go.work, so package dependencies do not need replace directives in go.mod.
#
# scenarios used to be named here on top of $CmdModules, because the suite was a
# bag of test files with no entry point. It has a cmd/ now, so the probe in
# modules.ps1 finds it like any other command module and there is no special case
# left to make.
$roots = @($CmdModules) | Sort-Object -Unique
$reachable = New-Object System.Collections.Generic.HashSet[string]
foreach ($m in $roots) {
    Write-Host "--- $m (dependencies) ---"
    Push-Location (Join-Path $RepoRoot $m)
    try {
        $deps = go list -test -deps -f '{{.ImportPath}}' ./... 2>&1
        if ($LASTEXITCODE -ne 0) {
            $deps | Out-String -Stream | ForEach-Object { Write-Host $_ }
            throw "go list failed in '$m'"
        }
        foreach ($dep in $deps) {
            $dep = "$dep".Trim()
            if ($dep) { [void]$reachable.Add($dep) }
        }
    }
    finally { Pop-Location }
}

# Layer 1: unreachable functions across all command modules.
# Starting from each root module's commands and package test executables, report
# functions that no execution or test path can reach across the workspace.
$rootPatterns = @($roots | ForEach-Object { "./$_/..." })
Push-Location $RepoRoot
try {
    # deadcode defaults to the module of the first package. Filter to the project
    # module prefix instead, so dead code in any workspace module reachable from a
    # command is reported while third-party dependencies are left out.
    # The filter is quoted: unquoted, PowerShell splits the argument at the dot in
    # "github.com", sending deadcode a bogus package path.
    if ($null -ne $deadcodeTool) {
        $out = & $deadcodeTool.Source -test "-filter=github.com/miroslav-matejovsky/opdl" $rootPatterns 2>&1
    }
    else {
        $out = go run golang.org/x/tools/cmd/deadcode@latest -test "-filter=github.com/miroslav-matejovsky/opdl" $rootPatterns 2>&1
    }
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
finally {
    Pop-Location
}

# Layer 2: packages not reachable from a command module.
# A package that is only imported by another library package is still dead if
# that library package is not part of a command's dependency graph.
$workspacePackages = @()
foreach ($m in $Modules) {
    Push-Location (Join-Path $RepoRoot $m)
    try {
        $rows = go list -f '{{.ImportPath}}|{{.Name}}|{{join .GoFiles ","}}' ./... 2>&1
        if ($LASTEXITCODE -ne 0) {
            $rows | Out-String -Stream | ForEach-Object { Write-Host $_ }
            throw "go list failed in '$m'"
        }
        foreach ($row in $rows) {
            $parts = "$row" -split '\|', 3
            $importPath = $parts[0]
            $name = if ($parts.Count -ge 2) { $parts[1] } else { '' }
            $goFiles = if ($parts.Count -ge 3) { $parts[2] } else { '' }
            $codeFiles = @($goFiles -split ',' | Where-Object { $_ -and $_ -ne 'doc.go' })
            if ($name -eq 'main' -or $codeFiles.Count -eq 0) { continue }
            $workspacePackages += $importPath
        }
    }
    finally { Pop-Location }
}

$orphans = @($workspacePackages | Where-Object { -not $reachable.Contains($_) })

if ($orphans) {
    Write-Host "unreachable (dead) packages from command modules:"
    $orphans | Sort-Object -Unique | ForEach-Object { Write-Host "  $_" }
    throw "unreachable packages found"
}

Write-Host "no dead code found"
