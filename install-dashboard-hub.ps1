param(
    [int]$Port = 4765,
    [string]$ServePath = "/dashboards",
    [string]$TaskName = "HemSoft CLI Dashboard Hub"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$repositoryRoot = [System.IO.Path]::GetFullPath($PSScriptRoot).TrimEnd('\')
$launcherPath = Join-Path $repositoryRoot "run-dashboard-hub.ps1"
$powerShellPath = (Get-Command powershell.exe -ErrorAction Stop).Path
$tailscalePath = (Get-Command tailscale.exe -ErrorAction Stop).Path
$existingTask = Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
if ($existingTask) {
    $expectedFragment = [System.IO.Path]::GetFileName($launcherPath)
    $existingArguments = ($existingTask.Actions | ForEach-Object Arguments) -join " "
    if ($existingArguments -notlike "*$expectedFragment*") {
        throw "Scheduled task '$TaskName' exists but does not run $launcherPath."
    }
}

& $launcherPath -Port $Port -NoBrowser

$action = New-ScheduledTaskAction `
    -Execute $powerShellPath `
    -Argument "-NoProfile -WindowStyle Hidden -ExecutionPolicy Bypass -File `"$launcherPath`" -Port $Port -NoBrowser" `
    -WorkingDirectory $repositoryRoot
$trigger = New-ScheduledTaskTrigger -AtLogOn -User "$env:USERDOMAIN\$env:USERNAME"
$principal = New-ScheduledTaskPrincipal `
    -UserId "$env:USERDOMAIN\$env:USERNAME" `
    -LogonType Interactive `
    -RunLevel Limited
$settings = New-ScheduledTaskSettingsSet `
    -AllowStartIfOnBatteries `
    -DontStopIfGoingOnBatteries `
    -ExecutionTimeLimit ([TimeSpan]::Zero) `
    -RestartCount 3 `
    -RestartInterval (New-TimeSpan -Minutes 1)
Register-ScheduledTask `
    -TaskName $TaskName `
    -Action $action `
    -Trigger $trigger `
    -Principal $principal `
    -Settings $settings `
    -Description "Starts the loopback-only Codex CLI and Copilot CLI dashboard hub." `
    -Force | Out-Null

& $tailscalePath serve --bg --set-path $ServePath "http://127.0.0.1:$Port"
& $tailscalePath serve --bg --yes --tcp=80 "tcp://127.0.0.1:$Port"
$tailnetIp = (& $tailscalePath ip -4 | Select-Object -First 1).Trim()

Write-Host "Installed scheduled task: $TaskName"
Write-Host "Published privately at: https://desktop-phubt5b.tail3280fc.ts.net$ServePath/"
Write-Host "DNS-independent fallback: http://$tailnetIp/"
