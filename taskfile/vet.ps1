. (Join-Path $PSScriptRoot "modules.ps1")
Invoke-PerModule -Names $Modules -Action { go vet ./... }
Write-Host "vet done"
