#Requires -Version 7.0
[CmdletBinding()]
param()

# Create an ignored, derived registry for the explicit Docker Desktop network
# overlay. The source identity and its private key files are never changed.
$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$source = Join-Path $repoRoot 'deploy/secrets/local/runner_registry'
$directory = Join-Path $repoRoot '.cache/proxyloom-manual'
$destination = Join-Path $directory 'runner_registry_network'
if (-not (Test-Path -LiteralPath $source -PathType Leaf)) { throw 'Development runner registry is missing.' }
$items = @(Get-Content -LiteralPath $source -Raw | ConvertFrom-Json)
if ($items.Count -ne 1 -or $items[0].validation_slots -ne 1) { throw 'Development runner registry has an unexpected shape.' }
$items[0] | Add-Member -NotePropertyName connectivity_slots -NotePropertyValue 4 -Force
$items[0] | Add-Member -NotePropertyName throughput_slots -NotePropertyValue 1 -Force
New-Item -ItemType Directory -Force -Path $directory | Out-Null
$temporary = Join-Path $directory ('runner_registry_network.' + [Guid]::NewGuid().ToString('N'))
try {
    [IO.File]::WriteAllText($temporary, (ConvertTo-Json -InputObject @($items) -Depth 5) + [Environment]::NewLine, [Text.UTF8Encoding]::new($false))
    Move-Item -LiteralPath $temporary -Destination $destination -Force
} finally {
    Remove-Item -LiteralPath $temporary -Force -ErrorAction SilentlyContinue
}
Write-Output 'Created derived local Runner registry with bounded online capacities. No keys were changed.'
