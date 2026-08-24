# Run every gh-x quality gate from the repository root.
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Show-Section {
    param([Parameter(Mandatory)][string]$Name)

    Write-Host "`n=== $Name ===" -ForegroundColor Cyan
}

function Invoke-CheckedCommand {
    param(
        [Parameter(Mandatory)][string]$Name,
        [Parameter(Mandatory)][string]$Executable,
        [Parameter()][string[]]$Arguments = @()
    )

    Show-Section $Name
    & $Executable @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$Name failed with exit code $LASTEXITCODE."
    }
}

function Invoke-NoOutputCommand {
    param(
        [Parameter(Mandatory)][string]$Name,
        [Parameter(Mandatory)][string]$Executable,
        [Parameter()][string[]]$Arguments = @()
    )

    Show-Section $Name
    $output = @(& $Executable @Arguments 2>&1)
    $exitCode = $LASTEXITCODE
    if ($output.Count -gt 0) {
        $output | ForEach-Object { Write-Host $_ }
    }
    if ($exitCode -ne 0) {
        throw "$Name failed with exit code $exitCode."
    }
    if ($output.Count -gt 0) {
        throw "$Name produced findings."
    }
}

function Assert-NoSourceMatches {
    param(
        [Parameter(Mandatory)][string]$Name,
        [Parameter(Mandatory)][System.IO.FileInfo[]]$Files,
        [Parameter(Mandatory)][string]$Pattern
    )

    Show-Section $Name
    $matches = @(Select-String -LiteralPath $Files.FullName -Pattern $Pattern)
    if ($matches.Count -gt 0) {
        $matches | ForEach-Object { Write-Host $_ }
        throw "$Name produced findings."
    }
}

function Assert-PinnedToolVersions {
    param([Parameter(Mandatory)][string]$VersionFile)

    Show-Section 'pinned analyzer versions'
    $pins = @{}
    foreach ($line in Get-Content -LiteralPath $VersionFile) {
        if ($line -match '^(?<name>[A-Z0-9_]+)=(?<version>\S+)$') {
            $pins[$Matches.name] = $Matches.version
        }
    }

    $tools = @(
        @{ Name = 'staticcheck'; Module = 'honnef.co/go/tools'; Pin = 'STATICCHECK_VERSION' },
        @{ Name = 'gocritic'; Module = 'github.com/go-critic/go-critic'; Pin = 'GOCRITIC_VERSION' },
        @{ Name = 'errcheck'; Module = 'github.com/kisielk/errcheck'; Pin = 'ERRCHECK_VERSION' },
        @{ Name = 'deadcode'; Module = 'golang.org/x/tools'; Pin = 'DEADCODE_VERSION' },
        @{ Name = 'govulncheck'; Module = 'golang.org/x/vuln'; Pin = 'GOVULNCHECK_VERSION' },
        @{ Name = 'gocyclo'; Module = 'github.com/fzipp/gocyclo'; Pin = 'GOCYCLO_VERSION' },
        @{ Name = 'gocognit'; Module = 'github.com/uudashr/gocognit'; Pin = 'GOCOGNIT_VERSION' },
        @{ Name = 'gremlins'; Module = 'github.com/go-gremlins/gremlins'; Pin = 'GREMLINS_VERSION' }
    )

    foreach ($tool in $tools) {
        $expected = $pins[$tool.Pin]
        if (-not $expected) {
            throw "Missing $($tool.Pin) in $VersionFile."
        }

        $commandPath = (Get-Command $tool.Name -ErrorAction Stop).Source
        $metadata = @(& go version -m $commandPath 2>&1)
        if ($LASTEXITCODE -ne 0) {
            $metadata | ForEach-Object { Write-Host $_ }
            throw "Could not inspect $($tool.Name)."
        }

        $modulePattern = '^\s*mod\s+' + [regex]::Escape($tool.Module) + '\s+(?<version>\S+)'
        $moduleLine = $metadata | Where-Object { [string]$_ -match $modulePattern } | Select-Object -First 1
        if (-not $moduleLine -or [string]$moduleLine -notmatch $modulePattern) {
            throw "$($tool.Name) was not built from $($tool.Module)."
        }

        $actual = $Matches.version
        if ($actual -ne $expected) {
            throw "$($tool.Name) is $actual; expected $expected from .github/quality-tools.env."
        }
        Write-Host "$($tool.Name) $actual"
    }

    return $pins
}

