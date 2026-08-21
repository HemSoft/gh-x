$ErrorActionPreference = "Stop"

$copilotHome = if ($env:COPILOT_HOME) {
    $env:COPILOT_HOME
} else {
    Join-Path $HOME ".copilot"
}
$controlDirectory = Join-Path $copilotHome "extensions\copilot-spend\artifacts\canvas-controls"
$repositoryRoot = [System.IO.Path]::GetFullPath($PSScriptRoot).TrimEnd('\')
$controls = @()

if (Test-Path -LiteralPath $controlDirectory -PathType Container) {
    foreach ($file in Get-ChildItem -LiteralPath $controlDirectory -Filter "*.json" -File) {
        try {
            $control = Get-Content -LiteralPath $file.FullName -Raw | ConvertFrom-Json
            $controlRoot = [System.IO.Path]::GetFullPath([string]$control.workingDirectory).TrimEnd('\')
            $process = Get-Process -Id ([int]$control.pid) -ErrorAction SilentlyContinue
            if ($process -and $controlRoot -ieq $repositoryRoot) {
                $controls += [pscustomobject]@{
                    Endpoint = [string]$control.endpoint
                    UpdatedAt = [datetime]$control.updatedAt
                }
            } elseif (-not $process) {
                Remove-Item -LiteralPath $file.FullName -Force -ErrorAction SilentlyContinue
            }
        } catch {
            Remove-Item -LiteralPath $file.FullName -Force -ErrorAction SilentlyContinue
        }
    }
}

$control = $controls | Sort-Object UpdatedAt -Descending | Select-Object -First 1
if (-not $control) {
    throw "No running Copilot CLI session with the copilot-spend extension was found for $repositoryRoot."
}

Invoke-WebRequest -UseBasicParsing -Method Post -Uri $control.Endpoint | Out-Null
Write-Host "Opened the Copilot Session Dashboard in the current CLI session."
