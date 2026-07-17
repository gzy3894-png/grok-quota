$ErrorActionPreference = 'Stop'

$repoRoot = (Resolve-Path -LiteralPath $PSScriptRoot).Path
$go = Get-Command go -ErrorAction Stop
$env:CGO_ENABLED = '1'
$goos = (& $go.Source env GOOS).Trim()
if ($LASTEXITCODE -ne 0) { throw "go env GOOS failed with exit $LASTEXITCODE" }
$goarch = (& $go.Source env GOARCH).Trim()
if ($LASTEXITCODE -ne 0) { throw "go env GOARCH failed with exit $LASTEXITCODE" }
$cgo = (& $go.Source env CGO_ENABLED).Trim()
if ($LASTEXITCODE -ne 0) { throw "go env CGO_ENABLED failed with exit $LASTEXITCODE" }

if ($goos -ne 'windows' -or $goarch -ne 'amd64') {
    throw "This build entrypoint targets Windows amd64; detected GOOS=$goos GOARCH=$goarch"
}
if ($cgo -ne '1') {
    throw 'CGO_ENABLED must be 1; install a working C compiler and enable CGO.'
}

Push-Location -LiteralPath $repoRoot
try {
    & $go.Source mod tidy
    if ($LASTEXITCODE -ne 0) { throw "go mod tidy failed with exit $LASTEXITCODE" }

    & $go.Source test ./...
    if ($LASTEXITCODE -ne 0) { throw "go test failed with exit $LASTEXITCODE" }

    $dist = Join-Path $repoRoot 'dist'
    New-Item -ItemType Directory -Force -Path $dist | Out-Null
    $artifact = Join-Path $dist 'grok-quota.dll'
    $versioned = Join-Path $dist 'grok-quota-v0.1.9.dll'
    & $go.Source build -buildvcs=false -buildmode=c-shared -trimpath -ldflags='-s -w' -o $artifact .
    if ($LASTEXITCODE -ne 0) { throw "go build failed with exit $LASTEXITCODE" }
    Copy-Item -LiteralPath $artifact -Destination $versioned -Force

    foreach ($headerName in @('grok-quota.h', 'grok-quota-v0.1.9.h')) {
        $header = Join-Path $dist $headerName
        if (Test-Path -LiteralPath $header) {
            Remove-Item -LiteralPath $header -Force
        }
    }
    $hash = (Get-FileHash -LiteralPath $artifact -Algorithm SHA256).Hash
    $info = Get-Item -LiteralPath $artifact
    Write-Output "Built $($info.FullName)"
    Write-Output "Versioned: $versioned"
    Write-Output "Size: $($info.Length) bytes"
    Write-Output "SHA256: $hash"
} finally {
    Pop-Location
}


