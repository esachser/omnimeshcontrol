#Requires -Version 5.1
<#
.SYNOPSIS
    Starts the full local development environment for Omnimesh.

.DESCRIPTION
    Starts the following components as background PowerShell Jobs:
    - etcd (standalone)
    - SPIRE Server (sqlite database)
    - SPIRE Agent (with dynamically generated join token)
    - CoreDNS (with etcd backend)

    Press Enter to stop all components cleanly.

.PARAMETER EtcdBin
    Path to etcd. Defaults to "etcd".

.PARAMETER SpireServerBin
    Path to spire-server. Defaults to "spire-server".

.PARAMETER SpireAgentBin
    Path to spire-agent. Defaults to "spire-agent".

.PARAMETER CoreDNSBin
    Path to coredns. Defaults to "coredns".

.PARAMETER LogLevel
    Log level for etcd, SPIRE, and CoreDNS. Defaults to "info".
#>
[CmdletBinding()]
param(
    [string] $EtcdBin        = "etcd",
    [string] $SpireServerBin = "spire-server",
    [string] $SpireAgentBin  = "spire-agent",
    [string] $CoreDNSBin     = "coredns",
    [string] $LogLevel       = "info",
    [string] $LogDir         = "",
    [switch] $NoWait
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

# ---------------------------------------------------------------------------
# Resolve script paths & logs folder
# ---------------------------------------------------------------------------
$EtcdScript        = Join-Path $PSScriptRoot "etcd-standalone.ps1"
$SpireServerScript = Join-Path $PSScriptRoot "spire-server.ps1"
$SpireAgentScript  = Join-Path $PSScriptRoot "spire-agent.ps1"
$CoreDNSScript     = Join-Path $PSScriptRoot "coredns.ps1"

$ProjectRoot = Split-Path -Parent $PSScriptRoot
$ServerConfig = Join-Path $ProjectRoot "configs\spire-server.conf"

if ([string]::IsNullOrWhiteSpace($LogDir)) {
    $LogDir = Join-Path $ProjectRoot "logs"
}
if (-not (Test-Path $LogDir)) {
    New-Item -ItemType Directory -Path $LogDir -Force | Out-Null
}

# Verify all helper scripts exist
foreach ($s in @($EtcdScript, $SpireServerScript, $SpireAgentScript, $CoreDNSScript)) {
    if (-not (Test-Path $s)) {
        Write-Error "Required script '$s' not found."
        exit 1
    }
}

# Print effective config banner
Write-Host ""
Write-Host "=== OMNIMESH DEV ENVIRONMENT =========================================" -ForegroundColor Cyan
Write-Host "  etcd Bin     : $EtcdBin"
Write-Host "  SPIRE Server : $SpireServerBin"
Write-Host "  SPIRE Agent  : $SpireAgentBin"
Write-Host "  CoreDNS Bin  : $CoreDNSBin"
Write-Host "  Log Level    : $LogLevel"
Write-Host "  Logs Folder  : $LogDir"
Write-Host "======================================================================" -ForegroundColor Cyan
Write-Host ""

$Jobs = @()

try {
    # ---------------------------------------------------------------------------
    # 1. Start etcd
    # ---------------------------------------------------------------------------
    Write-Host "Starting etcd (standalone)..." -ForegroundColor Yellow
    $etcdLogFile = Join-Path $LogDir "etcd.log"
    $etcdJob = Start-Job -Name "etcd" -ScriptBlock {
        param($script, $bin, $logLevel, $logFile)
        & $script -EtcdBin $bin -LogLevel $logLevel *>&1 | Tee-Object -FilePath $logFile
    } -ArgumentList $EtcdScript, $EtcdBin, $LogLevel, $etcdLogFile
    $Jobs += $etcdJob

    # Wait a moment for etcd to start listening
    Start-Sleep -Seconds 2

    # ---------------------------------------------------------------------------
    # 2. Start SPIRE Server
    # ---------------------------------------------------------------------------
    Write-Host "Starting SPIRE Server..." -ForegroundColor Yellow
    $spireServerLogFile = Join-Path $LogDir "spire-server.log"
    $spireServerJob = Start-Job -Name "spire-server" -ScriptBlock {
        param($script, $bin, $logLevel, $logFile)
        & $script -SpireServerBin $bin -LogLevel $logLevel *>&1 | Tee-Object -FilePath $logFile
    } -ArgumentList $SpireServerScript, $SpireServerBin, $LogLevel, $spireServerLogFile
    $Jobs += $spireServerJob

    # Wait for SPIRE Server to be ready/healthy
    Write-Host "Waiting for SPIRE Server to become healthy..." -ForegroundColor Yellow
    $healthy = $false
    for ($i = 0; $i -lt 15; $i++) {
        Start-Sleep -Seconds 1
        $health = & $SpireServerBin healthcheck 2>&1
        if ($LASTEXITCODE -eq 0) {
            $healthy = $true
            break
        }
    }
    if (-not $healthy) {
        Write-Warning "SPIRE Server healthcheck did not report healthy. Proceeding anyway..."
    } else {
        Write-Host "SPIRE Server is healthy!" -ForegroundColor Green
    }

    # ---------------------------------------------------------------------------
    # 3. Generate Agent Join Token (if not already joined)
    # ---------------------------------------------------------------------------
    $SpireAgentKeyFile = Join-Path $ProjectRoot "data\spire-agent\keys.json"
    $token = ""

    if (Test-Path $SpireAgentKeyFile) {
        Write-Host "SPIRE Agent is already joined (found $SpireAgentKeyFile). Starting normal execution..." -ForegroundColor Green
    } else {
        Write-Host "Generating SPIRE Agent join token..." -ForegroundColor Yellow
        $tokenOutput = & $SpireServerBin token generate -spiffeID spiffe://omnimesh.local/agents/dev-agent 2>&1
        
        if ($tokenOutput -match "Token:\s*(.+)") {
            $token = $Matches[1].Trim()
            Write-Host "Successfully generated agent join token: $token" -ForegroundColor Green
        } else {
            # Try to parse raw string if it is just outputted directly
            $token = ($tokenOutput -join "").Trim()
            if ($token -notmatch "failed" -and $token -notmatch "error" -and $token.Length -gt 5) {
                Write-Host "Extracted agent join token: $token" -ForegroundColor Green
            } else {
                Write-Error "Failed to generate SPIRE Agent join token. Output: $tokenOutput"
                exit 1
            }
        }
    }

    # ---------------------------------------------------------------------------
    # 4. Start SPIRE Agent
    # ---------------------------------------------------------------------------
    Write-Host "Starting SPIRE Agent..." -ForegroundColor Yellow
    $spireAgentLogFile = Join-Path $LogDir "spire-agent.log"
    $spireAgentJob = Start-Job -Name "spire-agent" -ScriptBlock {
        param($script, $bin, $token, $logLevel, $logFile)
        & $script -SpireAgentBin $bin -JoinToken $token -LogLevel $logLevel *>&1 | Tee-Object -FilePath $logFile
    } -ArgumentList $SpireAgentScript, $SpireAgentBin, $token, $LogLevel, $spireAgentLogFile
    $Jobs += $spireAgentJob

    # Wait for SPIRE Agent to become healthy
    Write-Host "Waiting for SPIRE Agent to become healthy..." -ForegroundColor Yellow
    $agentHealthy = $false
    for ($i = 0; $i -lt 15; $i++) {
        Start-Sleep -Seconds 1
        $agentHealth = & $SpireAgentBin healthcheck 2>&1
        if ($LASTEXITCODE -eq 0) {
            $agentHealthy = $true
            break
        }
    }
    if (-not $agentHealthy) {
        Write-Warning "SPIRE Agent healthcheck did not report healthy. Proceeding anyway..."
    } else {
        Write-Host "SPIRE Agent is healthy!" -ForegroundColor Green
    }

    # ---------------------------------------------------------------------------
    # 5. Register omnimeshcontrol SPIFFE Identity in SPIRE Server
    # ---------------------------------------------------------------------------
    Write-Host "Registering omnimeshcontrol workload entry in SPIRE Server..." -ForegroundColor Yellow
    $controlBinPath = Join-Path $ProjectRoot "bin\omnimeshcontrol.exe"
    
    # Remove old entry if present to ensure clean creation with -admin flag
    & $SpireServerBin entry delete -spiffeID spiffe://omnimesh.local/ns/omnimesh-system/sa/omnimeshcontrol 2>&1 | Out-Null

    $entryOut = & $SpireServerBin entry create `
        -spiffeID spiffe://omnimesh.local/ns/omnimesh-system/sa/omnimeshcontrol `
        -parentID spiffe://omnimesh.local/agents/dev-agent `
        -selector windows:process-name:omnimeshcontrol.exe `
        -selector windows:path:$controlBinPath `
        -selector unix:user:runner `
        -admin 2>&1

    if ($LASTEXITCODE -eq 0 -or ($entryOut -join "") -match "already exists") {
        Write-Host "Successfully registered omnimeshcontrol admin workload identity!" -ForegroundColor Green
    } else {
        Write-Warning "Registration of omnimeshcontrol identity reported: $entryOut"
    }

    # ---------------------------------------------------------------------------
    # 6. Start CoreDNS
    # ---------------------------------------------------------------------------
    Write-Host "Starting CoreDNS..." -ForegroundColor Yellow
    $coreDNSLogFile = Join-Path $LogDir "coredns.log"
    $corednsJob = Start-Job -Name "coredns" -ScriptBlock {
        param($script, $bin, $logFile)
        & $script -CoreDNSBin $bin *>&1 | Tee-Object -FilePath $logFile
    } -ArgumentList $CoreDNSScript, $CoreDNSBin, $coreDNSLogFile
    $Jobs += $corednsJob

    if ($NoWait) {
        Write-Host ""
        Write-Host "Dev environment jobs started (NoWait mode)." -ForegroundColor Green
        return $Jobs
    }

    # ---------------------------------------------------------------------------
    # Status Summary
    # ---------------------------------------------------------------------------
    Write-Host ""
    Write-Host "All components started in background!" -ForegroundColor Green
    Write-Host "Active Jobs:"
    Get-Job | Format-Table -Property Name, State, HasMoreData
    Write-Host ""
    Write-Host "Press ENTER to stop all services cleanly, or Ctrl+C to detach (jobs keep running)." -ForegroundColor Yellow
    $null = Read-Host

}
finally {
    if (-not $NoWait) {
        # ---------------------------------------------------------------------------
        # Graceful Shutdown
        # ---------------------------------------------------------------------------
        Write-Host "Stopping all dev environment jobs..." -ForegroundColor Yellow
        foreach ($job in $Jobs) {
            Stop-Job  -Job $job -ErrorAction SilentlyContinue
            Remove-Job -Job $job -Force -ErrorAction SilentlyContinue
            Write-Host "  Stopped background job: $($job.Name)"
        }
        Write-Host "All jobs stopped." -ForegroundColor Green
    }
}
