<#
.SYNOPSIS
    Dev bootstrap for relay: build binaries, ensure Postgres is up, start the
    server, log in the CLI, then start an agent and the web dev server.

.DESCRIPTION
    Brings up a complete local relay stack for development:
      1. Compiles relay-server, relay-agent, and relay into bin\.
      2. Creates the Postgres Docker container if it does not already exist,
         and makes sure it is running.
      3. Starts relay-server (with bootstrap admin + auto-enroll) in its own window.
      4. Logs the CLI in via the auth API and writes the bearer token to config.
      5. Starts relay-agent in its own window.
      6. Starts the web dev server (Vite) in its own window.

    The server, agent, and web dev server each run in a separate PowerShell
    window so their logs stay visible. Close a window to stop that piece.

.NOTES
    Requires: go, docker (Docker Desktop running), node/npm on PATH.
#>

$ErrorActionPreference = 'Stop'

# ─── Configuration ───────────────────────────────────────────────────────────
$RepoRoot       = Split-Path -Parent $PSScriptRoot
$PgContainer    = 'relay-postgres'
$PgImage        = 'postgres:16'
$PgUser         = 'relay'
$PgPassword     = 'relay'
$PgDatabase     = 'relay'
$PgPort         = 5432
$ServerUrl      = 'http://localhost:8080'
$Coordinator    = 'localhost:9090'
$AdminEmail     = 'admin@example.com'
$AdminPassword  = 'changeme123'

# ─── Helpers ─────────────────────────────────────────────────────────────────
function Write-Step($msg) { Write-Host "`n==> $msg" -ForegroundColor Cyan }
function Write-Info($msg) { Write-Host "    $msg" -ForegroundColor DarkGray }

function Assert-Command($name) {
    if (-not (Get-Command $name -ErrorAction SilentlyContinue)) {
        throw "'$name' was not found on PATH. Please install it and try again."
    }
}

# Prefer PowerShell 7 (pwsh) for spawned windows, fall back to Windows PowerShell.
function Get-Shell {
    if (Get-Command pwsh -ErrorAction SilentlyContinue) { return 'pwsh' }
    return 'powershell'
}

# Launch a long-running process in its own window that stays open (-NoExit).
function Start-DevWindow($title, $workingDir, $command) {
    $shell = Get-Shell
    $full  = "`$Host.UI.RawUI.WindowTitle = '$title'; $command"
    Start-Process -FilePath $shell `
        -ArgumentList '-NoExit', '-Command', $full `
        -WorkingDirectory $workingDir | Out-Null
}

# ─── 0. Preflight ────────────────────────────────────────────────────────────
Write-Step 'Checking prerequisites'
Assert-Command go
Assert-Command docker
Assert-Command npm
Write-Info "Repo root: $RepoRoot"

# ─── 1. Build binaries ───────────────────────────────────────────────────────
Write-Step 'Building relay binaries into bin\'
Push-Location $RepoRoot
try {
    go build -o bin\relay-server.exe ./cmd/relay-server
    go build -o bin\relay-agent.exe  ./cmd/relay-agent
    go build -o bin\relay.exe        ./cmd/relay
    if ($LASTEXITCODE -ne 0) { throw "go build failed (exit $LASTEXITCODE)" }
    Write-Info 'Built relay-server.exe, relay-agent.exe, relay.exe'
}
finally {
    Pop-Location
}

# ─── 2. Postgres container ───────────────────────────────────────────────────
Write-Step "Ensuring Postgres container '$PgContainer' exists and is running"
$exists  = (docker ps -a --filter "name=^/$PgContainer`$" --format '{{.Names}}') -eq $PgContainer
if (-not $exists) {
    Write-Info 'Container not found - creating it.'
    docker run -d `
        --name $PgContainer `
        -e POSTGRES_USER=$PgUser `
        -e POSTGRES_PASSWORD=$PgPassword `
        -e POSTGRES_DB=$PgDatabase `
        -p "$($PgPort):5432" `
        $PgImage | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "docker run failed (exit $LASTEXITCODE)" }
}
else {
    $running = (docker ps --filter "name=^/$PgContainer`$" --format '{{.Names}}') -eq $PgContainer
    if (-not $running) {
        Write-Info 'Container exists but is stopped - starting it.'
        docker start $PgContainer | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "docker start failed (exit $LASTEXITCODE)" }
    }
    else {
        Write-Info 'Container already running.'
    }
}

