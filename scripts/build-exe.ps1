[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$outputDirectory = Join-Path $projectRoot 'dist'
$outputFile = Join-Path $outputDirectory 'WardogsMortar.exe'
$projectDirectory = Join-Path $projectRoot 'cmd\wardogs-mortar'
$builtFile = Join-Path $projectDirectory 'build\bin\WardogsMortar.exe'

New-Item -ItemType Directory -Force -Path $outputDirectory | Out-Null
Push-Location $projectDirectory
try {
    # Wails packages build/windows/icon.ico into the executable resources.
    & go run github.com/wailsapp/wails/v2/cmd/wails@v2.15.0 build -clean -s -m -skipbindings -platform windows/amd64 -o WardogsMortar.exe -tags production -trimpath -ldflags '-s -w -H=windowsgui'
    if ($LASTEXITCODE -ne 0) {
        throw "Wails build failed with exit code $LASTEXITCODE."
    }
}
finally {
    Pop-Location
}

if (-not (Test-Path -LiteralPath $builtFile)) {
    throw "Wails build did not produce $builtFile."
}
Copy-Item -LiteralPath $builtFile -Destination $outputFile -Force
Write-Host "Built: $outputFile"
