$ErrorActionPreference = "Stop"

$Repo = if ($env:ZETA_REPO) { $env:ZETA_REPO } else { "SlimaineB/zeta" }
$Version = if ($env:ZETA_VERSION) { $env:ZETA_VERSION } else { "latest" }
$BinDir = if ($env:ZETA_BIN_DIR) { $env:ZETA_BIN_DIR } else { Join-Path $env:LOCALAPPDATA "zeta\bin" }

$Arch = switch ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture) {
  "X64" { "amd64" }
  "Arm64" { "arm64" }
  default { throw "Unsupported architecture: $([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture)" }
}

$Asset = "zeta-windows-$Arch.exe"
if ($Version -eq "latest") {
  $Base = "https://github.com/$Repo/releases/latest/download"
} else {
  if (-not $Version.StartsWith("v")) { $Version = "v$Version" }
  $Base = "https://github.com/$Repo/releases/download/$Version"
}

$Tmp = Join-Path ([System.IO.Path]::GetTempPath()) ([System.Guid]::NewGuid().ToString())
New-Item -ItemType Directory -Path $Tmp | Out-Null
try {
  Write-Host "==> Downloading $Asset"
  Invoke-WebRequest -Uri "$Base/$Asset" -OutFile (Join-Path $Tmp $Asset)

  $Checksums = Join-Path $Tmp "checksums.txt"
  try {
    Invoke-WebRequest -Uri "$Base/checksums.txt" -OutFile $Checksums
    $Line = Get-Content $Checksums | Where-Object { $_ -match "\s$([regex]::Escape($Asset))$" } | Select-Object -First 1
    if ($Line) {
      $Expected = ($Line -split "\s+")[0]
      $Actual = (Get-FileHash -Algorithm SHA256 (Join-Path $Tmp $Asset)).Hash.ToLower()
      if ($Expected -ne $Actual) { throw "Checksum mismatch for $Asset" }
    }
  } catch {
    if ($_.Exception.Message -like "Checksum mismatch*") { throw }
    Write-Host "warning: checksum verification skipped: $_"
  }

  New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
  Copy-Item (Join-Path $Tmp $Asset) (Join-Path $BinDir "zeta.exe") -Force
  Write-Host "==> Installed $(Join-Path $BinDir 'zeta.exe')"

  $UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
  if ($UserPath -notlike "*$BinDir*") {
    $NewPath = if ([string]::IsNullOrEmpty($UserPath)) { $BinDir } else { "$UserPath;$BinDir" }
    [Environment]::SetEnvironmentVariable("Path", $NewPath, "User")
    Write-Host "Added $BinDir to your user PATH. Open a new terminal to use it."
  }
} finally {
  Remove-Item -Recurse -Force $Tmp -ErrorAction SilentlyContinue
}

Write-Host ""
Write-Host "Next:"
Write-Host "  zeta login --server http://zeta.local --email dev@example.com --password dev"
