param(
  [Parameter(Mandatory = $true)]
  [string]$OutputSyso,
  [Parameter(Mandatory = $true)]
  [string]$Timestamp,
  [Parameter(Mandatory = $true)]
  [string]$ProductName,
  [Parameter(Mandatory = $true)]
  [string]$FileDescription,
  [Parameter(Mandatory = $true)]
  [string]$OriginalFilename
)

$ErrorActionPreference = "Stop"

if ($Timestamp -notmatch '^\d{14}$') {
  throw "Timestamp version must use yyyyMMddHHmmss digits only: $Timestamp"
}

$windres = Get-Command windres -ErrorAction SilentlyContinue
if (-not $windres) {
  throw "windres.exe is required to build Windows version resources"
}

$outDir = Split-Path -Parent $OutputSyso
New-Item -ItemType Directory -Force $outDir | Out-Null

$year = [int]$Timestamp.Substring(0, 4)
$monthDay = [int]$Timestamp.Substring(4, 4)
$hourMinute = [int]$Timestamp.Substring(8, 4)
$second = [int]$Timestamp.Substring(12, 2)

$rcPath = [System.IO.Path]::ChangeExtension($OutputSyso, ".rc")
$rc = @"
1 VERSIONINFO
FILEVERSION $year,$monthDay,$hourMinute,$second
PRODUCTVERSION $year,$monthDay,$hourMinute,$second
FILEFLAGSMASK 0x3fL
FILEFLAGS 0x0L
FILEOS 0x40004L
FILETYPE 0x1L
FILESUBTYPE 0x0L
BEGIN
  BLOCK "StringFileInfo"
  BEGIN
    BLOCK "040904B0"
    BEGIN
      VALUE "CompanyName", "OneKeyVE"
      VALUE "FileDescription", "$FileDescription"
      VALUE "FileVersion", "$Timestamp"
      VALUE "InternalName", "$ProductName"
      VALUE "OriginalFilename", "$OriginalFilename"
      VALUE "ProductName", "$ProductName"
      VALUE "ProductVersion", "$Timestamp"
    END
  END
  BLOCK "VarFileInfo"
  BEGIN
    VALUE "Translation", 0x0409, 1200
  END
END
"@

Set-Content -LiteralPath $rcPath -Value $rc -Encoding ASCII
try {
  & $windres.Source -O coff -o $OutputSyso $rcPath
} finally {
  Remove-Item -LiteralPath $rcPath -Force -ErrorAction SilentlyContinue
}
