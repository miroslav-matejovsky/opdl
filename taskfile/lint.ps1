. (Join-Path $PSScriptRoot "modules.ps1")
Write-Host "Running golangci-lint per module. This may take a moment..."
Invoke-PerModule -Names $Modules -Action { golangci-lint run ./... }
Write-Host "lint done"
