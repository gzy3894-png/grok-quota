# Build grok-quota CPA plugin shared library.
# Usage:
#   pwsh -File .\build.ps1                  # native host (Windows amd64 here)
#   pwsh -File .\build.ps1 -Target windows/amd64
#   pwsh -File .\build.ps1 -Target linux/amd64   # requires CGO cross toolchain
#   pwsh -File .\build.ps1 -Target all           # try common targets
[CmdletBinding()]
param(
    [string]$Target = ''
)

$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path -LiteralPath $PSScriptRoot).Path
$go = Get-Command go -ErrorAction Stop
$version = '0.1.16'

function Get-NativeTarget {
    $goos = (& $go.Source env GOOS).Trim()
    if ($LASTEXITCODE -ne 0) { throw "go env GOOS failed with exit $LASTEXITCODE" }
    $goarch = (& $go.Source env GOARCH).Trim()
    if ($LASTEXITCODE -ne 0) { throw "go env GOARCH failed with exit $LASTEXITCODE" }
    return "$goos/$goarch"
}

function Get-Ext([string]$goos) {
    switch ($goos) {
        'windows' { return '.dll' }
        'darwin' { return '.dylib' }
        default { return '.so' }
    }
}

function Build-One([string]$goos, [string]$goarch) {
    $env:CGO_ENABLED = '1'
    $env:GOOS = $goos
    $env:GOARCH = $goarch
    $ext = Get-Ext $goos
    $dist = Join-Path $repoRoot 'dist'
    New-Item -ItemType Directory -Force -Path $dist | Out-Null
    $name = "grok-quota$ext"
    $versioned = "grok-quota-v$version-$goos-$goarch$ext"
    $artifact = Join-Path $dist $name
    $versionedPath = Join-Path $dist $versioned
    # Platform subdir mirrors CPA plugins layout
    $platDir = Join-Path $dist (Join-Path $goos $goarch)
    New-Item -ItemType Directory -Force -Path $platDir | Out-Null
    $platArtifact = Join-Path $platDir $name

    Write-Output "Building $goos/$goarch -> $artifact"
    & $go.Source build -buildvcs=false -buildmode=c-shared -trimpath -ldflags='-s -w' -o $artifact .
    if ($LASTEXITCODE -ne 0) {
        throw "go build failed for $goos/$goarch with exit $LASTEXITCODE (CGO cross-compile may be missing)"
    }
    Copy-Item -LiteralPath $artifact -Destination $versionedPath -Force
    Copy-Item -LiteralPath $artifact -Destination $platArtifact -Force
    foreach ($headerName in @("grok-quota.h", "grok-quota-v$version.h")) {
        $header = Join-Path $dist $headerName
        if (Test-Path -LiteralPath $header) { Remove-Item -LiteralPath $header -Force }
        $ph = Join-Path $platDir $headerName
        if (Test-Path -LiteralPath $ph) { Remove-Item -LiteralPath $ph -Force }
    }
    $hash = (Get-FileHash -LiteralPath $artifact -Algorithm SHA256).Hash
    Write-Output "OK $versionedPath"
    Write-Output "SHA256: $hash"
}

Push-Location -LiteralPath $repoRoot
try {
    & $go.Source mod tidy
    if ($LASTEXITCODE -ne 0) { throw "go mod tidy failed with exit $LASTEXITCODE" }

    # Always run tests on the host (not under foreign GOOS).
    $savedGOOS = $env:GOOS
    $savedGOARCH = $env:GOARCH
    Remove-Item Env:GOOS -ErrorAction SilentlyContinue
    Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
    $env:CGO_ENABLED = '1'
    & $go.Source test ./...
    if ($LASTEXITCODE -ne 0) { throw "go test failed with exit $LASTEXITCODE" }
    if ($null -ne $savedGOOS) { $env:GOOS = $savedGOOS }
    if ($null -ne $savedGOARCH) { $env:GOARCH = $savedGOARCH }

    $native = Get-NativeTarget
    if ([string]::IsNullOrWhiteSpace($Target) -or $Target -eq 'native') {
        $Target = $native
    }

    $targets = @()
    if ($Target -eq 'all') {
        $targets = @('windows/amd64', 'linux/amd64', 'linux/arm64', 'darwin/amd64', 'darwin/arm64')
    } else {
        $targets = @($Target)
    }

    $ok = @()
    $fail = @()
    foreach ($t in $targets) {
        $parts = $t.Split('/')
        if ($parts.Count -ne 2) { throw "invalid target: $t (want goos/goarch)" }
        try {
            Build-One -goos $parts[0] -goarch $parts[1]
            $ok += $t
        } catch {
            $fail += "$t :: $($_.Exception.Message)"
            if ($Target -ne 'all') { throw }
            Write-Warning $_
        }
    }
    Write-Output "Built: $($ok -join ', ')"
    if ($fail.Count -gt 0) {
        Write-Output "Skipped/failed (install CGO cross toolchain or build on that OS with build.sh):"
        $fail | ForEach-Object { Write-Output "  - $_" }
    }
} finally {
    Pop-Location
    Remove-Item Env:GOOS -ErrorAction SilentlyContinue
    Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
}
