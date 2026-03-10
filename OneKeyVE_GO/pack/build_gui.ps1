$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

New-Item -ItemType Directory -Force ".tmp-go", ".gocache", ".gomodcache", "bin", "assets" | Out-Null

$env:GOTMPDIR = (Resolve-Path ".tmp-go").Path
$env:GOCACHE = (Resolve-Path ".gocache").Path
$env:GOMODCACHE = (Resolve-Path ".gomodcache").Path
$env:GOPROXY = "file:///F:/Programer/Go_Workspace/pkg/mod/cache/download"
$env:GOSUMDB = "off"

$iconPath = Join-Path $root "assets\app.ico"

if (-not (Test-Path $iconPath)) {
  & (Join-Path $PSScriptRoot "convert_image_icon.ps1") -OutputIcon $iconPath
}

go build `
  -trimpath `
  -ldflags "-H windowsgui -s -w" `
  -o ".\bin\OneKeyVE.exe" `
  .\cmd\onekeyve-gui

if (Test-Path $iconPath) {
  & (Join-Path $PSScriptRoot "embed_icon.ps1") -ExePath (Join-Path $root "bin\OneKeyVE.exe") -IconPath $iconPath
}

& (Join-Path $PSScriptRoot "refresh_shell_icons.ps1")
