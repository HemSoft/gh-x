# perfection-audit.ps1 — Quick quality audit for gh-x
# Run from repo root: .\.agents\skills\perfection\scripts\perfection-audit.ps1
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Continue'

$repoRoot = git rev-parse --show-toplevel 2>$null
if ($repoRoot) { Set-Location $repoRoot }

Write-Host "`n=== go vet ===" -ForegroundColor Cyan
go vet ./... 2>&1
$vetOk = $LASTEXITCODE -eq 0

Write-Host "`n=== staticcheck ===" -ForegroundColor Cyan
$sc = Get-Command staticcheck -ErrorAction SilentlyContinue
if ($sc) {
    staticcheck ./... 2>&1
    $scOk = $LASTEXITCODE -eq 0
} else {
    Write-Host "(not installed — run: go install honnef.co/go/tools/cmd/staticcheck@latest)" -ForegroundColor Yellow
    $scOk = $null
}

Write-Host "`n=== test coverage ===" -ForegroundColor Cyan
go test -coverprofile=coverage.out ./... 2>&1
$testOk = $LASTEXITCODE -eq 0
if (Test-Path coverage.out) {
    go tool cover -func=coverage.out 2>&1
    Remove-Item coverage.out -ErrorAction SilentlyContinue
}

Write-Host "`n=== cyclomatic complexity (>5) ===" -ForegroundColor Cyan
$gc = Get-Command gocyclo -ErrorAction SilentlyContinue
if ($gc) {
    gocyclo -over 5 . 2>&1
} else {
    Write-Host "(not installed — run: go install github.com/fzipp/gocyclo/cmd/gocyclo@latest)" -ForegroundColor Yellow
}

Write-Host "`n=== cognitive complexity (>5) ===" -ForegroundColor Cyan
$gcog = Get-Command gocognit -ErrorAction SilentlyContinue
if ($gcog) {
    gocognit -over 5 . 2>&1
} else {
    Write-Host "(not installed — run: go install github.com/uudashr/gocognit/cmd/gocognit@latest)" -ForegroundColor Yellow
}

Write-Host "`n=== race detector ===" -ForegroundColor Cyan
go test -race -count=1 ./... 2>&1
$raceOk = $LASTEXITCODE -eq 0

Write-Host "`n=== Summary ===" -ForegroundColor Cyan
$status = @{
    "Vet"    = if ($vetOk)  { "✅ pass" } else { "❌ fail" }
    "Tests"  = if ($testOk) { "✅ pass" } else { "❌ fail" }
    "Race"   = if ($raceOk) { "✅ pass" } else { "❌ fail" }
}
if ($null -ne $scOk) {
    $status["Staticcheck"] = if ($scOk) { "✅ pass" } else { "❌ fail" }
}
$status.GetEnumerator() | Sort-Object Name | ForEach-Object {
    Write-Host "  $($_.Name): $($_.Value)"
}
Write-Host ""
