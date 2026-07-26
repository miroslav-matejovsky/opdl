. (Join-Path $PSScriptRoot "modules.ps1")
Write-Host "fmt starting"
Invoke-PerModule -Names $Modules -Action { go fmt ./... }
Write-Host "fmt done"
