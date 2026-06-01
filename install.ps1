#Requires -Version 5.1
<#
.SYNOPSIS
    mimicode installer for Windows.

.DESCRIPTION
    Builds mimicode from source and installs it to a per-user location, then
    adds that location to your user PATH so you can run `mimicode` from any
    terminal. No administrator rights required.

.EXAMPLE
    irm https://raw.githubusercontent.com/trymimicode/mimicode-go/main/install.ps1 | iex

.EXAMPLE
    # Override the install location:
    $env:INSTALL_DIR = "C:\tools\mimicode"; .\install.ps1
#>

$ErrorActionPreference = "Stop"

$Repo        = "trymimicode/mimicode-go"
$Module      = "github.com/trymimicode/mimicode-go"
$BinaryName  = "mimicode.exe"
$CmdPath     = "./cmd/mimicode"

# Per-user, no-admin location that is easy to reach. We add it to the user
# PATH below so `mimicode` works from anywhere.
$InstallDir = if ($env:INSTALL_DIR) { $env:INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA "Programs\mimicode" }

Write-Host "Installing mimicode..." -ForegroundColor Cyan

function Install-Binary([string]$SourcePath) {
    if (-not (Test-Path $InstallDir)) {
        New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    }
    $dest = Join-Path $InstallDir $BinaryName
    Move-Item -Path $SourcePath -Destination $dest -Force
    Write-Host "OK  $BinaryName installed to $dest" -ForegroundColor Green
    return $dest
}

function Build-FromSource {
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
        return $null
    }
    Write-Host "OK  Go detected, building from source..." -ForegroundColor Green

    # If we're already inside the mimicode repo, build the local checkout so
    # local changes are what get installed; otherwise clone a fresh copy.
    $cleanup = $null
    if ((Test-Path "go.mod") -and ((Get-Content "go.mod" -TotalCount 1) -match [regex]::Escape("module $Module"))) {
        $buildDir = (Get-Location).Path
        Write-Host "    Building from local checkout: $buildDir"
    } else {
        if (-not (Get-Command git -ErrorAction SilentlyContinue)) {
            Write-Host "ERR git is required to clone the repository." -ForegroundColor Red
            return $null
        }
        $buildDir = Join-Path ([System.IO.Path]::GetTempPath()) ("mimicode-" + [System.Guid]::NewGuid().ToString("N"))
        $cleanup = $buildDir
        Write-Host "    Cloning repository..."
        git clone --depth 1 "https://github.com/$Repo.git" $buildDir
    }

    try {
        # Version metadata matches the Makefile so `mimicode --version` is accurate.
        Push-Location $buildDir
        $version = (git describe --tags --always --dirty 2>$null); if (-not $version) { $version = "dev" }
        $commit  = (git rev-parse --short HEAD 2>$null);          if (-not $commit)  { $commit  = "unknown" }
        $buildDate = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")

        Write-Host "    Building binary ($version)..."
        $out = Join-Path $buildDir $BinaryName
        $ldflags = "-s -w -X main.version=$version -X main.commit=$commit -X main.buildDate=$buildDate"
        go build -ldflags="$ldflags" -o $out $CmdPath
        Pop-Location

        return (Install-Binary $out)
    } finally {
        if ($cleanup -and (Test-Path $cleanup)) {
            Remove-Item -Recurse -Force $cleanup -ErrorAction SilentlyContinue
        }
    }
}

function Get-Prebuilt {
    $arch = if ([Environment]::Is64BitOperatingSystem) {
        if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
    } else { "amd64" }
    $file = "$BinaryName-windows-$arch.exe"
    $url  = "https://github.com/$Repo/releases/latest/download/$file"
    $tmp  = Join-Path ([System.IO.Path]::GetTempPath()) $file
    Write-Host "    Checking for a prebuilt binary..."
    try {
        Invoke-WebRequest -Uri $url -OutFile $tmp -UseBasicParsing -ErrorAction Stop
        Write-Host "OK  Downloaded prebuilt binary" -ForegroundColor Green
        return (Install-Binary $tmp)
    } catch {
        if (Test-Path $tmp) { Remove-Item $tmp -Force }
        return $null
    }
}

# Prefer building from source; fall back to a prebuilt download if Go is absent.
$installed = Build-FromSource
if (-not $installed) { $installed = Get-Prebuilt }
if (-not $installed) {
    Write-Host "ERR Go is not installed and no prebuilt binary is available." -ForegroundColor Red
    Write-Host "    Install Go 1.26+ (winget install GoLang.Go) and re-run, or download"
    Write-Host "    a binary from https://github.com/$Repo/releases"
    exit 1
}

# ── Add install dir to the user PATH (persistent) and current session ────────
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if (-not $userPath) { $userPath = "" }
$paths = $userPath.Split(';') | Where-Object { $_ -ne "" }
if ($paths -notcontains $InstallDir) {
    $newPath = (@($InstallDir) + $paths) -join ';'
    [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
    Write-Host "OK  Added $InstallDir to your user PATH" -ForegroundColor Green
    Write-Host "    (Open a new terminal for the PATH change to take effect.)"
}
# Make it usable in the current session too.
if (($env:Path -split ';') -notcontains $InstallDir) {
    $env:Path = "$InstallDir;$env:Path"
}

# ── Verify ───────────────────────────────────────────────────────────────────
try {
    & $installed --version | Out-Null
} catch {
    Write-Host "WARN Installed but 'mimicode --version' failed - check the output above." -ForegroundColor Yellow
}

# ── Dependency / environment checks ──────────────────────────────────────────
if (-not (Get-Command rg -ErrorAction SilentlyContinue)) {
    Write-Host ""
    Write-Host "WARN ripgrep (rg) is required but not installed." -ForegroundColor Yellow
    Write-Host "     Install: winget install BurntSushi.ripgrep.MSVC"
}

if (-not $env:ANTHROPIC_API_KEY) {
    Write-Host ""
    Write-Host "WARN ANTHROPIC_API_KEY not set." -ForegroundColor Yellow
    Write-Host "     Get a key at https://console.anthropic.com/settings/keys then set it:"
    Write-Host "       setx ANTHROPIC_API_KEY `"your-key-here`""
}

Write-Host ""
Write-Host "Installation complete!" -ForegroundColor Green
Write-Host ""
Write-Host "Usage:"
Write-Host "  mimicode `"add tests to calc.go`""
Write-Host "  mimicode --tui"
Write-Host "  mimicode -s myfeature `"continue working`""
Write-Host ""
Write-Host "Docs: https://github.com/$Repo"
