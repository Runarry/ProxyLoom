#Requires -Version 7.0
[CmdletBinding()]
param()
$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$secretDir = Join-Path $repoRoot 'deploy/secrets/local'
[void][IO.Directory]::CreateDirectory($secretDir)

# Secure the host directory before writing any values. Containers only mount
# individual readable files; they never receive the complete secrets directory.
if ($IsWindows) {
    $directoryInfo = [IO.DirectoryInfo]::new($secretDir)
    $acl = [IO.FileSystemAclExtensions]::GetAccessControl($directoryInfo, [Security.AccessControl.AccessControlSections]::Access)
    $acl.SetAccessRuleProtection($true, $false)
    foreach ($existingRule in @($acl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier]))) { [void]$acl.RemoveAccessRuleSpecific($existingRule) }
    $currentSid = [Security.Principal.WindowsIdentity]::GetCurrent().User
    foreach ($sid in @($currentSid, [Security.Principal.SecurityIdentifier]::new('S-1-5-18'), [Security.Principal.SecurityIdentifier]::new('S-1-5-32-544'))) {
        $rule = [Security.AccessControl.FileSystemAccessRule]::new($sid, 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow')
        $acl.AddAccessRule($rule)
    }
    # Sandboxed shells may run as a different account from Docker Desktop.
    # Grant only the established repository owner read access, not Users or
    # Authenticated Users. The normal interactive case adds no extra account.
    $gitPath = Join-Path $repoRoot '.git'
    if (Test-Path -LiteralPath $gitPath) {
        $owner = (Get-Acl -LiteralPath $gitPath).Owner
        $ownerSid = ([Security.Principal.NTAccount]::new($owner)).Translate([Security.Principal.SecurityIdentifier])
        if ($ownerSid.Value -ne $currentSid.Value -and $ownerSid.Value.StartsWith('S-1-5-21-')) {
            $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($ownerSid, 'ReadAndExecute', 'ContainerInherit,ObjectInherit', 'None', 'Allow'))
        }
    }
    [IO.FileSystemAclExtensions]::SetAccessControl($directoryInfo, $acl)
} else {
    [IO.File]::SetUnixFileMode($secretDir, [IO.UnixFileMode]::UserRead -bor [IO.UnixFileMode]::UserWrite -bor [IO.UnixFileMode]::UserExecute)
}

$names = @('db_bootstrap_password', 'db_runtime_password', 'db_migration_password', 'database_dsn', 'migration_dsn', 'master_key', 'token_pepper', 'content_hmac_key')
$existing = @($names | Where-Object { Test-Path -LiteralPath (Join-Path $secretDir $_) })
if ($existing.Count -ne 0 -and $existing.Count -ne $names.Count) {
    throw 'Incomplete development secret set. Restore the matching files; initialization will not rotate credentials of an existing database.'
}
if ($existing.Count -eq $names.Count) {
    foreach ($name in $names) {
        $bytes = [IO.File]::ReadAllBytes((Join-Path $secretDir $name))
        if ($bytes.Length -eq 0) { throw "Empty development secret file: $name" }
        if ($name -in @('master_key', 'token_pepper', 'content_hmac_key') -and $bytes.Length -ne 32) { throw "Invalid development key length: $name" }
    }
    Write-Output 'Existing development secret set preserved. No values were printed or rotated.'
    exit 0
}

function New-RandomBytes { return ,([Security.Cryptography.RandomNumberGenerator]::GetBytes(32)) }
function Write-Secret([string]$Name, [byte[]]$Value) {
    $path = Join-Path $secretDir $Name
    $stream = [IO.File]::Open($path, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
    try { $stream.Write($Value, 0, $Value.Length) } finally { $stream.Dispose() }
    if (-not $IsWindows) {
        [IO.File]::SetUnixFileMode($path, [IO.UnixFileMode]::UserRead -bor [IO.UnixFileMode]::GroupRead -bor [IO.UnixFileMode]::OtherRead)
    }
}
$utf8 = [Text.UTF8Encoding]::new($false)
$bootstrapPassword = [Convert]::ToHexString((New-RandomBytes)).ToLowerInvariant()
$runtimePassword = [Convert]::ToHexString((New-RandomBytes)).ToLowerInvariant()
$migrationPassword = [Convert]::ToHexString((New-RandomBytes)).ToLowerInvariant()
Write-Secret 'db_bootstrap_password' ($utf8.GetBytes($bootstrapPassword))
Write-Secret 'db_runtime_password' ($utf8.GetBytes($runtimePassword))
Write-Secret 'db_migration_password' ($utf8.GetBytes($migrationPassword))
Write-Secret 'database_dsn' ($utf8.GetBytes("postgres://proxyloom:${runtimePassword}@postgres:5432/proxyloom?sslmode=disable"))
Write-Secret 'migration_dsn' ($utf8.GetBytes("postgres://proxyloom_migrator:${migrationPassword}@postgres:5432/proxyloom?sslmode=disable"))
foreach ($name in @('master_key', 'token_pepper', 'content_hmac_key')) { Write-Secret $name (New-RandomBytes) }
Write-Output 'Created development secret files with independent random values. No production credentials are used.'
