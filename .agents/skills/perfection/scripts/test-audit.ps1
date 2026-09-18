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
    } finally {
        Pop-Location
    }
} finally {
    Remove-Item -LiteralPath $fixture -Recurse -Force
}
Write-Host 'Audit executor tests passed: failure, continuation, prerequisites, missing tools, service blocks, success and inventory.'
