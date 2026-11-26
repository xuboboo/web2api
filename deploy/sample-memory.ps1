param(
  [int]$Port = 7000,
  [int]$DurationSeconds = 1800,
  [int]$IntervalSeconds = 10,
  [string]$OutputPath = (Join-Path $env:TEMP ('web2api-memory-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '.csv'))
)

$ErrorActionPreference = 'Stop'
if ($Port -lt 1 -or $Port -gt 65535 -or $DurationSeconds -lt 1 -or $IntervalSeconds -lt 1) {
  throw 'Port must be 1–65535 and duration and interval must be positive.'
}
if (Test-Path -LiteralPath $OutputPath) { throw 'Output file already exists.' }
$listener = @(Get-NetTCPConnection -State Listen -LocalPort $Port -ErrorAction Stop | Select-Object -ExpandProperty OwningProcess -Unique)
if ($listener.Count -ne 1) { throw 'Expected a single process listening on the selected port.' }
$rootId = [int]$listener[0]
$root = Get-Process -Id $rootId -ErrorAction Stop
if ($root.ProcessName -ne 'web2api') { throw 'The selected listener is not a web2api process.' }
$start = $root.StartTime
$deadline = (Get-Date).AddSeconds($DurationSeconds)
$samples = [System.Collections.Generic.List[object]]::new()
do {
  $root = Get-Process -Id $rootId -ErrorAction Stop
  if ($root.StartTime -ne $start) { throw 'The monitored process was replaced.' }
  $all = @(Get-CimInstance Win32_Process)
  $ids = [System.Collections.Generic.HashSet[int]]::new()
  [void]$ids.Add($rootId)
  do {
    $changed = $false
    foreach ($process in $all) {
      if ($ids.Contains([int]$process.ParentProcessId) -and $ids.Add([int]$process.ProcessId)) { $changed = $true }
    }
  } while ($changed)
  $tree = @(Get-Process -Id @($ids) -ErrorAction SilentlyContinue)
  $os = Get-CimInstance Win32_OperatingSystem
  $samples.Add([pscustomobject]@{
    timestamp = (Get-Date).ToString('o')
    root_pid = $rootId
    process_count = $tree.Count
    working_set_bytes = ($tree | Measure-Object WorkingSet64 -Sum).Sum
    private_bytes = ($tree | Measure-Object PrivateMemorySize64 -Sum).Sum
    system_free_bytes = [int64]$os.FreePhysicalMemory * 1024
  })
  $remaining = ($deadline - (Get-Date)).TotalSeconds
  if ($remaining -gt 0) { Start-Sleep -Seconds ([Math]::Min($IntervalSeconds, [Math]::Ceiling($remaining))) }
} while ((Get-Date) -lt $deadline)
$samples | Export-Csv -LiteralPath $OutputPath -NoTypeInformation -Encoding UTF8
$peak = ($samples | Measure-Object working_set_bytes -Maximum).Maximum
Write-Output ('samples=' + $samples.Count + ' peak_working_set_bytes=' + $peak + ' output=' + $OutputPath)
