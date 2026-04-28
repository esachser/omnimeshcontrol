#Requires -Version 5.1
<#
.SYNOPSIS
    Starts a standalone (single-node) etcd instance for local development.

.DESCRIPTION
    Runs etcd as a foreground process on localhost:2379 (client) / localhost:2380 (peer).
    Data is persisted under $DataDir so the node survives restarts.
    Press Ctrl+C to stop.

.PARAMETER EtcdBin
    Path to the etcd executable.  Defaults to "etcd" (must be on PATH).

.PARAMETER DataDir
    Directory where etcd stores its data.  Defaults to ".\data\etcd-standalone".

.PARAMETER ClientURL
    URL the client listener binds to.  Defaults to "http://localhost:2379".

.PARAMETER PeerURL
    URL the peer listener binds to.    Defaults to "http://localhost:2380".

.PARAMETER LogLevel
    etcd log level (debug | info | warn | error | panic | fatal).
    Defaults to "info".

.EXAMPLE
    .\etcd-standalone.ps1

.EXAMPLE
    .\etcd-standalone.ps1 -DataDir C:\etcd-data -LogLevel warn
#>
[CmdletBinding()]
param(
    [string] $EtcdBin   = "etcd",
    [string] $DataDir   = ".\data\etcd-standalone",
    [string] $ClientURL = "http://localhost:2379",
    [string] $PeerURL   = "http://localhost:2380",
    [string] $LogLevel  = "info"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

# ---------------------------------------------------------------------------
# Resolve paths
# ---------------------------------------------------------------------------
$DataDir = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($DataDir)

# ---------------------------------------------------------------------------
# Check etcd is available
# ---------------------------------------------------------------------------
if (-not (Get-Command $EtcdBin -ErrorAction SilentlyContinue)) {
    Write-Error "etcd binary '$EtcdBin' not found. Install etcd and add it to PATH, or pass -EtcdBin <path>."
    exit 1
}

# ---------------------------------------------------------------------------
# Print effective config
# ---------------------------------------------------------------------------
Write-Host ""
Write-Host "=== etcd STANDALONE ==================================================" -ForegroundColor Cyan
Write-Host "  Executable  : $EtcdBin"
Write-Host "  Data dir    : $DataDir"
Write-Host "  Client URL  : $ClientURL"
Write-Host "  Peer URL    : $PeerURL"
Write-Host "  Log level   : $LogLevel"
Write-Host "======================================================================" -ForegroundColor Cyan
Write-Host ""

# ---------------------------------------------------------------------------
# etcd arguments
# ---------------------------------------------------------------------------
$etcdArgs = @(
    "--name",                   "omnimesh-standalone"
    "--data-dir",               $DataDir
    "--listen-client-urls",     $ClientURL
    "--advertise-client-urls",  $ClientURL
    "--listen-peer-urls",       $PeerURL
    "--initial-advertise-peer-urls", $PeerURL
    "--initial-cluster",        "omnimesh-standalone=$PeerURL"
    "--initial-cluster-state",  "new"
    "--initial-cluster-token",  "omnimesh-standalone"
    "--log-level",              $LogLevel
)

# ---------------------------------------------------------------------------
# Run
# ---------------------------------------------------------------------------
Write-Host "Starting etcd (foreground)...  Press Ctrl+C to stop." -ForegroundColor Yellow
& $EtcdBin @etcdArgs
