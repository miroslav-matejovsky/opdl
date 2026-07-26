. (Join-Path $PSScriptRoot "modules.ps1")
Write-Host "tidy starting"
Invoke-PerModule -Names $Modules -Action { go mod tidy }
Write-Host "tidy done"
