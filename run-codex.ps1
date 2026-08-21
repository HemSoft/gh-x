param(
    [string]$NodeCommand = "node",
    [string]$BrowserCommand,
    [int]$StartupTimeoutSeconds = 10
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$repositoryRoot = [System.IO.Path]::GetFullPath($PSScriptRoot).TrimEnd('\')
$dashboardEntry = Join-Path $repositoryRoot "src\codex-dashboard\main.mjs"
$userProfile = [Environment]::GetFolderPath("UserProfile")
$codexHome = if ($env:CODEX_HOME) {
    $env:CODEX_HOME
} else {
    Join-Path $userProfile ".codex"
}
$controlPath = Join-Path $codexHome "dashboard\control.json"

function Get-LiveDashboardUrl {
    if (-not (Test-Path -LiteralPath $controlPath -PathType Leaf)) {
        return $null
    }

    try {
        $control = Get-Content -LiteralPath $controlPath -Raw | ConvertFrom-Json
        $process = Get-Process -Id ([int]$control.pid) -ErrorAction SilentlyContinue
        if (-not $process) {
            Remove-Item -LiteralPath $controlPath -Force -ErrorAction SilentlyContinue
            return $null
        }

        $url = [string]$control.url
        $healthUrl = [System.Uri]::new([System.Uri]$url, "api/health").AbsoluteUri
        $health = Invoke-RestMethod -Method Get -Uri $healthUrl -TimeoutSec 1
        if ($health.ready -eq $true) {
            return $url
        }
    } catch {
        Remove-Item -LiteralPath $controlPath -Force -ErrorAction SilentlyContinue
    }

    return $null
}

function Resolve-DashboardBrowser {
    if ($BrowserCommand) {
        return (Get-Command -Name $BrowserCommand -ErrorAction Stop).Path
    }

    foreach ($command in @("msedge.exe", "chrome.exe")) {
        $browser = Get-Command -Name $command -ErrorAction SilentlyContinue
        if ($browser) {
            return $browser.Path
        }
    }

    $candidates = @()
    foreach ($root in @(
        ${env:ProgramFiles(x86)},
        $env:ProgramFiles,
        $env:LOCALAPPDATA
    )) {
        if (-not $root) {
            continue
        }
        $candidates += Join-Path $root "Microsoft\Edge\Application\msedge.exe"
        $candidates += Join-Path $root "Google\Chrome\Application\chrome.exe"
    }

    foreach ($candidate in $candidates) {
        if (Test-Path -LiteralPath $candidate -PathType Leaf) {
            return $candidate
        }
    }

    throw "Microsoft Edge or Google Chrome is required to open the dashboard in its own window."
}

$dashboardUrl = Get-LiveDashboardUrl
if (-not $dashboardUrl) {
    $node = Get-Command -Name $NodeCommand -ErrorAction Stop
    $serverProcess = Start-Process `
        -FilePath $node.Path `
        -ArgumentList @(
            "`"$dashboardEntry`"",
            "--control",
            "`"$controlPath`""
        ) `
        -WorkingDirectory $repositoryRoot `
        -WindowStyle Hidden `
        -PassThru

    $deadline = [DateTime]::UtcNow.AddSeconds($StartupTimeoutSeconds)
    do {
        if ($serverProcess.HasExited) {
            throw "The Codex dashboard server exited with code $($serverProcess.ExitCode)."
        }
        Start-Sleep -Milliseconds 100
        $dashboardUrl = Get-LiveDashboardUrl
    } while (-not $dashboardUrl -and [DateTime]::UtcNow -lt $deadline)

    if (-not $dashboardUrl) {
        Stop-Process -Id $serverProcess.Id -Force -ErrorAction SilentlyContinue
        throw "The Codex dashboard server did not become ready within $StartupTimeoutSeconds seconds."
    }
}

$browserPath = Resolve-DashboardBrowser
Start-Process `
    -FilePath $browserPath `
    -ArgumentList "--app=$dashboardUrl" `
    -WorkingDirectory $repositoryRoot | Out-Null
Write-Host "Opened the Codex Usage Dashboard in its own window."
