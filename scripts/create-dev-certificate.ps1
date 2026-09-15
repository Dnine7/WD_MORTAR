[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [Security.SecureString]$Password
)

$ErrorActionPreference = 'Stop'
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$outputDirectory = Join-Path $projectRoot 'dist\certificate'
$pfxPath = Join-Path $outputDirectory 'WardogsMortar-dev.pfx'
$cerPath = Join-Path $outputDirectory 'WardogsMortar-dev.cer'

New-Item -ItemType Directory -Force -Path $outputDirectory | Out-Null
$certificate = New-SelfSignedCertificate `
    -Type Custom `
    -Subject 'CN=Wardogs Mortar Calculator' `
    -FriendlyName 'Wardogs Mortar Calculator development signing' `
    -CertStoreLocation 'Cert:\CurrentUser\My' `
    -KeyAlgorithm RSA `
    -KeyLength 3072 `
    -HashAlgorithm SHA256 `
    -KeyUsage DigitalSignature `
    -TextExtension @('2.5.29.37={text}1.3.6.1.5.5.7.3.3') `
    -NotAfter (Get-Date).AddYears(2)

Export-PfxCertificate -Cert $certificate -FilePath $pfxPath -Password $Password | Out-Null
Export-Certificate -Cert $certificate -FilePath $cerPath -Type CERT | Out-Null

Write-Host "PFX: $pfxPath"
Write-Host "CER: $cerPath"
Write-Warning 'The certificate was not trusted automatically. Import the CER into Current User > Trusted People before installing the signed MSIX.'

