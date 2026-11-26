#!/usr/bin/env pwsh
# 运行平台 E2E 冒烟套件(目标固定在 /web-tool 前缀下)。
#   ./deploy/run-e2e.ps1                              # 默认目标 http://127.0.0.1:7000，只读
#   ./deploy/run-e2e.ps1 https://radar.ydsdoa.cn        # 指定只读目标
#
# E2E_ISOLATED=1 仅用于临时数据的独立实例。管理员凭据可用环境变量覆盖:
#   $env:WEB2API_ADMIN_USER='admin'; $env:WEB2API_ADMIN_PASSWORD='<config.yaml 中的密码>'
param(
    [string]$BaseUrl = 'http://127.0.0.1:7000'
)

$ErrorActionPreference = 'Stop'
Write-Host "running e2e.mjs against $BaseUrl (app root: $BaseUrl/web-tool/)"
node (Join-Path $PSScriptRoot 'e2e.mjs') $BaseUrl
exit $LASTEXITCODE
