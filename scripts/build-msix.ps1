[CmdletBinding()]
param(
    [string]$CertificatePath,
    [Security.SecureString]$CertificatePassword
)

$ErrorActionPreference = 'Stop'
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$distDirectory = Join-Path $projectRoot 'dist'
$stageDirectory = Join-Path $distDirectory 'msix-stage'
$packagePath = Join-Path $distDirectory 'WardogsMortar.msix'

function Find-WindowsSdkTool([string]$Name) {
    $command = Get-Command $Name -ErrorAction SilentlyContinue
    if ($command) {
        return $command.Source
    }

    $localTool = Get-ChildItem -LiteralPath (Join-Path $projectRoot '.tools') -Recurse -File -Filter $Name -ErrorAction SilentlyContinue |
        Where-Object { $_.FullName -match '\\x64\\' } |
        Sort-Object FullName -Descending |
        Select-Object -First 1
    if ($localTool) {
        return $localTool.FullName
    }

    $kitsRoot = Join-Path ${env:ProgramFiles(x86)} 'Windows Kits\10\bin'
    if (Test-Path -LiteralPath $kitsRoot) {
        $candidate = Get-ChildItem -LiteralPath $kitsRoot -Directory |
            Sort-Object Name -Descending |
            ForEach-Object { Join-Path $_.FullName "x64\$Name" } |
            Where-Object { Test-Path -LiteralPath $_ } |
            Select-Object -First 1
        if ($candidate) {
            return $candidate
        }
    }
    return $null
}

function Assert-SafeStagePath([string]$Path) {
    $resolvedRoot = $projectRoot.TrimEnd('\') + '\'
    $resolvedPath = [IO.Path]::GetFullPath($Path)
    if (-not $resolvedPath.StartsWith($resolvedRoot, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to modify a staging path outside the project: $resolvedPath"
    }
}

function New-PackageAsset([string]$Path, [int]$Width, [int]$Height) {
    Add-Type -AssemblyName System.Drawing
    $bitmap = [Drawing.Bitmap]::new($Width, $Height)
    try {
        $graphics = [Drawing.Graphics]::FromImage($bitmap)
        try {
            $graphics.Clear([Drawing.Color]::FromArgb(19, 24, 31))
            $brush = [Drawing.SolidBrush]::new([Drawing.Color]::FromArgb(64, 190, 255))
            try {
                $margin = [Math]::Max(2, [int]($Width * 0.16))
                $graphics.FillEllipse($brush, $margin, $margin, $Width - 2 * $margin, $Height - 2 * $margin)
            }
            finally {
                $brush.Dispose()
            }
        }
        finally {
            $graphics.Dispose()
        }
        $bitmap.Save($Path, [Drawing.Imaging.ImageFormat]::Png)
    }
    finally {
        $bitmap.Dispose()
    }
}

$makeAppx = Find-WindowsSdkTool 'makeappx.exe'
if (-not $makeAppx) {
    throw 'makeappx.exe was not found. Install the Windows 10/11 SDK, then run this script again.'
}

Assert-SafeStagePath $stageDirectory
if (Test-Path -LiteralPath $stageDirectory) {
    Remove-Item -LiteralPath $stageDirectory -Recurse -Force
}
New-Item -ItemType Directory -Force -Path (Join-Path $stageDirectory 'Assets') | Out-Null
New-Item -ItemType Directory -Force -Path $distDirectory | Out-Null

Push-Location $projectRoot
try {
    # Wails selects its real desktop runtime through the production build tag.
    & go build -tags production -trimpath -ldflags '-s -w -H=windowsgui' -o (Join-Path $stageDirectory 'WardogsMortar.exe') ./cmd/wardogs-mortar
    if ($LASTEXITCODE -ne 0) {
        throw "Go build failed with exit code $LASTEXITCODE."
    }
}
finally {
    Pop-Location
}

Copy-Item -LiteralPath (Join-Path $projectRoot 'packaging\AppxManifest.xml') -Destination $stageDirectory
Copy-Item -LiteralPath (Join-Path $projectRoot 'packaging\config.default.json') -Destination $stageDirectory
New-PackageAsset (Join-Path $stageDirectory 'Assets\StoreLogo.png') 50 50
New-PackageAsset (Join-Path $stageDirectory 'Assets\Square44x44Logo.png') 44 44
New-PackageAsset (Join-Path $stageDirectory 'Assets\Square150x150Logo.png') 150 150

if (Test-Path -LiteralPath $packagePath) {
    Remove-Item -LiteralPath $packagePath -Force
}
& $makeAppx pack /d $stageDirectory /p $packagePath /o
if ($LASTEXITCODE -ne 0) {
    throw "MSIX packaging failed with exit code $LASTEXITCODE."
}

if ($CertificatePath) {
    $signTool = Find-WindowsSdkTool 'signtool.exe'
    if (-not $signTool) {
        throw 'signtool.exe was not found. Install the Windows 10/11 SDK signing tools.'
    }
    $passwordPointer = [IntPtr]::Zero
    try {
        $signArguments = @('sign', '/fd', 'SHA256', '/f', $CertificatePath)
        if ($CertificatePassword) {
            $passwordPointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($CertificatePassword)
            $plainPassword = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($passwordPointer)
            $signArguments += @('/p', $plainPassword)
        }
        $signArguments += $packagePath
        & $signTool @signArguments
        if ($LASTEXITCODE -ne 0) {
            throw "MSIX signing failed with exit code $LASTEXITCODE."
        }
    }
    finally {
        if ($passwordPointer -ne [IntPtr]::Zero) {
            [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($passwordPointer)
        }
    }
}

Write-Host "Built: $packagePath"
if (-not $CertificatePath) {
    Write-Warning 'The MSIX is unsigned. Sign it with a certificate whose subject matches the manifest Publisher before installation.'
}
