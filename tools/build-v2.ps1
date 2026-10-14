param([string]$Version = '2.3.0')
$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
if ($Version -notmatch '^\d+\.\d+\.\d+$') { throw 'Use a numeric major.minor.patch version.' }
$sourceRoot = Join-Path $repoRoot ('build/v2-preview-source-' + (Get-Date -Format 'yyyyMMdd-HHmmss'))
New-Item -ItemType Directory -Path $sourceRoot -Force | Out-Null
Push-Location $repoRoot
try {
    # Include public source only; never copy private runtime files or build copies.
    $sourceFiles = & git ls-files --cached --others --exclude-standard
    if ($LASTEXITCODE -ne 0) { throw 'Cannot enumerate public repository source.' }
    foreach ($relative in $sourceFiles) {
        if ($relative -match '^outputs[/\\]') { continue }
        $destination = [IO.Path]::GetFullPath((Join-Path $sourceRoot $relative))
        if (-not $destination.StartsWith($sourceRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) { throw 'Source path escaped build directory.' }
        New-Item -ItemType Directory -Path (Split-Path -Parent $destination) -Force | Out-Null
        Copy-Item -LiteralPath (Join-Path $repoRoot $relative) -Destination $destination
    }
    $configPath = Join-Path $sourceRoot 'wails.json'
    $config = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
    $filename = "OpenCode_Odometer_OpenCodeV2_${Version}_preview.exe"
    $config.outputfilename = [IO.Path]::GetFileNameWithoutExtension($filename)
    $config.info.productVersion = $Version
    $config.info.productName = 'OpenCode Odometer for OpenCode V2'
    $config.info.comments = 'OpenCode V2 preview; tested against OpenCode 2.0.28'
    [IO.File]::WriteAllText($configPath, ($config | ConvertTo-Json -Depth 20), (New-Object Text.UTF8Encoding($false)))
    Push-Location $sourceRoot
    try {
        # Bindings are unchanged. Never launch the widget/installer while packaging.
        & wails build -tags opencode_v2 -o $filename -skipbindings -m -nosyncgomod -s -trimpath
        if ($LASTEXITCODE -ne 0) { throw 'V2 Wails build failed.' }
    } finally { Pop-Location }
    $outputDir = Join-Path $repoRoot 'build/v2-preview'
    New-Item -ItemType Directory -Path $outputDir -Force | Out-Null
    $output = Join-Path $outputDir $filename
    Copy-Item -LiteralPath (Join-Path $sourceRoot "build/bin/$filename") -Destination $output
    $checksum = (Get-FileHash -LiteralPath $output -Algorithm SHA256).Hash
    "$checksum  $filename" | Set-Content -LiteralPath ($output + '.sha256') -Encoding ascii
    Write-Output "Executable: $output"
    Write-Output "SHA256: $checksum"
} finally { Pop-Location }
