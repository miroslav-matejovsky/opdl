. (Join-Path $PSScriptRoot "modules.ps1")
Invoke-PerModule -Names $Modules -Action { go fmt ./... }
Write-Host "fmt done"
