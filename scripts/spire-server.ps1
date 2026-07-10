#Requires -Version 5.1
<#
.SYNOPSIS
    Starts the SPIRE Server for local development.

.DESCRIPTION
    Runs SPIRE Server as a foreground process.
    Data is stored under $DataDir.
    Press Ctrl+C to stop.

.PARAMETER SpireServerBin
    Path to the spire-server executable. Defaults to "spire-server" (must be on PATH).

.PARAMETER DataDir
    Directory where SPIRE Server stores database and keys. Defaults to ".\data\spire-server".

.PARAMETER ConfigPath
    Path to the SPIRE Server config file. Defaults to ".\configs\spire-server.conf".

.PARAMETER LogLevel
    Log level (debug | info | warn | error). Defaults to "info".

.EXAMPLE
    .\spire-server.ps1
#>
[CmdletBinding()]
param(
    [string] $SpireServerBin = "spire-server",
    [string] $DataDir        = ".\data\spire-server",
    [string] $ConfigPath     = ".\configs\spire-server.conf",
    [string] $LogLevel       = "info"
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
    $ResolvedDataDir = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($DataDir)
    $ResolvedConfig = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($ConfigPath)

    # Ensure data directory exists
    if (-not (Test-Path $ResolvedDataDir)) {
        New-Item -ItemType Directory -Path $ResolvedDataDir -Force | Out-Null
    }

    # Verify binary is available
    if (-not (Get-Command $SpireServerBin -ErrorAction SilentlyContinue)) {
        Write-Error "spire-server binary '$SpireServerBin' not found. Install SPIRE and add it to PATH, or pass -SpireServerBin <path>."
        exit 1
    }

    # Print effective config
    Write-Host ""
    Write-Host "=== SPIRE SERVER =====================================================" -ForegroundColor Cyan
    Write-Host "  Executable  : $SpireServerBin"
    Write-Host "  Config Path : $ResolvedConfig"
    Write-Host "  Data Dir    : $ResolvedDataDir"
    Write-Host "  Log Level   : $LogLevel"
    Write-Host "======================================================================" -ForegroundColor Cyan
    Write-Host ""

    # Start SPIRE Server
    Write-Host "Starting SPIRE Server (foreground)...  Press Ctrl+C to stop." -ForegroundColor Yellow
    & $SpireServerBin run -config $ResolvedConfig -logLevel $LogLevel
}
finally {
    Set-Location $OldDir
}
