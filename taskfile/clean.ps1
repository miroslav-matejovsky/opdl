# Remove build artifacts, test results, and .exe files from repo
. (Join-Path $PSScriptRoot "modules.ps1")

$foldersToRemove = @(".test-results", ".cache", ".runs", "site", "bin", "dist", ".tmp")

$locations = @($RepoRoot) + ($Modules | ForEach-Object { Join-Path $RepoRoot $_ })

foreach ($loc in $locations) {
  foreach ($folder in $foldersToRemove) {
    $target = Join-Path $loc $folder
    if (Test-Path $target) {
      Write-Host "removing folder: $target"
      Remove-Item -Recurse -Force $target
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
    Remove-Item -Force $_.FullName
  }
}
# this script is called from Taskfile, so the final message is printed by Taskfile, not here
# Write-Host "clean done"
