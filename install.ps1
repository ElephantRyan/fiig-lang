# Installs the fiig toolchain from a GitHub release.
#
#   irm https://raw.githubusercontent.com/ElephantRyan/fiig-lang/main/install.ps1 | iex
#
# $env:FIIG_VERSION  a release tag such as v0.1.0-alpha.2 (default: the
#                    newest release, pre-releases included: every alpha is one)
# $env:FIIG_INSTALL  the directory to put fiig.exe in
#                    (default: %LOCALAPPDATA%\fiig)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$repo = 'ElephantRyan/fiig-lang'
$dest = if ($env:FIIG_INSTALL) { $env:FIIG_INSTALL } else { Join-Path $env:LOCALAPPDATA 'fiig' }

if ($env:PROCESSOR_ARCHITECTURE -ne 'AMD64') {
    throw "install: unsupported CPU $($env:PROCESSOR_ARCHITECTURE); releases are windows/amd64 only"
}

$version = $env:FIIG_VERSION
if (-not $version) {
    # /releases/latest skips pre-releases, so ask for the newest release.
    $version = (Invoke-RestMethod "https://api.github.com/repos/$repo/releases?per_page=1")[0].tag_name
    if (-not $version) { throw "install: could not find a release of $repo" }
}

$name = "fiig-$version-windows-amd64"
$base = "https://github.com/$repo/releases/download/$version"
$tmp = Join-Path ([IO.Path]::GetTempPath()) ([IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    Write-Host "Downloading fiig $version (windows/amd64)"
    Invoke-WebRequest "$base/$name.zip" -OutFile (Join-Path $tmp "$name.zip")
    Invoke-WebRequest "$base/SHA256SUMS" -OutFile (Join-Path $tmp 'SHA256SUMS')

    $line = Get-Content (Join-Path $tmp 'SHA256SUMS') | Where-Object { $_ -match " $([regex]::Escape($name)).zip$" }
    if (-not $line) { throw "install: SHA256SUMS has no entry for $name.zip" }
    $want = ($line -split '\s+')[0]
    $have = (Get-FileHash (Join-Path $tmp "$name.zip") -Algorithm SHA256).Hash
    if ($have -ne $want) { throw "install: checksum mismatch for $name.zip" }

    Expand-Archive (Join-Path $tmp "$name.zip") -DestinationPath $tmp
    New-Item -ItemType Directory -Force -Path $dest | Out-Null
    Copy-Item (Join-Path $tmp "$name\fiig.exe") (Join-Path $dest 'fiig.exe') -Force
} finally {
    Remove-Item $tmp -Recurse -Force -ErrorAction SilentlyContinue
}

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($userPath -split ';') -notcontains $dest) {
    [Environment]::SetEnvironmentVariable('Path', "$userPath;$dest", 'User')
    Write-Host "Added $dest to your PATH; open a new terminal to use it."
}
Write-Host "Installed $(& (Join-Path $dest 'fiig.exe') version | Select-Object -First 1) to $dest"
Write-Host 'Next: docs/getting-started.md in the repository, or  fiig help'
