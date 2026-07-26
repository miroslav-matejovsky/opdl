. (Join-Path $PSScriptRoot "modules.ps1")
Write-Host "vet starting"
Invoke-PerModule -Names $Modules -Action { go vet ./... }
Write-Host "vet done"
