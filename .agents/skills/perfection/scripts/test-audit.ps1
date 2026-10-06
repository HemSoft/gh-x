# Dependency-free regression tests, runnable on Windows and Ubuntu PowerShell 7.
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'perfection-audit.ps1')

function Assert-Equal {
    param($Actual, $Expected, [string]$Message)
    if ($Actual -cne $Expected) { throw "${Message}: expected '$Expected', got '$Actual'" }
}

function New-TestGate {
    param([string]$Id, [string[]]$Tools = @(), [string[]]$Needs = @())
    return [pscustomobject]@{ id = $Id; tools = $Tools; needs = $Needs; ci = @() }
}

$context = @{ Visited = [System.Collections.Generic.List[string]]::new() }
$inventory = [pscustomobject]@{ gates = @(
    (New-TestGate 'helper-tests'),
    (New-TestGate 'independent'),
    (New-TestGate 'dependent' -Needs @('helper-tests')),
    (New-TestGate 'missing-tool' -Tools @('unavailable-audit-fixture')),
    (New-TestGate 'network'),
    (New-TestGate 'after-block')
) }
$actions = @{
    'helper-tests' = { param($c) Invoke-CheckedCommand 'failing helper fixture' 'pwsh' @('-NoProfile', '-Command', 'exit 7') }
    'independent' = { param($c) $c.Visited.Add('independent') }
    'dependent' = { param($c) throw 'A failed prerequisite must prevent execution' }
    'missing-tool' = { param($c) throw 'A missing tool must prevent execution' }
    'network' = { param($c) Stop-AuditBlocked 'Fixture network unavailable' }
    'after-block' = { param($c) $c.Visited.Add('after-block') }
}
$results = @(Invoke-AuditPlan $inventory $actions $context -ToolAvailable { param($name) $name -ne 'unavailable-audit-fixture' })
Assert-Equal ($results.Status -join ',') 'fail,pass,blocked,blocked,blocked,pass' 'Independent gate reporting'
Assert-Equal ($context.Visited -join ',') 'independent,after-block' 'Continue after failures and blocks'
Assert-Equal (Get-AuditExitCode $results) 1 'Failed or blocked audits must exit nonzero'
Assert-Equal $results[0].Detail 'failing helper fixture failed with exit code 7.' 'Preserve native failure'
Assert-Equal $results[3].Detail 'Missing tools: unavailable-audit-fixture' 'Name missing tool'
Assert-Equal $results[4].Detail 'Fixture network unavailable' 'Name unavailable service'

$passing = @(Invoke-AuditPlan ([pscustomobject]@{ gates = @((New-TestGate 'ok')) }) @{ ok = {} } @{})
Assert-Equal (Get-AuditExitCode $passing) 0 'Successful plan'
Assert-Equal (Get-AuditExitCode @()) 1 'Empty plan is not qualification'
$blocked = @(Invoke-AuditPlan ([pscustomobject]@{ gates = @((New-TestGate 'missing' -Tools @('fixture'))) }) @{ missing = {} } @{} -ToolAvailable { $false })
Assert-Equal (Get-AuditExitCode $blocked) 1 'Blocked-only plan is not qualification'
$unknown = @(Invoke-AuditPlan ([pscustomobject]@{ gates = @((New-TestGate 'unknown')) }) @{} @{})
Assert-Equal $unknown[0].Status 'fail' 'Unimplemented gates fail closed'