function Assert-CrapThreshold {
    param(
        [Parameter(Mandatory)][string]$CoveragePath,
        [Parameter(Mandatory)][double]$Threshold
    )

    Show-Section "CRAP score < $Threshold"

    $complexityLines = @(& gocyclo -ignore '_test\.go' . 2>&1)
    if ($LASTEXITCODE -ne 0) {
        $complexityLines | ForEach-Object { Write-Host $_ }
        throw 'gocyclo failed while calculating CRAP scores.'
    }

    $coverageLines = @(& go tool cover "-func=$CoveragePath" 2>&1)
    if ($LASTEXITCODE -ne 0) {
        $coverageLines | ForEach-Object { Write-Host $_ }
        throw 'go tool cover failed while calculating CRAP scores.'
    }

    $coverageByFunction = @{}
    foreach ($line in $coverageLines) {
        $text = [string]$line
        if ($text -match '^\S+/(?<file>[^/:]+\.go):\d+:\s+(?<function>\S+)\s+(?<coverage>[0-9.]+)%$') {
            $coverageByFunction["$($Matches.file)|$($Matches.function)"] = [double]$Matches.coverage
        }
    }

    $failures = @()
    foreach ($line in $complexityLines) {
        $parts = ([string]$line -split '\s+')
        if ($parts.Count -lt 4) {
            continue
        }

        $complexity = 0
        if (-not [int]::TryParse($parts[0], [ref]$complexity)) {
            continue
        }

        $functionName = $parts[2]
        if ($functionName -match '^\([^)]+\)\.(.+)$') {
            $functionName = $Matches[1]
        } elseif ($functionName.Contains('.')) {
            $functionName = $functionName.Split('.', 2)[1]
        }

        $fileName = [System.IO.Path]::GetFileName(($parts[3] -split ':')[0])
        $key = "$fileName|$functionName"
        $coverage = if ($coverageByFunction.ContainsKey($key)) {
            [double]$coverageByFunction[$key]
        } else {
            0.0
        }
        $uncovered = 1.0 - ($coverage / 100.0)
        $crap = ([math]::Pow($complexity, 2) * [math]::Pow($uncovered, 3)) + $complexity
        if ($crap -ge $Threshold) {
            $failures += [pscustomobject]@{
                CRAP = [math]::Round($crap, 1)
                Complexity = $complexity
                Coverage = $coverage
                Function = $functionName
                File = $fileName
            }
        }
    }

    if ($failures.Count -gt 0) {
        $failures | Sort-Object CRAP -Descending | Format-Table -AutoSize | Out-Host
        throw "$($failures.Count) function(s) have CRAP score >= $Threshold."
    }

    Write-Host "All functions have CRAP score below $Threshold."
}

$repoRoot = (& git rev-parse --show-toplevel 2>$null).Trim()
if (-not $repoRoot -or $LASTEXITCODE -ne 0) {
    throw 'Run this script from a gh-x worktree.'
}
Set-Location $repoRoot

$requiredTools = @(
    'go',
    'gofmt',
    'node',
    'npx',
    'staticcheck',
    'gocritic',
    'errcheck',
    'deadcode',
    'govulncheck',
    'gocyclo',
    'gocognit',
    'gremlins'
)
$missingTools = @($requiredTools | Where-Object { -not (Get-Command $_ -ErrorAction SilentlyContinue) })
if ($missingTools.Count -gt 0) {
    throw "Missing required quality tools: $($missingTools -join ', '). See .github/quality-tools.env for the pinned versions."
}
$qualityToolPins = Assert-PinnedToolVersions (Join-Path $repoRoot '.github\quality-tools.env')
$markdownlintVersion = $qualityToolPins['MARKDOWNLINT_CLI2_VERSION']
if (-not $markdownlintVersion) {
    throw 'Missing MARKDOWNLINT_CLI2_VERSION in .github/quality-tools.env.'
}

