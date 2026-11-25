param(
  [string]$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path,
  [string]$DeployDir = 'C:\Users\YDSD\AppData\Local\web2api-local',
  [string]$Binary = '',
  [switch]$UpdateConfig,
  [switch]$VerifyOnly
)
$ErrorActionPreference = 'Stop'
$deployWeb = Join-Path $DeployDir 'web'
$deployExe = Join-Path $DeployDir 'web2api.exe'

function Fail([string]$msg) { throw $msg }
function Assert-Marker([string]$path, [string]$marker) {
  if (!(Test-Path -LiteralPath $path)) { Fail ("missing file: " + $path) }
  if ((Select-String -LiteralPath $path -Pattern $marker -SimpleMatch -ErrorAction SilentlyContinue | Measure-Object).Count -lt 1) {
    Fail ("marker missing in " + $path + ": " + $marker)
  }
}
function Get-LiveProcess() {
  Get-CimInstance Win32_Process -Filter "Name = 'web2api.exe'" | Where-Object { $_.CommandLine -like ('*' + $DeployDir + '*') }
}
function Wait-PortFree([int]$port = 7000, [int]$seconds = 15) {
  $deadline = (Get-Date).AddSeconds($seconds)
  while ((Get-Date) -lt $deadline) {
    if (!(Get-NetTCPConnection -State Listen -LocalPort $port -ErrorAction SilentlyContinue)) { return }
    Start-Sleep -Milliseconds 300
  }
  Fail ('port ' + $port + ' still in use')
}
function Read-Plain([System.Security.SecureString]$secure) {
  $bstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
  try { return [Runtime.InteropServices.Marshal]::PtrToStringAuto($bstr) }
  finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr) }
}
function Start-Live() {
  $credPath = Join-Path $DeployDir 'admin.credential.xml'
  $metricsPath = Join-Path $DeployDir 'metrics.secret.xml'
  if (!(Test-Path -LiteralPath $credPath)) { Fail ('missing credential: ' + $credPath) }
  $cred = Import-Clixml -LiteralPath $credPath
  $metricsToken = Read-Plain (Import-Clixml -LiteralPath $metricsPath)
  $vars = @{
    # 本机 UI 压力验收需要较高管理面预算；生产默认值不变。
    WEB2API_SECURITY_ADMIN_RATE_PER_MINUTE = '300'
    WEB2API_SECURITY_ADMIN_RATE_PER_IP = '300'
    WEB2API_SERVER_HOST = '127.0.0.1'
    WEB2API_SERVER_PORT = '7000'
    WEB2API_APP_DEBUG_ENDPOINTS = 'false'
    WEB2API_APP_SAVE_LOG = 'false'
    WEB2API_PLATFORM_ADMIN_PASSWORD = $cred.GetNetworkCredential().Password
    WEB2API_PLATFORM_ADMIN_USERNAME = $cred.UserName
    WEB2API_PLATFORM_DATA_DIR = (Join-Path $DeployDir 'data')
    WEB2API_SECURITY_METRICS_TOKEN = $metricsToken
  }
  $old = @{}
  try {
    foreach ($k in $vars.Keys) { $old[$k] = [Environment]::GetEnvironmentVariable($k); [Environment]::SetEnvironmentVariable($k, $vars[$k]) }
    Start-Process -FilePath $deployExe -ArgumentList @('serve', '--config', 'config.yaml') -WorkingDirectory $DeployDir -WindowStyle Hidden -RedirectStandardOutput (Join-Path $DeployDir 'stdout.log') -RedirectStandardError (Join-Path $DeployDir 'stderr.log') -PassThru | Out-Null
  } finally {
    foreach ($k in $old.Keys) { [Environment]::SetEnvironmentVariable($k, $old[$k]) }
  }
}
function Cache-Bust() {
  $pages = @(
    @{ file = (Join-Path $deployWeb 'admin/index.html'); dir = (Join-Path $deployWeb 'admin') },
    @{ file = (Join-Path $deployWeb 'site/index.html'); dir = (Join-Path $deployWeb 'site') }
  )
  foreach ($page in $pages) {
    if (!(Test-Path -LiteralPath $page.file)) { continue }
    $text = Get-Content -LiteralPath $page.file -Raw -Encoding UTF8
    foreach ($asset in @('assets/app.js', 'assets/style.css', 'assets/theme-boot.js', 'assets/engine-status.js')) {
      $file = Join-Path $page.dir $asset
      if (!(Test-Path -LiteralPath $file)) { continue }
      $hash = (Get-FileHash -LiteralPath $file -Algorithm SHA256).Hash.Substring(0, 8)
      $text = [regex]::Replace($text, '(' + [regex]::Escape($asset) + '\?v=)[^"'']+', '${1}' + $hash)
    }
    [IO.File]::WriteAllText($page.file, $text, (New-Object Text.UTF8Encoding($false)))
  }
}
function Verify() {
  $cred = Import-Clixml -LiteralPath (Join-Path $DeployDir 'admin.credential.xml')
  $pass = $cred.GetNetworkCredential().Password
  $idx = Invoke-RestMethod 'http://127.0.0.1:7000/web-tool/console/' -TimeoutSec 8
  $nav = ([regex]::Matches($idx, '#/users|#/logs|#/blacklist')).Count
  $v = [regex]::Match($idx, 'app\.js\?v=([^"'']+)').Groups[1].Value
  if ($nav -lt 6) { Fail ('console nav missing new pages: ' + $nav) }
  $js = Invoke-RestMethod ('http://127.0.0.1:7000/web-tool/console/assets/app.js?v=' + $v) -TimeoutSec 8
  $markers = ([regex]::Matches($js, 'pageUsers|pageLogs|pageBlacklist')).Count
  if ($markers -lt 6) { Fail ('served app.js missing new pages: ' + $markers) }
  $h = Invoke-RestMethod 'http://127.0.0.1:7000/web-tool/health' -TimeoutSec 8
  if ($h.status -ne 'healthy') { Fail ('unhealthy: ' + $h.status) }
  $headers = @{ 'Content-Type' = 'application/json' }
  $login = Invoke-RestMethod -Method Post -Uri 'http://127.0.0.1:7000/web-tool/api/auth/login' -Headers $headers -Body (@{ username = $cred.UserName; password = $pass } | ConvertTo-Json -Compress) -TimeoutSec 8
  $auth = @{ Authorization = 'Bearer ' + $login.token }
  $me = Invoke-RestMethod -Uri 'http://127.0.0.1:7000/web-tool/api/me' -Headers $auth -TimeoutSec 8
  if ($me.role -ne 'admin') { Fail ('role mismatch: ' + $me.role) }
  $users = Invoke-RestMethod -Uri 'http://127.0.0.1:7000/web-tool/api/users' -Headers $auth -TimeoutSec 8
  $cfg = Invoke-RestMethod -Uri 'http://127.0.0.1:7000/web-tool/api/logs/config' -Headers $auth -TimeoutSec 8
  if ($null -eq $cfg.PSObject.Properties['file_logging_enabled']) { Fail 'logs config missing file_logging_enabled' }
  Write-Output ('VERIFY_OK nav=' + $nav + ' js_markers=' + $markers + ' assets_version=' + $v + ' role=' + $me.role + ' users=' + $users.total + ' retention=' + $cfg.retention_days)
}

