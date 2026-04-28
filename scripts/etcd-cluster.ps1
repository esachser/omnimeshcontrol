#Requires -Version 5.1
<#
.SYNOPSIS
    Starts a 3-node etcd cluster that mirrors the Omnimesh dev topology.

.DESCRIPTION
    Node layout (from data/node-agents.txt):

        Node   Name    Client URL               Peer URL                 GRPC
        -----  ------  -----------------------  -----------------------  -----
        omm-1  omm-1   http://localhost:2379     http://localhost:2380     :9090
        omm-2  omm-2   http://localhost:12379    http://localhost:12380    :9091
        omm-3  omm-3   http://localhost:22379    http://localhost:22380    :9092

    Each etcd node is started as a background PowerShell Job so all three
    run in the same terminal session.  Press Enter (or Ctrl+C) to stop all
    nodes cleanly.

.PARAMETER EtcdBin
    Path to the etcd executable.  Defaults to "etcd" (must be on PATH).

.PARAMETER BaseDataDir
    Base directory for per-node data sub-directories.
    Defaults to ".\data".  Nodes will use:
        <BaseDataDir>\etcd-1   (omm-1)
        <BaseDataDir>\etcd-2   (omm-2)
        <BaseDataDir>\etcd-3   (omm-3)

.PARAMETER ClusterState
    "new"      – bootstrap a fresh cluster (first run).
    "existing" – join an already-bootstrapped cluster (subsequent runs).
    Defaults to "new".

.PARAMETER ClusterToken
    Token that uniquely identifies this cluster.
    Defaults to "test-omm".

.PARAMETER LogLevel
    etcd log level (debug | info | warn | error | panic | fatal).
    Defaults to "info".

.EXAMPLE
    # First run – bootstrap
    .\etcd-cluster.ps1

.EXAMPLE
    # Subsequent runs – rejoin existing data
    .\etcd-cluster.ps1 -ClusterState existing

.EXAMPLE
    # Custom data location
    .\etcd-cluster.ps1 -BaseDataDir C:\etcd-data -ClusterState existing
#>
[CmdletBinding()]
param(
    [string] $EtcdBin      = "etcd",
    [string] $BaseDataDir  = ".\data",
    [ValidateSet("new","existing")]
    [string] $ClusterState = "new",
    [string] $ClusterToken = "test-omm",
    [string] $LogLevel     = "info"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

# ---------------------------------------------------------------------------
# Check etcd is available
# ---------------------------------------------------------------------------
if (-not (Get-Command $EtcdBin -ErrorAction SilentlyContinue)) {
    Write-Error "etcd binary '$EtcdBin' not found. Install etcd and add it to PATH, or pass -EtcdBin <path>."
    exit 1
}

# ---------------------------------------------------------------------------
# Resolve base data dir
# ---------------------------------------------------------------------------
$BaseDataDir = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($BaseDataDir)

# ---------------------------------------------------------------------------
# Cluster topology (matches data/node-agents.txt)
# ---------------------------------------------------------------------------
$InitialCluster = "omm-1=http://localhost:2380,omm-2=http://localhost:12380,omm-3=http://localhost:22380"

$Nodes = @(
    [PSCustomObject]@{
        Name       = "omm-1"
        ClientURL  = "http://localhost:2379"
        PeerURL    = "http://localhost:2380"
        DataDir    = Join-Path $BaseDataDir "etcd-1"
    },
    [PSCustomObject]@{
        Name       = "omm-2"
        ClientURL  = "http://localhost:12379"
        PeerURL    = "http://localhost:12380"
        DataDir    = Join-Path $BaseDataDir "etcd-2"
    },
    [PSCustomObject]@{
        Name       = "omm-3"
        ClientURL  = "http://localhost:22379"
        PeerURL    = "http://localhost:22380"
        DataDir    = Join-Path $BaseDataDir "etcd-3"
    }
)

# ---------------------------------------------------------------------------
# Print banner
# ---------------------------------------------------------------------------
Write-Host ""
Write-Host "=== etcd 3-NODE CLUSTER ===============================================" -ForegroundColor Cyan
Write-Host "  Executable     : $EtcdBin"
Write-Host "  Cluster token  : $ClusterToken"
Write-Host "  Cluster state  : $ClusterState"
Write-Host "  Initial cluster: $InitialCluster"
Write-Host "  Log level      : $LogLevel"
Write-Host ""
foreach ($n in $Nodes) {
    Write-Host ("  {0,-6}  client={1,-28} peer={2,-28} data={3}" -f $n.Name, $n.ClientURL, $n.PeerURL, $n.DataDir)
}
Write-Host "=======================================================================" -ForegroundColor Cyan
Write-Host ""

# ---------------------------------------------------------------------------
# Start each node as a background Job
# ---------------------------------------------------------------------------
$Jobs = @()

foreach ($node in $Nodes) {
    # Capture loop variable for the script block closure.
    $n = $node

    Write-Host "Starting $($n.Name)..." -ForegroundColor Yellow

    $job = Start-Job -Name $n.Name -ScriptBlock {
        param($bin, $name, $dataDir, $clientURL, $peerURL, $initialCluster, $clusterState, $token, $logLevel)

        & $bin `
            --name                        $name `
            --data-dir                    $dataDir `
            --listen-client-urls          $clientURL `
            --advertise-client-urls       $clientURL `
            --listen-peer-urls            $peerURL `
            --initial-advertise-peer-urls $peerURL `
            --initial-cluster             $initialCluster `
            --initial-cluster-state       $clusterState `
            --initial-cluster-token       $token `
            --log-level                   $logLevel

    } -ArgumentList $EtcdBin, $n.Name, $n.DataDir, $n.ClientURL, $n.PeerURL, `
                    $InitialCluster, $ClusterState, $ClusterToken, $LogLevel

    $Jobs += $job
}

Write-Host ""
Write-Host "All 3 nodes started as background jobs." -ForegroundColor Green
Write-Host "Client endpoints:"
Write-Host "  omm-1  ->  http://localhost:2379"
Write-Host "  omm-2  ->  http://localhost:12379"
Write-Host "  omm-3  ->  http://localhost:22379"
Write-Host ""
Write-Host "Connect with etcdctl:"
Write-Host '  $env:ETCDCTL_API=3; etcdctl --endpoints=http://localhost:2379,http://localhost:12379,http://localhost:22379 endpoint health'
Write-Host ""
Write-Host "Use the following env vars for the Omnimesh control plane:"
Write-Host '  $env:OMNIMESH_ETCD_ENDPOINTS="http://localhost:2379,http://localhost:12379,http://localhost:22379"'
Write-Host '  $env:OMNIMESH_ETCD_AUTOSYNC="30s"'
Write-Host ""
Write-Host "Press ENTER to stop all nodes, or Ctrl+C to detach (nodes keep running)." -ForegroundColor Yellow
$null = Read-Host

# ---------------------------------------------------------------------------
# Graceful shutdown
# ---------------------------------------------------------------------------
Write-Host "Stopping etcd nodes..." -ForegroundColor Yellow

foreach ($job in $Jobs) {
    Stop-Job  -Job $job -ErrorAction SilentlyContinue
    Remove-Job -Job $job -Force -ErrorAction SilentlyContinue
    Write-Host "  Stopped $($job.Name)"
}

Write-Host "All nodes stopped." -ForegroundColor Green
