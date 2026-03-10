$ErrorActionPreference = "SilentlyContinue"

Add-Type @"
using System;
using System.Runtime.InteropServices;

public static class ShellRefresh
{
    [DllImport("shell32.dll")]
    public static extern void SHChangeNotify(uint wEventId, uint uFlags, IntPtr dwItem1, IntPtr dwItem2);
}
"@

$ie4uinit = Get-Command ie4uinit.exe -ErrorAction SilentlyContinue
if ($null -ne $ie4uinit) {
  & $ie4uinit.Source -ClearIconCache | Out-Null
  & $ie4uinit.Source -show | Out-Null
}

[ShellRefresh]::SHChangeNotify(0x08000000, 0x0000, [IntPtr]::Zero, [IntPtr]::Zero)
