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
    [switch] $NoWait
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

# ---------------------------------------------------------------------------
# Resolve script paths
# ---------------------------------------------------------------------------
$EtcdScript        = Join-Path $PSScriptRoot "etcd-standalone.ps1"
$SpireServerScript = Join-Path $PSScriptRoot "spire-server.ps1"
$SpireAgentScript  = Join-Path $PSScriptRoot "spire-agent.ps1"
$CoreDNSScript     = Join-Path $PSScriptRoot "coredns.ps1"

$ProjectRoot = Split-Path -Parent $PSScriptRoot
$ServerConfig = Join-Path $ProjectRoot "configs\spire-server.conf"

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
Write-Host "======================================================================" -ForegroundColor Cyan
Write-Host ""

$Jobs = @()

try {
    # ---------------------------------------------------------------------------
    # 1. Start etcd
    # ---------------------------------------------------------------------------
    Write-Host "Starting etcd (standalone)..." -ForegroundColor Yellow
    $etcdJob = Start-Job -Name "etcd" -ScriptBlock {
        param($script, $bin, $logLevel)
        & $script -EtcdBin $bin -LogLevel $logLevel
    } -ArgumentList $EtcdScript, $EtcdBin, $LogLevel
    $Jobs += $etcdJob

    # Wait a moment for etcd to start listening
    Start-Sleep -Seconds 2

    # ---------------------------------------------------------------------------
    # 2. Start SPIRE Server
    # ---------------------------------------------------------------------------
    Write-Host "Starting SPIRE Server..." -ForegroundColor Yellow
    $spireServerJob = Start-Job -Name "spire-server" -ScriptBlock {
        param($script, $bin, $logLevel)
        & $script -SpireServerBin $bin -LogLevel $logLevel
    } -ArgumentList $SpireServerScript, $SpireServerBin, $LogLevel
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
    # 3. Generate Agent Join Token
    # ---------------------------------------------------------------------------
    Write-Host "Generating SPIRE Agent join token..." -ForegroundColor Yellow
    $tokenOutput = & $SpireServerBin token generate -spiffeID spiffe://omnimesh.local/agents/dev-agent 2>&1
    
    $token = ""
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

    # ---------------------------------------------------------------------------
    # 4. Start SPIRE Agent
    # ---------------------------------------------------------------------------
    Write-Host "Starting SPIRE Agent..." -ForegroundColor Yellow
    $spireAgentJob = Start-Job -Name "spire-agent" -ScriptBlock {
        param($script, $bin, $token, $logLevel)
        & $script -SpireAgentBin $bin -JoinToken $token -LogLevel $logLevel
    } -ArgumentList $SpireAgentScript, $SpireAgentBin, $token, $LogLevel
    $Jobs += $spireAgentJob

    # ---------------------------------------------------------------------------
    # 5. Start CoreDNS
    # ---------------------------------------------------------------------------
    Write-Host "Starting CoreDNS..." -ForegroundColor Yellow
    $corednsJob = Start-Job -Name "coredns" -ScriptBlock {
        param($script, $bin)
        & $script -CoreDNSBin $bin
    } -ArgumentList $CoreDNSScript, $CoreDNSBin
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
