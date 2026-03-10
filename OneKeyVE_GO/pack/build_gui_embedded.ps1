$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

New-Item -ItemType Directory -Force ".tmp-go", ".gocache", ".gomodcache", "bin", "assets", "internal\ffmpeg\embedded_bins" | Out-Null

$env:GOTMPDIR = (Resolve-Path ".tmp-go").Path
$env:GOCACHE = (Resolve-Path ".gocache").Path
$env:GOMODCACHE = (Resolve-Path ".gomodcache").Path
$env:GOPROXY = "file:///F:/Programer/Go_Workspace/pkg/mod/cache/download"
$env:GOSUMDB = "off"

$iconPath = Join-Path $root "assets\app.ico"
if (-not (Test-Path $iconPath)) {
  & (Join-Path $PSScriptRoot "convert_image_icon.ps1") -OutputIcon $iconPath
}

$srcBinDir = "C:\ffmpeg\bin"
if (-not (Test-Path $srcBinDir)) {
  throw "Embedded build requires: $srcBinDir"
}

$embeddedDir = Join-Path $root "internal\ffmpeg\embedded_bins"
$runtimeFiles = Get-ChildItem $srcBinDir -File | Where-Object {
  $_.Extension.ToLowerInvariant() -in @(".exe", ".dll")
}
if ($runtimeFiles.Count -eq 0) {
  throw "No FFmpeg runtime files found in: $srcBinDir"
}

foreach ($file in $runtimeFiles) {
  Copy-Item -Force $file.FullName (Join-Path $embeddedDir $file.Name)
}

try {
  go build `
    -tags bundled_ffmpeg `
    -trimpath `
    -ldflags "-H windowsgui -s -w" `
    -o ".\bin\OneKeyVE-embedded.exe" `
    .\cmd\onekeyve-gui
} finally {
  Get-ChildItem $embeddedDir -File | Remove-Item -Force -ErrorAction SilentlyContinue
}

if (Test-Path $iconPath) {
  & (Join-Path $PSScriptRoot "embed_icon.ps1") -ExePath (Join-Path $root "bin\OneKeyVE-embedded.exe") -IconPath $iconPath
}

& (Join-Path $PSScriptRoot "refresh_shell_icons.ps1")
