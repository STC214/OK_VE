$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

New-Item -ItemType Directory -Force ".tmp-go", ".gocache", ".gomodcache", "bin", "assets" | Out-Null

$env:GOTMPDIR = (Resolve-Path ".tmp-go").Path
$env:GOCACHE = (Resolve-Path ".gocache").Path
$env:GOMODCACHE = (Resolve-Path ".gomodcache").Path

$iconPath = Join-Path $root "assets\app.ico"
$versionSyso = Join-Path $root "cmd\onekeyve-gui\version_windows.syso"
$buildMutex = [System.Threading.Mutex]::new($false, "Global\OneKeyVEGoBuildVersionResource")
$buildMutexAcquired = $false
$versionTimestamp = ""

if (-not (Test-Path $iconPath)) {
  & (Join-Path $PSScriptRoot "convert_image_icon.ps1") -OutputIcon $iconPath
}

try {
  [void]$buildMutex.WaitOne()
  $buildMutexAcquired = $true
  $versionTimestamp = Get-Date -Format "yyyyMMddHHmmss"

  & (Join-Path $PSScriptRoot "build_version_resource.ps1") `
    -OutputSyso $versionSyso `
    -Timestamp $versionTimestamp `
    -ProductName "OneKeyVE" `
    -FileDescription "OneKeyVE" `
    -OriginalFilename "OneKeyVE.exe"

  go build `
    -trimpath `
    -ldflags "-H windowsgui -s -w -X onekeyvego/internal/gui.BuildVersion=$versionTimestamp" `
    -o ".\bin\OneKeyVE.exe" `
    .\cmd\onekeyve-gui
} finally {
  Remove-Item -LiteralPath $versionSyso -Force -ErrorAction SilentlyContinue
  if ($buildMutexAcquired) {
    $buildMutex.ReleaseMutex()
  }
  $buildMutex.Dispose()
}

if (Test-Path $iconPath) {
  & (Join-Path $PSScriptRoot "embed_icon.ps1") -ExePath (Join-Path $root "bin\OneKeyVE.exe") -IconPath $iconPath
}

& (Join-Path $PSScriptRoot "refresh_shell_icons.ps1")
Write-Host "Built OneKeyVE.exe version $versionTimestamp"
