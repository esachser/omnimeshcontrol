#Requires -Version 5.1
<#
.SYNOPSIS
    Starts CoreDNS for local development.

.DESCRIPTION
    Runs CoreDNS as a foreground process.
    Configured to resolve 'omnimesh.local' zone via etcd.
    Press Ctrl+C to stop.

.PARAMETER CoreDNSBin
    Path to the coredns executable. Defaults to "coredns" (must be on PATH).

.PARAMETER ConfigPath
    Path to the Corefile config. Defaults to ".\configs\Corefile".

.PARAMETER EtcdEndpoint
    Endpoint for etcd where SkyDNS records are stored. Defaults to "http://localhost:2379".
#>
[CmdletBinding()]
param(
    [string] $CoreDNSBin   = "coredns",
    [string] $ConfigPath   = ".\configs\Corefile",
    [string] $EtcdEndpoint = "http://localhost:2379"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

# ---------------------------------------------------------------------------
# Resolve paths relative to Project Root
# ---------------------------------------------------------------------------
$ProjectRoot = Split-Path -Parent $PSScriptRoot
$OldDir = Get-Location
Set-Location $ProjectRoot

try {
    $ResolvedConfig = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($ConfigPath)

    # Verify binary is available
    if (-not (Get-Command $CoreDNSBin -ErrorAction SilentlyContinue)) {
        Write-Error "coredns binary '$CoreDNSBin' not found. Install CoreDNS and add it to PATH, or pass -CoreDNSBin <path>."
        exit 1
    }

    # Set the OMNIMESH_ETCD_ENDPOINT environment variable for CoreDNS to use
    $env:OMNIMESH_ETCD_ENDPOINT = $EtcdEndpoint

    # Print effective config
    Write-Host ""
    Write-Host "=== CoreDNS ==========================================================" -ForegroundColor Cyan
    Write-Host "  Executable    : $CoreDNSBin"
    Write-Host "  Config Path   : $ResolvedConfig"
    Write-Host "  etcd Endpoint : $env:OMNIMESH_ETCD_ENDPOINT"
    Write-Host "======================================================================" -ForegroundColor Cyan
    Write-Host ""

    # Start CoreDNS
    Write-Host "Starting CoreDNS (foreground)...  Press Ctrl+C to stop." -ForegroundColor Yellow
    & $CoreDNSBin -conf $ResolvedConfig
}
finally {
    Set-Location $OldDir
}