if ($VerifyOnly) { Verify; exit 0 }

$srcWeb = Join-Path $RepoRoot 'web'
$srcCfg = Join-Path $RepoRoot 'config.yaml'
if (!(Test-Path -LiteralPath $srcWeb)) { Fail ('repo web missing: ' + $srcWeb) }
foreach ($marker in @('pageUsers', 'pageLogs', 'pageBlacklist')) {
  Assert-Marker (Join-Path $srcWeb 'admin/assets/app.js') $marker
}
if ($Binary) { $newExe = $Binary }
else {
  $go = if ($env:W2A_GO) { $env:W2A_GO } else { 'go' }
  $newExe = Join-Path $env:TEMP 'w2a-deploy-build.exe'
  Push-Location $RepoRoot
  try {
    & $go build -trimpath -ldflags '-s -w' -o $newExe .
    if ($LASTEXITCODE -ne 0) { Fail 'go build failed' }
  } finally { Pop-Location }
}
$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$live = Get-LiveProcess
if ($live) { Stop-Process -Id $live.ProcessId -Force; Wait-PortFree }
Copy-Item -LiteralPath $deployExe -Destination (Join-Path $DeployDir ('web2api.pre-deploy-' + $stamp + '.exe')) -Force
Copy-Item -LiteralPath $deployWeb -Destination (Join-Path $DeployDir ('web.pre-deploy-' + $stamp)) -Recurse -Force
Copy-Item -LiteralPath $newExe -Destination $deployExe -Force
Copy-Item -Path (Join-Path $srcWeb '*') -Destination $deployWeb -Recurse -Force
if (Test-Path -LiteralPath (Join-Path $deployWeb 'web')) { Remove-Item -LiteralPath (Join-Path $deployWeb 'web') -Recurse -Force }
if ($UpdateConfig) { Copy-Item -LiteralPath $srcCfg -Destination (Join-Path $DeployDir 'config.yaml') -Force }
foreach ($marker in @('pageUsers', 'pageLogs', 'pageBlacklist')) {
  Assert-Marker (Join-Path $deployWeb 'admin/assets/app.js') $marker
}
Cache-Bust
Start-Live
$deadline = (Get-Date).AddSeconds(30)
do {
  Start-Sleep -Milliseconds 500
  try { $h = Invoke-RestMethod 'http://127.0.0.1:7000/web-tool/health' -TimeoutSec 2; if ($h.status -eq 'healthy') { break } } catch {}
} while ((Get-Date) -lt $deadline)
Verify
Write-Output ('DEPLOY_OK backup_stamp=' + $stamp + ' exe_size=' + (Get-Item -LiteralPath $deployExe).Length)
