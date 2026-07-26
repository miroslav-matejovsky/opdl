# Remove build artifacts, test results, and .exe files from repo
. (Join-Path $PSScriptRoot "modules.ps1")

Write-Host "clean starting"

$foldersToRemove = @(".test-results", ".cache", ".runs", "site", "bin", "dist", ".tmp")

$locations = @($RepoRoot) + ($Modules | ForEach-Object { Join-Path $RepoRoot $_ })

# Removal fails when another process still holds a file inside the target. That
# is not a broken clean. It means something is still running, and deleting the
# rest of the tree underneath it would be worse: the running suite then fails on
# a missing binary or a missing deployment.json, which says nothing about the
# real cause.
#
# scenarios/.tmp is the case this happens on. A scenario suite keeps its built
# binaries and its NATS journals there for the whole run, and `task all` starts
# by cleaning, so running `task all` while a suite is open in another terminal
# lands here every time. The raw PowerShell error names neither the directory
# nor the reason, so say both and stop.
function Remove-TargetOrExplain {
  param(
    [Parameter(Mandatory)][string]$Target,
    [Parameter(Mandatory)][string]$Kind
  )
  try {
    Remove-Item -Recurse -Force -ErrorAction Stop $Target
  }
  catch {
    Write-Host ""
    Write-Host "clean could not remove this $($Kind):"
    Write-Host "  $Target"
    Write-Host "  $($_.Exception.Message)"
    Write-Host ""
    Write-Host "A process is still holding a file inside it. The usual cause is a"
    Write-Host "scenario suite running in another terminal, which holds its platform"
    Write-Host "processes and journals under scenarios/.tmp for the whole run."
    Write-Host ""
    Write-Host "Let it finish or stop it, then clean again."
    throw "clean failed on $Target"
  }
}

foreach ($loc in $locations) {
  foreach ($folder in $foldersToRemove) {
    $target = Join-Path $loc $folder
    if (Test-Path $target) {
      Write-Host "removing folder: $target"
      Remove-TargetOrExplain -Target $target -Kind "folder"
    }
  }
}

$foldersToIgnore = @(".venv")

Write-Host "removing *.exe files (ignoring: $($foldersToIgnore -join ', '))..."
Get-ChildItem -Recurse -Filter "*.exe" -File | ForEach-Object {
  $parts = $_.FullName -split [regex]::Escape([System.IO.Path]::DirectorySeparatorChar)
  $ignored = $parts | Where-Object { $foldersToIgnore -contains $_ }
  if ($ignored) {
    Write-Host "ignoring $($_.FullName) (in excluded folder: $($ignored -join ', '))"
  }
  else {
    Write-Host "removing $($_.FullName)"
    Remove-TargetOrExplain -Target $_.FullName -Kind "file"
  }
}
# this script is called from Taskfile, so the final message is printed by Taskfile, not here
# Write-Host "clean done"