# Wait for Postgres to accept connections.
Write-Info 'Waiting for Postgres to be ready...'
$pgReady = $false
for ($i = 0; $i -lt 30; $i++) {
    docker exec $PgContainer pg_isready -U $PgUser -q 2>$null
    if ($LASTEXITCODE -eq 0) { $pgReady = $true; break }
    Start-Sleep -Seconds 1
}
if (-not $pgReady) { throw "Postgres did not become ready within 30s." }
Write-Info 'Postgres is ready.'

# ─── 3. Start relay-server ───────────────────────────────────────────────────
Write-Step 'Starting relay-server'
$serverCmd = @"
`$env:RELAY_BOOTSTRAP_ADMIN    = '$AdminEmail'
`$env:RELAY_BOOTSTRAP_PASSWORD = '$AdminPassword'
`$env:RELAY_ALLOW_AUTO_ENROLL  = 'true'
.\bin\relay-server.exe
"@
Start-DevWindow -title 'relay-server' -workingDir $RepoRoot -command $serverCmd
Write-Info 'relay-server launched in a new window.'

# ─── 4. Wait for the server, then log in ─────────────────────────────────────
Write-Step "Waiting for the server at $ServerUrl/v1/health"
$serverUp = $false
for ($i = 0; $i -lt 60; $i++) {
    try {
        $r = Invoke-WebRequest -UseBasicParsing -Uri "$ServerUrl/v1/health" -TimeoutSec 2
        if ($r.StatusCode -eq 200) { $serverUp = $true; break }
    }
    catch { }
    Start-Sleep -Seconds 1
}
if (-not $serverUp) { throw "Server did not become healthy within 60s." }
Write-Info 'Server is healthy.'

Write-Step 'Logging in the relay CLI (via auth API)'
$loginBody = @{ email = $AdminEmail; password = $AdminPassword } | ConvertTo-Json
$loginResp = Invoke-RestMethod -Method Post -Uri "$ServerUrl/v1/auth/login" `
    -Body $loginBody -ContentType 'application/json'
if (-not $loginResp.token) { throw "Login succeeded but no token was returned." }

$configDir  = Join-Path $env:APPDATA 'relay'
$configPath = Join-Path $configDir 'config.json'
if (-not (Test-Path $configDir)) { New-Item -ItemType Directory -Path $configDir | Out-Null }
@{ server_url = $ServerUrl; token = $loginResp.token } |
    ConvertTo-Json | Set-Content -Path $configPath -Encoding utf8
Write-Info "Saved credentials to $configPath"

# ─── 5. Start relay-agent ────────────────────────────────────────────────────
Write-Step 'Starting relay-agent'
Start-DevWindow -title 'relay-agent' -workingDir $RepoRoot `
    -command ".\bin\relay-agent.exe --coordinator $Coordinator"
Write-Info 'relay-agent launched in a new window.'

# ─── 6. Start the web dev server ─────────────────────────────────────────────
Write-Step 'Starting the web dev server'
$webDir = Join-Path $RepoRoot 'web'
$webCmd = "if (-not (Test-Path node_modules)) { npm install }; npm run dev"
Start-DevWindow -title 'relay-web' -workingDir $webDir -command $webCmd
Write-Info 'web dev server launched in a new window.'

# ─── Done ────────────────────────────────────────────────────────────────────
Write-Host "`nDev stack is up:" -ForegroundColor Green
Write-Host "    server : $ServerUrl  (gRPC $Coordinator)" -ForegroundColor Green
Write-Host "    web    : http://localhost:5173" -ForegroundColor Green
Write-Host "    admin  : $AdminEmail / $AdminPassword" -ForegroundColor Green
Write-Host "    Each piece runs in its own window; close a window to stop it.`n" -ForegroundColor Green
