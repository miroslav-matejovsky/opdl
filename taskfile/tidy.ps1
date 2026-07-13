. (Join-Path $PSScriptRoot "modules.ps1")
Invoke-PerModule -Names $Modules -Action { go mod tidy }
Write-Host "tidy done"
