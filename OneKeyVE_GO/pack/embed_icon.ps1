param(
  [Parameter(Mandatory = $true)]
  [string]$ExePath,
  [Parameter(Mandatory = $true)]
  [string]$IconPath
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path $ExePath)) {
  throw "Target exe not found: $ExePath"
}
if (-not (Test-Path $IconPath)) {
  throw "Icon file not found: $IconPath"
}

Add-Type @"
using System;
using System.Runtime.InteropServices;

public static class NativeResources
{
    [DllImport("kernel32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
    public static extern IntPtr BeginUpdateResource(string fileName, bool deleteExistingResources);

    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    public static extern bool UpdateResource(
        IntPtr updateHandle,
        IntPtr type,
        IntPtr name,
        ushort language,
        byte[] data,
        uint dataSize
    );

    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    public static extern bool EndUpdateResource(IntPtr updateHandle, bool discard);
}
"@

function Set-UInt16 {
  param([byte[]]$Buffer, [int]$Offset, [UInt16]$Value)
  $bytes = [BitConverter]::GetBytes($Value)
  [Array]::Copy($bytes, 0, $Buffer, $Offset, 2)
}

function Set-UInt32 {
  param([byte[]]$Buffer, [int]$Offset, [UInt32]$Value)
  $bytes = [BitConverter]::GetBytes($Value)
  [Array]::Copy($bytes, 0, $Buffer, $Offset, 4)
}

$iconBytes = [System.IO.File]::ReadAllBytes((Resolve-Path $IconPath))
$reserved = [BitConverter]::ToUInt16($iconBytes, 0)
$iconType = [BitConverter]::ToUInt16($iconBytes, 2)
$count = [BitConverter]::ToUInt16($iconBytes, 4)

if ($reserved -ne 0 -or $iconType -ne 1 -or $count -lt 1) {
  throw "Invalid ICO file: $IconPath"
}

$entries = @()
for ($i = 0; $i -lt $count; $i++) {
  $offset = 6 + ($i * 16)
  $entries += [PSCustomObject]@{
    Width      = $iconBytes[$offset + 0]
    Height     = $iconBytes[$offset + 1]
    ColorCount = $iconBytes[$offset + 2]
    Reserved   = $iconBytes[$offset + 3]
    Planes     = [BitConverter]::ToUInt16($iconBytes, $offset + 4)
    BitCount   = [BitConverter]::ToUInt16($iconBytes, $offset + 6)
    BytesInRes = [BitConverter]::ToUInt32($iconBytes, $offset + 8)
    ImageOffset = [BitConverter]::ToUInt32($iconBytes, $offset + 12)
    ResourceID = [UInt16]($i + 1)
  }
}

$groupBytes = New-Object byte[] (6 + ($count * 14))
Set-UInt16 $groupBytes 0 0
Set-UInt16 $groupBytes 2 1
Set-UInt16 $groupBytes 4 $count

for ($i = 0; $i -lt $entries.Count; $i++) {
  $entry = $entries[$i]
  $offset = 6 + ($i * 14)
  $groupBytes[$offset + 0] = $entry.Width
  $groupBytes[$offset + 1] = $entry.Height
  $groupBytes[$offset + 2] = $entry.ColorCount
  $groupBytes[$offset + 3] = $entry.Reserved
  Set-UInt16 $groupBytes ($offset + 4) $entry.Planes
  Set-UInt16 $groupBytes ($offset + 6) $entry.BitCount
  Set-UInt32 $groupBytes ($offset + 8) $entry.BytesInRes
  Set-UInt16 $groupBytes ($offset + 12) $entry.ResourceID
}

$updateHandle = [NativeResources]::BeginUpdateResource((Resolve-Path $ExePath), $false)
if ($updateHandle -eq [IntPtr]::Zero) {
  throw "BeginUpdateResource failed: $([Runtime.InteropServices.Marshal]::GetLastWin32Error())"
}

$commit = $false
try {
  foreach ($entry in $entries) {
    $imageBytes = New-Object byte[] $entry.BytesInRes
    [Array]::Copy($iconBytes, [int]$entry.ImageOffset, $imageBytes, 0, [int]$entry.BytesInRes)

    $updated = [NativeResources]::UpdateResource(
      $updateHandle,
      [IntPtr]3,
      [IntPtr]$entry.ResourceID,
      [UInt16]0,
      $imageBytes,
      [UInt32]$imageBytes.Length
    )
    if (-not $updated) {
      throw "UpdateResource RT_ICON failed: $([Runtime.InteropServices.Marshal]::GetLastWin32Error())"
    }
  }

  $groupUpdated = [NativeResources]::UpdateResource(
    $updateHandle,
    [IntPtr]14,
    [IntPtr]1,
    [UInt16]0,
    $groupBytes,
    [UInt32]$groupBytes.Length
  )
  if (-not $groupUpdated) {
    throw "UpdateResource RT_GROUP_ICON failed: $([Runtime.InteropServices.Marshal]::GetLastWin32Error())"
  }

  $commit = $true
} finally {
  [void][NativeResources]::EndUpdateResource($updateHandle, (-not $commit))
}
