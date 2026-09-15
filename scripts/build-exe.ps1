[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$outputDirectory = Join-Path $projectRoot 'dist'
$outputFile = Join-Path $outputDirectory 'WardogsMortar.exe'

New-Item -ItemType Directory -Force -Path $outputDirectory | Out-Null
Push-Location $projectRoot
try {
    # Wails selects its real desktop runtime through the production build tag.
    & go build -tags production -trimpath -ldflags '-s -w -H=windowsgui' -o $outputFile ./cmd/wardogs-mortar
    if ($LASTEXITCODE -ne 0) {
        throw "Go build failed with exit code $LASTEXITCODE."
    }
}
finally {
    Pop-Location
}

Write-Host "Built: $outputFile"
