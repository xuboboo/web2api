#!/usr/bin/env pwsh
# Runs the OpenSERP end-to-end suite against a deployed instance.
#   ./deploy/run-e2e.ps1                      # defaults to http://127.0.0.1:7000
#   ./deploy/run-e2e.ps1 http://host:7000     # custom target
param([string]$BaseUrl = 'http://127.0.0.1:7000')
$ErrorActionPreference = 'Stop'
node "$PSScriptRoot/e2e.mjs" $BaseUrl
exit $LASTEXITCODE