$tempRoot = [System.IO.Path]::GetTempPath()
$coveragePath = Join-Path $tempRoot "gh-x-coverage-$([guid]::NewGuid().ToString('N')).out"
$buildPath = Join-Path $tempRoot "gh-x-build-$([guid]::NewGuid().ToString('N')).exe"

try {
    Invoke-CheckedCommand 'build' 'go' @('build', '-o', $buildPath, './...')
    Invoke-CheckedCommand 'vet' 'go' @('vet', './...')
    Invoke-NoOutputCommand 'formatting (gofmt)' 'gofmt' @('-l', '.')
    Invoke-CheckedCommand 'module tidiness' 'go' @('mod', 'tidy', '-diff')
    Invoke-CheckedCommand 'staticcheck' 'staticcheck' @('./...')
    Invoke-CheckedCommand 'gocritic' 'gocritic' @('check', './...')
    Invoke-CheckedCommand 'errcheck' 'errcheck' @('-exclude', '.errcheck_excludes', './...')
    Invoke-NoOutputCommand 'dead code' 'deadcode' @('./...')
    Invoke-CheckedCommand 'vulnerability scan' 'govulncheck' @('./...')

    $goFiles = @(Get-ChildItem -LiteralPath $repoRoot -Recurse -File -Filter '*.go')
    $productionGoFiles = @($goFiles | Where-Object { $_.Name -notlike '*_test.go' })
    Assert-NoSourceMatches 'lint suppressions' $goFiles '//nolint|//lint:ignore|//nosec|#nosec'
    Assert-NoSourceMatches 'fmt.Print production calls' $productionGoFiles 'fmt\.Print'

    Invoke-CheckedCommand 'race tests and coverage' 'go' @('test', '-race', '-count=1', "-coverprofile=$coveragePath", './...')
    Invoke-CheckedCommand 'Node dashboard tests' 'node' @('--test', 'src/codex-dashboard/*.test.mjs', 'src/dashboard-hub/*.test.mjs')

    Show-Section 'coverage >= 70%'
    $coverageLines = @(& go tool cover "-func=$coveragePath" 2>&1)
    if ($LASTEXITCODE -ne 0) {
        $coverageLines | ForEach-Object { Write-Host $_ }
        throw 'go tool cover failed.'
    }
    $coverageLines | ForEach-Object { Write-Host $_ }
    $totalLine = $coverageLines | Where-Object { [string]$_ -match '^total:' } | Select-Object -Last 1
    if (-not $totalLine -or [string]$totalLine -notmatch '([0-9.]+)%') {
        throw 'Could not parse total coverage.'
    }
    $totalCoverage = [double]$Matches[1]
    if ($totalCoverage -lt 70.0) {
        throw "Total coverage $totalCoverage% is below 70%."
    }

    Invoke-NoOutputCommand 'cyclomatic complexity <= 10' 'gocyclo' @('-over', '10', '-ignore', '_test\.go', '.')
    Invoke-NoOutputCommand 'cognitive complexity <= 15' 'gocognit' @('-over', '15', '-ignore', '_test\.go', '.')
    Assert-CrapThreshold $coveragePath 30.0
    Invoke-CheckedCommand 'Markdown lint' 'npx' @('--yes', "markdownlint-cli2@$markdownlintVersion", '**/*.md', '#node_modules', '#.agents', '#.github/agents')

    Show-Section 'mutation efficacy >= 90%'
    $mutationOutput = @(& gremlins unleash --timeout-coefficient 10 --threshold-efficacy 90 ./src 2>&1)
    $mutationExitCode = $LASTEXITCODE
    $mutationOutput | ForEach-Object { Write-Host $_ }
    if ($mutationExitCode -ne 0) {
        throw "Mutation testing failed with exit code $mutationExitCode."
    }
    if (($mutationOutput -join "`n").Contains('No results to report')) {
        throw 'Mutation testing produced no results.'
    }

    Write-Host "`nAll quality gates passed." -ForegroundColor Green
} finally {
    Remove-Item -LiteralPath $coveragePath, $buildPath -Force -ErrorAction SilentlyContinue
}
