param(
  [string]$SourceImage,
  [string]$OutputIcon = (Join-Path (Split-Path -Parent $PSScriptRoot) "assets\app.ico")
)

$ErrorActionPreference = "Stop"

$projectRoot = Split-Path -Parent $PSScriptRoot

if ([string]::IsNullOrWhiteSpace($SourceImage)) {
  $source = Get-ChildItem -Path $projectRoot -File |
    Where-Object { $_.Extension.ToLowerInvariant() -in @('.png', '.jpg', '.jpeg', '.bmp', '.webp') } |
    Sort-Object LastWriteTime -Descending |
    Select-Object -First 1
  if ($null -eq $source) {
    throw "No source image found in $projectRoot"
  }
  $SourceImage = $source.FullName
}

if (-not (Test-Path $SourceImage)) {
  throw "Source image not found: $SourceImage"
}

New-Item -ItemType Directory -Force (Split-Path -Parent $OutputIcon) | Out-Null

Add-Type -AssemblyName System.Drawing

function New-IconEntry {
  param(
    [byte]$Width,
    [byte]$Height,
    [byte[]]$Data
  )

  [PSCustomObject]@{
    Width  = $Width
    Height = $Height
    Data   = $Data
  }
}

$sizes = @(16, 24, 32, 48, 64, 128, 256)

$sourceImageObject = [System.Drawing.Image]::FromFile((Resolve-Path $SourceImage))
try {
  $cropSize = [Math]::Min($sourceImageObject.Width, $sourceImageObject.Height)
  $srcX = [int](($sourceImageObject.Width - $cropSize) / 2)
  $srcY = [int](($sourceImageObject.Height - $cropSize) / 2)

  $square = New-Object System.Drawing.Bitmap($cropSize, $cropSize)
  try {
    $graphics = [System.Drawing.Graphics]::FromImage($square)
    try {
      $graphics.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
      $graphics.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::HighQuality
      $graphics.PixelOffsetMode = [System.Drawing.Drawing2D.PixelOffsetMode]::HighQuality
      $graphics.CompositingQuality = [System.Drawing.Drawing2D.CompositingQuality]::HighQuality
      $graphics.DrawImage(
        $sourceImageObject,
        [System.Drawing.Rectangle]::FromLTRB(0, 0, $cropSize, $cropSize),
        $srcX,
        $srcY,
        $cropSize,
        $cropSize,
        [System.Drawing.GraphicsUnit]::Pixel
      )
    } finally {
      $graphics.Dispose()
    }

    $entries = New-Object System.Collections.Generic.List[object]
    foreach ($size in $sizes) {
      $bitmap = New-Object System.Drawing.Bitmap($size, $size)
      try {
        $graphics = [System.Drawing.Graphics]::FromImage($bitmap)
        try {
          $graphics.Clear([System.Drawing.Color]::Transparent)
          $graphics.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
          $graphics.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::HighQuality
          $graphics.PixelOffsetMode = [System.Drawing.Drawing2D.PixelOffsetMode]::HighQuality
          $graphics.CompositingQuality = [System.Drawing.Drawing2D.CompositingQuality]::HighQuality
          $graphics.DrawImage($square, 0, 0, $size, $size)
        } finally {
          $graphics.Dispose()
        }

        $stream = New-Object System.IO.MemoryStream
        try {
          $bitmap.Save($stream, [System.Drawing.Imaging.ImageFormat]::Png)
          $widthByte = if ($size -ge 256) { [byte]0 } else { [byte]$size }
          $heightByte = if ($size -ge 256) { [byte]0 } else { [byte]$size }
          $entries.Add((New-IconEntry -Width $widthByte -Height $heightByte -Data $stream.ToArray()))
        } finally {
          $stream.Dispose()
        }
      } finally {
        $bitmap.Dispose()
      }
    }

    $fileStream = [System.IO.File]::Create($OutputIcon)
    try {
      $writer = New-Object System.IO.BinaryWriter($fileStream)
      try {
        $writer.Write([UInt16]0)
        $writer.Write([UInt16]1)
        $writer.Write([UInt16]$entries.Count)

        $offset = 6 + (16 * $entries.Count)
        foreach ($entry in $entries) {
          $writer.Write([byte]$entry.Width)
          $writer.Write([byte]$entry.Height)
          $writer.Write([byte]0)
          $writer.Write([byte]0)
          $writer.Write([UInt16]1)
          $writer.Write([UInt16]32)
          $writer.Write([UInt32]$entry.Data.Length)
          $writer.Write([UInt32]$offset)
          $offset += $entry.Data.Length
        }

        foreach ($entry in $entries) {
          $writer.Write($entry.Data)
        }
      } finally {
        $writer.Dispose()
      }
    } finally {
      $fileStream.Dispose()
    }
  } finally {
    $square.Dispose()
  }
} finally {
  $sourceImageObject.Dispose()
}
