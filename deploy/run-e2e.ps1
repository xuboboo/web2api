#!/usr/bin/env pwsh
# 运行 E2E 套件。
#   ./deploy/run-e2e.ps1                              # 默认国内套件，目标 http://127.0.0.1:7000
#   ./deploy/run-e2e.ps1 http://10.0.0.5:7000         # 指定目标
#   ./deploy/run-e2e.ps1 -Suite global                # 旧版六引擎套件（需启用境外引擎）
#
# 节流参数（单 IP 建议放宽，避免把出口 IP 打热）：
#   $env:E2E_PACE_MS='8000'; $env:E2E_SOAK_PACE_MS='20000'
#   $env:E2E_SOAK_RUNS='3'; $env:E2E_CONCURRENCY='2'
param(
    [string]$BaseUrl = 'http://127.0.0.1:7000',
    [ValidateSet('domestic', 'global')]
    [string]$Suite = 'domestic'
)

$ErrorActionPreference = 'Stop'
$script = if ($Suite -eq 'global') { 'e2e.mjs' } else { 'e2e-domestic.mjs' }
Write-Host "running $script against $BaseUrl"
node (Join-Path $PSScriptRoot $script) $BaseUrl
exit $LASTEXITCODE
