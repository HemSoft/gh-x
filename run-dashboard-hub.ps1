param(
    [int]$Port = 4765,
    [switch]$NoBrowser,
    [string]$NodeCommand = "node",
    [int]$StartupTimeoutSeconds = 10
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$repositoryRoot = [System.IO.Path]::GetFullPath($PSScriptRoot).TrimEnd('\')
$dashboardEntry = Join-Path $repositoryRoot "src\dashboard-hub\main.mjs"
$userProfilePath = [Environment]::GetFolderPath("UserProfile")
$controlDirectory = Join-Path $userProfilePath ".codex\dashboard-hub"
$controlPath = Join-Path $controlDirectory "control.json"
$localUrl = "http://127.0.0.1:$Port/"

function Test-DashboardHub {
    try {
        $health = Invoke-RestMethod -Method Get -Uri "${localUrl}api/health" -TimeoutSec 1
        return $health.ready -eq $true -and $health.service -eq "cli-dashboard-hub"
    } catch {
        return $false
    }
}

if (-not (Test-DashboardHub)) {
    New-Item -ItemType Directory -Path $controlDirectory -Force | Out-Null
    $node = Get-Command -Name $NodeCommand -ErrorAction Stop
    $serverProcess = Start-Process `
        -FilePath $node.Path `
        -ArgumentList @(
            "`"$dashboardEntry`"",
            "--port", $Port,
            "--control", "`"$controlPath`""
        ) `
        -WorkingDirectory $repositoryRoot `
        -WindowStyle Hidden `
        -RedirectStandardOutput (Join-Path $controlDirectory "stdout.log") `
        -RedirectStandardError (Join-Path $controlDirectory "stderr.log") `
        -PassThru

    $deadline = [DateTime]::UtcNow.AddSeconds($StartupTimeoutSeconds)
    do {
        if ($serverProcess.HasExited) {
            $errorLog = Get-Content -LiteralPath (Join-Path $controlDirectory "stderr.log") -Raw -ErrorAction SilentlyContinue
            throw "The dashboard hub exited with code $($serverProcess.ExitCode). $errorLog"
        }
        Start-Sleep -Milliseconds 100
    } while (-not (Test-DashboardHub) -and [DateTime]::UtcNow -lt $deadline)

    if (-not (Test-DashboardHub)) {
        Stop-Process -Id $serverProcess.Id -Force -ErrorAction SilentlyContinue
        throw "The dashboard hub did not become ready within $StartupTimeoutSeconds seconds."
    }
}

if (-not $NoBrowser) {
    Start-Process $localUrl | Out-Null
}

Write-Host "CLI dashboard hub ready at $localUrl"
