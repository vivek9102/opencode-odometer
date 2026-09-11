# Generates the application icon: an odometer/gauge dial.
#
# The project previously shipped the stock Wails logo as build/appicon.png,
# which is why the taskbar and window showed a "W". The build pipeline was
# correct all along - only the source artwork was the default.
#
# Produces build/appicon.png (1024) and build/windows/icon.ico (multi-size).

Add-Type -AssemblyName System.Drawing

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$pngPath = Join-Path $root "build\appicon.png"
$icoPath = Join-Path $root "build\windows\icon.ico"

function New-IconBitmap([int]$S) {
  $bmp = New-Object System.Drawing.Bitmap($S, $S, [System.Drawing.Imaging.PixelFormat]::Format32bppArgb)
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $g.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::AntiAlias
  $g.Clear([System.Drawing.Color]::Transparent)

  $pad = $S * 0.045
  $d = $S - (2 * $pad)

  # Rounded dark body, matching the UI panel colour.
  $r = $S * 0.22
  $path = New-Object System.Drawing.Drawing2D.GraphicsPath
  $path.AddArc($pad, $pad, $r, $r, 180, 90)
  $path.AddArc($pad + $d - $r, $pad, $r, $r, 270, 90)
  $path.AddArc($pad + $d - $r, $pad + $d - $r, $r, $r, 0, 90)
  $path.AddArc($pad, $pad + $d - $r, $r, $r, 90, 90)
  $path.CloseFigure()

  $bg = New-Object System.Drawing.Drawing2D.LinearGradientBrush(
    (New-Object System.Drawing.Point(0, 0)),
    (New-Object System.Drawing.Point($S, $S)),
    [System.Drawing.Color]::FromArgb(255, 32, 32, 40),
    [System.Drawing.Color]::FromArgb(255, 12, 12, 15))
  $g.FillPath($bg, $path)

  $edge = New-Object System.Drawing.Pen([System.Drawing.Color]::FromArgb(255, 62, 62, 74), [float]($S * 0.016))
  $g.DrawPath($edge, $path)

  # Gauge arc: 180deg sweep, green through amber to red - the same language
  # the odometer bar uses for spend.
  $cx = $S / 2.0
  $cy = $S * 0.60
  $rad = $S * 0.30
  $arcW = $S * 0.085
  $rect = New-Object System.Drawing.RectangleF(($cx - $rad), ($cy - $rad), (2 * $rad), (2 * $rad))

  $track = New-Object System.Drawing.Pen([System.Drawing.Color]::FromArgb(255, 45, 45, 55), [float]$arcW)
  $track.StartCap = [System.Drawing.Drawing2D.LineCap]::Round
  $track.EndCap = [System.Drawing.Drawing2D.LineCap]::Round
  $g.DrawArc($track, $rect, 180, 180)

  $segs = @(
    @{ start = 180; sweep = 96; col = [System.Drawing.Color]::FromArgb(255, 34, 197, 94) },
    @{ start = 276; sweep = 46; col = [System.Drawing.Color]::FromArgb(255, 245, 158, 11) },
    @{ start = 322; sweep = 38; col = [System.Drawing.Color]::FromArgb(255, 239, 68, 68) }
  )
  foreach ($seg in $segs) {
    $pen = New-Object System.Drawing.Pen($seg.col, [float]$arcW)
    $pen.StartCap = [System.Drawing.Drawing2D.LineCap]::Flat
    $pen.EndCap = [System.Drawing.Drawing2D.LineCap]::Flat
    $g.DrawArc($pen, $rect, $seg.start, $seg.sweep)
    $pen.Dispose()
  }

  # Needle pointing into the amber band: a meter in use, not at rest.
  $ang = 208.0 * [Math]::PI / 180.0
  $nx = $cx + ($rad * 0.80) * [Math]::Cos($ang)
  $ny = $cy + ($rad * 0.80) * [Math]::Sin($ang)
  $needle = New-Object System.Drawing.Pen([System.Drawing.Color]::White, [float]($S * 0.038))
  $needle.StartCap = [System.Drawing.Drawing2D.LineCap]::Round
  $needle.EndCap = [System.Drawing.Drawing2D.LineCap]::Round
  $g.DrawLine($needle, [float]$cx, [float]$cy, [float]$nx, [float]$ny)

  $hubR = $S * 0.055
  $hub = New-Object System.Drawing.SolidBrush([System.Drawing.Color]::White)
  $g.FillEllipse($hub, [float]($cx - $hubR), [float]($cy - $hubR), [float](2 * $hubR), [float](2 * $hubR))

  # Dollar mark above the dial: this meter measures money.
  $fontSize = $S * 0.26
  $font = New-Object System.Drawing.Font("Segoe UI", [float]$fontSize, [System.Drawing.FontStyle]::Bold,
    [System.Drawing.GraphicsUnit]::Pixel)
  $fmt = New-Object System.Drawing.StringFormat
  $fmt.Alignment = [System.Drawing.StringAlignment]::Center
  $fmt.LineAlignment = [System.Drawing.StringAlignment]::Center
  $green = New-Object System.Drawing.SolidBrush([System.Drawing.Color]::FromArgb(255, 34, 197, 94))
  $g.DrawString("`$", $font, $green, (New-Object System.Drawing.RectangleF(0, [float]($S * 0.10), [float]$S, [float]($S * 0.30))), $fmt)

  $g.Dispose()
  return $bmp
}

# --- PNG (1024) -------------------------------------------------------------
$big = New-IconBitmap 1024
$big.Save($pngPath, [System.Drawing.Imaging.ImageFormat]::Png)
$big.Dispose()
Write-Host "wrote $pngPath"

# --- ICO (multi-resolution) -------------------------------------------------
# Written by hand: System.Drawing cannot save a multi-image .ico. Each entry is
# a full PNG, which the ICO format has allowed since Vista.
$sizes = @(256, 128, 64, 48, 32, 24, 16)
$images = @()
foreach ($sz in $sizes) {
  $bmp = New-IconBitmap $sz
  $ms = New-Object System.IO.MemoryStream
  $bmp.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png)
  $images += , @{ size = $sz; bytes = $ms.ToArray() }
  $ms.Dispose(); $bmp.Dispose()
}

$fs = [System.IO.File]::Create($icoPath)
$bw = New-Object System.IO.BinaryWriter($fs)
$bw.Write([UInt16]0)                 # reserved
$bw.Write([UInt16]1)                 # type: icon
$bw.Write([UInt16]$images.Count)

$offset = 6 + (16 * $images.Count)
foreach ($img in $images) {
  $dim = if ($img.size -ge 256) { 0 } else { $img.size }
  $bw.Write([Byte]$dim)              # width  (0 => 256)
  $bw.Write([Byte]$dim)              # height
  $bw.Write([Byte]0)                 # palette count
  $bw.Write([Byte]0)                 # reserved
  $bw.Write([UInt16]1)               # colour planes
  $bw.Write([UInt16]32)              # bits per pixel
  $bw.Write([UInt32]$img.bytes.Length)
  $bw.Write([UInt32]$offset)
  $offset += $img.bytes.Length
}
foreach ($img in $images) { $bw.Write($img.bytes) }
$bw.Flush(); $bw.Close(); $fs.Close()
Write-Host "wrote $icoPath ($($images.Count) sizes)"