$root = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../../../..'))
$declared = Get-Content (Join-Path $root '.github/local-quality-gates.json') -Raw | ConvertFrom-Json
$implementations = Get-AuditActions
Assert-Equal (($declared.gates.id | Sort-Object) -join ',') (($implementations.Keys | Sort-Object) -join ',') 'Inventory/implementation parity'
Assert-Equal (($declared.gates.id | Sort-Object -Unique).Count) $declared.gates.Count 'Unique gate IDs'
$seen = @{}
foreach ($gate in $declared.gates) {
    foreach ($dependency in $gate.needs) {
        Assert-Equal $seen.ContainsKey($dependency) $true "Prerequisite order for $($gate.id)"
    }
    $seen[$gate.id] = $true
}
foreach ($gate in $declared.hosted) {
    if ([string]::IsNullOrWhiteSpace($gate.reason)) { throw "Missing hosted reason: $($gate.step)" }
}
# Exercise the actual helper-test action in an isolated Go module. This catches
# regressions where go ./... silently omits the hidden helper packages again.
$fixture = Join-Path ([IO.Path]::GetTempPath()) "gh-x-audit-fixture-$([guid]::NewGuid().ToString('N'))"
New-Item -ItemType Directory -Path (Join-Path $fixture '.github/scripts') -Force | Out-Null
try {
    Set-Content (Join-Path $fixture 'go.mod') "module auditfixture`n`ngo 1.23.0`n" -Encoding utf8
    Set-Content (Join-Path $fixture '.github/scripts/failure_test.go') @'
package fixture

import "testing"

func TestReleaseHelperFailure(t *testing.T) {
	t.Fatal("intentional audit regression fixture")
}
'@ -Encoding utf8
    Push-Location $fixture
    try {
        Invoke-CheckedCommand 'fixture formatting' 'gofmt' @('-w', '.github/scripts/failure_test.go')
        $fixtureInventory = [pscustomobject]@{ gates = @(
            (New-TestGate 'helper-tests' -Tools @('go')),
            (New-TestGate 'format' -Tools @('gofmt'))
        ) }
        $fixtureResults = @(Invoke-AuditPlan $fixtureInventory $implementations @{ Root = $fixture })
        Assert-Equal ($fixtureResults.Status -join ',') 'fail,pass' 'Real helper failure preserves independent gate'
        Assert-Equal (Get-AuditExitCode $fixtureResults) 1 'Real failing helper exits nonzero'
        Set-Content (Join-Path $fixture 'main.go') "package main`nfunc main() {}`n" -Encoding utf8
        Set-Content (Join-Path $fixture '.github/scripts/main.go') "package main`nfunc main() {} `nfunc hidden() { _ = missingSymbol }`n" -Encoding utf8
        (Get-Content (Join-Path $fixture '.github/scripts/failure_test.go') -Raw).Replace('package fixture', 'package main') | Set-Content (Join-Path $fixture '.github/scripts/failure_test.go')
        $scopeContext = @{ Root = $fixture; Temp = $fixture; Coverage = (Join-Path $fixture 'scope-coverage.out') }
        $scopeInventory = [pscustomobject]@{ gates = @(
            (New-TestGate 'build' -Tools @('go')),
            (New-TestGate 'vet' -Tools @('go')),
            (New-TestGate 'unit-tests' -Tools @('go'))
        ) }
        $scopeResults = @(Invoke-AuditPlan $scopeInventory $implementations $scopeContext)
        Assert-Equal ($scopeResults.Status -join ',') 'fail,fail,fail' 'Hidden helper compile fixture fails all shared package gates'

    } finally {
        Pop-Location
    }
} finally {
    Remove-Item -LiteralPath $fixture -Recurse -Force
}
Write-Host 'Audit executor tests passed: failure, continuation, prerequisites, missing tools, service blocks, success and inventory.'

# Coverage must not leak between repeated helper main.go files or methods.
$collisionFindings = @(Get-CrapFindings @(
    '6 main run .github/scripts/first/main.go:10:1',
    '6 main run .github/scripts/second/main.go:10:1',
    '6 main (First).Load src/shared.go:20:1',
    '6 main (Second).Load src/shared.go:40:1'
) @(
    'auditfixture/.github/scripts/first/main.go:10: run 100.0%',
    'auditfixture/.github/scripts/second/main.go:10: run 0.0%',
    'auditfixture/src/shared.go:20: Load 100.0%',
    'auditfixture/src/shared.go:40: Load 0.0%',
    'total: (statements) 70.0%'
) 'auditfixture' 30.0)
Assert-Equal $collisionFindings.Count 2 'Full file and declaration identity must preserve uncovered functions'
Assert-Equal ($collisionFindings.File -join ',') '.github/scripts/second/main.go,src/shared.go' 'Never borrow same-named function coverage'
Assert-Equal ($collisionFindings.CRAP -join ',') '42,42' 'Uncovered fixture exceeds unchanged CRAP threshold'

# Actions propagates LASTEXITCODE from the intentional failing Go fixture.
# Reaching this point proves every assertion and required cleanup succeeded.
exit 0
