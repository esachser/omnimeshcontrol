#Requires -Version 5.1
<#
.SYNOPSIS
    Starts the full Omnimesh local dev environment and launches omnimeshcontrol.exe.

.DESCRIPTION
    Starts the background infrastructure components using start-dev-env.ps1:
    - etcd (standalone)
    - SPIRE Server
    - SPIRE Agent
    - CoreDNS
    
    Then verifies/builds and launches omnimeshcontrol.exe from bin/ as a background job.
    Press Enter to stop all components cleanly.

.PARAMETER OmnimeshControlBin
    Path to omnimeshcontrol executable. Defaults to "bin\omnimeshcontrol.exe".

.PARAMETER EtcdBin
    Path to etcd binary. Defaults to "etcd".

.PARAMETER SpireServerBin
    Path to spire-server binary. Defaults to "spire-server".

.PARAMETER SpireAgentBin
    Path to spire-agent binary. Defaults to "spire-agent".

.PARAMETER CoreDNSBin
    Path to coredns binary. Defaults to "coredns".

.PARAMETER LogLevel
    Log level for components. Defaults to "info".

.PARAMETER SpireEnabled
    Set to $true to enable SPIRE identity manager in omnimeshcontrol. Defaults to $true.
#>
[CmdletBinding()]
param(
    [string] $OmnimeshControlBin = "bin\omnimeshcontrol.exe",
    [string] $EtcdBin            = "etcd",
    [string] $SpireServerBin     = "spire-server",
    [string] $SpireAgentBin      = "spire-agent",
    [string] $CoreDNSBin         = "coredns",
    [string] $LogLevel           = "info",
    [bool]   $SpireEnabled       = $true
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$ScriptDir   = $PSScriptRoot
$ProjectRoot = Split-Path -Parent $ScriptDir
$DevEnvScript = Join-Path $ScriptDir "start-dev-env.ps1"

# Verify start-dev-env.ps1 exists
if (-not (Test-Path $DevEnvScript)) {
    Write-Error "Required script '$DevEnvScript' not found."
    exit 1
}

# Resolve full path to omnimeshcontrol.exe
if (-not [System.IO.Path]::IsPathRooted($OmnimeshControlBin)) {
    $ControlBinPath = Join-Path $ProjectRoot $OmnimeshControlBin
} else {
    $ControlBinPath = $OmnimeshControlBin
}

# Verify or build omnimeshcontrol.exe
if (-not (Test-Path $ControlBinPath)) {
    Write-Host "Binary '$ControlBinPath' not found. Building omnimeshcontrol..." -ForegroundColor Yellow
    Push-Location $ProjectRoot
    try {
        & go build -o $ControlBinPath .\cmd\omnimeshcontrol\
        if ($LASTEXITCODE -ne 0) {
            Write-Error "Failed to build omnimeshcontrol."
            exit 1
        }
        Write-Host "Build succeeded: $ControlBinPath" -ForegroundColor Green
    }
    finally {
        Pop-Location
    }
}

Write-Host ""
Write-Host "=== OMNIMESH DEV + CONTROL PLANE STARTUP ==============================" -ForegroundColor Cyan
Write-Host "  Control Plane Bin : $ControlBinPath"
Write-Host "  SPIRE Integration : $SpireEnabled"
Write-Host "======================================================================" -ForegroundColor Cyan
Write-Host ""

$AllJobs = @()

try {
    # ---------------------------------------------------------------------------
    # 1. Launch Infrastructure Components via start-dev-env.ps1
    # ---------------------------------------------------------------------------
    Write-Host "Launching local infrastructure (etcd, SPIRE, CoreDNS)..." -ForegroundColor Yellow
    $infraJobs = & $DevEnvScript -EtcdBin $EtcdBin `
                                 -SpireServerBin $SpireServerBin `
                                 -SpireAgentBin $SpireAgentBin `
                                 -CoreDNSBin $CoreDNSBin `
                                 -LogLevel $LogLevel `
                                 -NoWait

    if ($infraJobs) {
        $AllJobs += $infraJobs
    }

    # Give infrastructure a moment to stabilize
    Start-Sleep -Seconds 2

    # ---------------------------------------------------------------------------
    # 2. Launch omnimeshcontrol.exe
    # ---------------------------------------------------------------------------
    Write-Host "Starting omnimeshcontrol.exe..." -ForegroundColor Yellow
    $ommJob = Start-Job -Name "omnimeshcontrol" -ScriptBlock {
        param($bin, $projectRoot, $spireEnabled)
        Set-Location $projectRoot
        $env:OMNIMESH_SPIRE_ENABLED = if ($spireEnabled) { "true" } else { "false" }
        & $bin
    } -ArgumentList $ControlBinPath, $ProjectRoot, $SpireEnabled

    $AllJobs += $ommJob

    # ---------------------------------------------------------------------------
    # Status Summary
    # ---------------------------------------------------------------------------
    Write-Host ""
    Write-Host "All Omnimesh dev components & Control Plane started!" -ForegroundColor Green
    Write-Host "Active Jobs:"
    Get-Job | Format-Table -Property Name, State, HasMoreData
    Write-Host ""
    Write-Host "Press ENTER to stop all services cleanly (infrastructure + omnimeshcontrol)." -ForegroundColor Yellow
    $null = Read-Host

}
finally {
    # ---------------------------------------------------------------------------
    # Graceful Shutdown
    # ---------------------------------------------------------------------------
    Write-Host "Stopping all Omnimesh dev jobs..." -ForegroundColor Yellow
    foreach ($job in $AllJobs) {
        Stop-Job  -Job $job -ErrorAction SilentlyContinue
        Remove-Job -Job $job -Force -ErrorAction SilentlyContinue
        Write-Host "  Stopped job: $($job.Name)"
    }
    Write-Host "All components stopped cleanly." -ForegroundColor Green
}
