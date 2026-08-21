# Build, test, and install gh-x
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$tag = & git describe --tags --always 2>$null
if (-not $tag) { $tag = 'dev' }

Write-Host '--- vet ---'
go vet ./...

Write-Host '--- test ---'
go test ./...

Write-Host '--- build ---'
$date = Get-Date -Format 'yyyy-MM-dd'
go build -ldflags "-X main.version=$tag -X main.buildDate=$date" -o gh-x.exe ./src

# Install into gh extension directory so `gh x` uses the local build
$extDir = Join-Path $env:LOCALAPPDATA 'GitHub CLI\extensions\gh-x'
if (Test-Path $extDir -PathType Container) {
    Copy-Item gh-x.exe (Join-Path $extDir 'gh-x.exe') -Force
    Write-Host "`n✅ Built & installed ($tag) — run: gh x"
} elseif (Test-Path $extDir -PathType Leaf) {
    $linkedRoot = (Get-Content -LiteralPath $extDir -Raw).Trim()
    if (-not (Test-Path -LiteralPath $linkedRoot -PathType Container) -or
        (Resolve-Path -LiteralPath $linkedRoot).ProviderPath -ne $PSScriptRoot) {
        throw "gh-x local extension points to an unexpected path: $linkedRoot"
    }
    Write-Host "`n✅ Built linked extension ($tag) — run: gh x"
} else {
    Write-Host "`n✅ Built ($tag) — extension dir not found, run: gh extension install ."
}
