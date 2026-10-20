# CaptainCode Windows installer.
#
# Download, inspect, then run this script from PowerShell:
#   .\install.ps1 [-Version v0.4.0] [-InstallDir <path>] [-Force]

[CmdletBinding()]
param(
    [string]$Version,
    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA 'Programs\CaptainCode'),
    [switch]$Force
)

$ErrorActionPreference = 'Stop'
$repository = 'john-nash-rs/captaincode'
$architecture = $env:PROCESSOR_ARCHITECTURE
if ($env:PROCESSOR_ARCHITEW6432) { $architecture = $env:PROCESSOR_ARCHITEW6432 }
switch ($architecture) {
    'AMD64' { $goArch = 'amd64' }
    'ARM64' { $goArch = 'arm64' }
    default { throw "Unsupported Windows architecture '$architecture'. CaptainCode supports AMD64 and ARM64." }
}

if (-not $Version) {
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$repository/releases/latest" -Headers @{ 'User-Agent' = 'CaptainCode-installer' }
    $Version = $release.tag_name
    if (-not $Version) { throw 'Could not determine the latest CaptainCode release.' }
}
if ($Version -notmatch '^v?\d+\.\d+\.\d+([-.][0-9A-Za-z.-]+)?$') {
    throw "Invalid version '$Version'. Use a release tag such as v0.4.0."
}
if ($Version -notlike 'v*') { $Version = "v$Version" }

$versionNumber = $Version.Substring(1)
$archiveName = "captaincode_${versionNumber}_windows_${goArch}.zip"
$releaseBase = "https://github.com/$repository/releases/download/$Version"
$temporaryDirectory = Join-Path ([System.IO.Path]::GetTempPath()) ([System.Guid]::NewGuid().ToString('N'))
$archivePath = Join-Path $temporaryDirectory $archiveName
$checksumsPath = Join-Path $temporaryDirectory 'checksums.txt'

try {
    New-Item -ItemType Directory -Path $temporaryDirectory | Out-Null
    Write-Host "Downloading CaptainCode $Version ($goArch)..."
    Invoke-WebRequest -UseBasicParsing -Uri "$releaseBase/$archiveName" -OutFile $archivePath
    Invoke-WebRequest -UseBasicParsing -Uri "$releaseBase/checksums.txt" -OutFile $checksumsPath

    $checksumLine = Get-Content $checksumsPath | Where-Object { $_ -match "^([0-9a-fA-F]{64})\s+\*?$([regex]::Escape($archiveName))$" } | Select-Object -First 1
    if (-not $checksumLine -or $checksumLine -notmatch '^([0-9a-fA-F]{64})\s+') {
        throw "No SHA-256 checksum for $archiveName in the release. Refusing to install."
    }
    $expectedHash = $Matches[1].ToLowerInvariant()
    $actualHash = (Get-FileHash -Algorithm SHA256 -Path $archivePath).Hash.ToLowerInvariant()
    if ($actualHash -ne $expectedHash) { throw "Checksum mismatch for $archiveName. Refusing to install." }

    $extractDirectory = Join-Path $temporaryDirectory 'extracted'
    Expand-Archive -LiteralPath $archivePath -DestinationPath $extractDirectory
    $sourceBinary = Join-Path $extractDirectory 'captaincode.exe'
    if (-not (Test-Path -LiteralPath $sourceBinary -PathType Leaf)) { throw 'The release archive does not contain captaincode.exe.' }

    $destination = Join-Path $InstallDir 'captaincode.exe'
    if ((Test-Path -LiteralPath $destination) -and -not $Force) {
        throw "CaptainCode is already installed at $destination. Close it and rerun with -Force to replace it."
    }
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    Copy-Item -LiteralPath $sourceBinary -Destination $destination -Force

    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $pathEntries = @($userPath -split ';' | Where-Object { $_ })
    if (-not ($pathEntries | Where-Object { $_.TrimEnd('\') -ieq $InstallDir.TrimEnd('\') })) {
        $updatedPath = (@($pathEntries) + $InstallDir) -join ';'
        [Environment]::SetEnvironmentVariable('Path', $updatedPath, 'User')
    }
    if (($env:Path -split ';' | Where-Object { $_.TrimEnd('\') -ieq $InstallDir.TrimEnd('\') }).Count -eq 0) {
        $env:Path = "$InstallDir;$env:Path"
    }

    Write-Host "Installed to $destination"
    Write-Host 'Run: captaincode version'
    Write-Host 'Open a new terminal if captaincode is not yet on PATH.'
    Write-Host 'Then run: captaincode doctor'
}
finally {
    if (Test-Path -LiteralPath $temporaryDirectory) { Remove-Item -LiteralPath $temporaryDirectory -Recurse -Force }
}
