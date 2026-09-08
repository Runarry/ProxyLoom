#Requires -Version 7.0
[CmdletBinding()]
param([ValidateSet('amd64','arm64')][string]$Architecture = 'amd64')
$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
# Establish the existing protected development directory without rotating keys.
& (Join-Path $PSScriptRoot 'dev-init.ps1')
if ($LASTEXITCODE -and $LASTEXITCODE -ne 0) { throw 'Development identity setup failed.' }
$secretDir = Join-Path $repoRoot 'deploy/secrets/local'
$names = @('runner_ca','runner_server_cert','runner_server_key','runner_client_cert','runner_client_key','runner_registry','runner.env')
$present = @($names | Where-Object { Test-Path -LiteralPath (Join-Path $secretDir $_) })
if ($present.Count -eq $names.Count) {
    foreach ($name in $names) { if ((Get-Item -LiteralPath (Join-Path $secretDir $name)).Length -eq 0) { throw 'Empty runner identity file; restore the existing bundle.' } }
    Write-Output 'Existing runner identity bundle preserved. No credentials printed or rotated.'
    exit 0
}
if ($present.Count -ne 0) { throw 'Incomplete runner identity bundle. Restore it; initialization never rotates an existing partial bundle.' }

# Read only the repository lock, and authenticate installed binaries before
# registering them. This initializer never downloads or executes a core.
$lockText = [IO.File]::ReadAllText((Join-Path $repoRoot 'compat/cores.lock.yaml'))
$buildIDs = @()
foreach ($match in [regex]::Matches($lockText, '(?ms)^  - id: (?<id>[^\r\n]+)\r?\n(?<body>.*?)(?=^  - id: |\z)')) {
    $body = $match.Groups['body'].Value
    if ($body -notmatch "(?m)^    arch: $Architecture`r?$" ) { continue }
    $fields = @{}
    foreach ($line in [regex]::Matches($body, '(?m)^    (?<key>[a-z_][a-z0-9_]*): (?<value>[^\r\n]+)')) { $fields[$line.Groups['key'].Value] = $line.Groups['value'].Value }
    $relative = '.cache/cores/{0}/{1}/{2}/{3}' -f $fields.family,$fields.git_tag,$Architecture,$fields.binary_name
    $binary = Join-Path $repoRoot $relative
    if (-not (Test-Path -LiteralPath $binary -PathType Leaf)) { throw 'Locked core cache missing. Run node scripts/verify-cores.mjs before runner initialization.' }
    if ((Get-FileHash -LiteralPath $binary -Algorithm SHA256).Hash.ToLowerInvariant() -cne $fields.binary_sha256) { throw 'Locked core digest mismatch. Runner identity was not created.' }
    $buildIDs += $match.Groups['id'].Value
}
if ($buildIDs.Count -ne 3) { throw 'Expected exactly three locked core builds for the selected architecture.' }

$utf8 = [Text.UTF8Encoding]::new($false)
function Write-ProtectedFile([string]$Name,[string]$Value) {
    $path = Join-Path $secretDir $Name
    $stream = [IO.File]::Open($path,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
    try { $bytes = $utf8.GetBytes($Value); $stream.Write($bytes,0,$bytes.Length) } finally { $stream.Dispose() }
    if (-not $IsWindows) { [IO.File]::SetUnixFileMode($path,[IO.UnixFileMode]::UserRead -bor [IO.UnixFileMode]::GroupRead -bor [IO.UnixFileMode]::OtherRead) }
}
function New-CertificateRequest([string]$Subject,[Security.Cryptography.RSA]$Key) {
    return [Security.Cryptography.X509Certificates.CertificateRequest]::new($Subject,$Key,[Security.Cryptography.HashAlgorithmName]::SHA256,[Security.Cryptography.RSASignaturePadding]::Pkcs1)
}
$caKey = [Security.Cryptography.RSA]::Create(3072)
$serverKey = [Security.Cryptography.RSA]::Create(3072)
$clientKey = [Security.Cryptography.RSA]::Create(3072)
$ca = $null; $serverCert = $null; $clientCert = $null
try {
    $start = [DateTimeOffset]::UtcNow.AddMinutes(-5)
    $end = $start.AddDays(90)
    $request = New-CertificateRequest 'CN=ProxyLoom development runner CA' $caKey
    $request.CertificateExtensions.Add([Security.Cryptography.X509Certificates.X509BasicConstraintsExtension]::new($true,$false,0,$true))
    $request.CertificateExtensions.Add([Security.Cryptography.X509Certificates.X509KeyUsageExtension]::new([Security.Cryptography.X509Certificates.X509KeyUsageFlags]::KeyCertSign,$true))
    $ca = $request.CreateSelfSigned($start,$end.AddDays(1))
    $serverRequest = New-CertificateRequest 'CN=api' $serverKey
    $san = [Security.Cryptography.X509Certificates.SubjectAlternativeNameBuilder]::new()
    $san.AddDnsName('api'); $san.AddDnsName('localhost'); $san.AddIpAddress([Net.IPAddress]::Loopback)
    $serverRequest.CertificateExtensions.Add($san.Build())
    $serverOids = [Security.Cryptography.OidCollection]::new(); [void]$serverOids.Add([Security.Cryptography.Oid]::new('1.3.6.1.5.5.7.3.1'))
    $serverRequest.CertificateExtensions.Add([Security.Cryptography.X509Certificates.X509EnhancedKeyUsageExtension]::new($serverOids,$true))
    $serverCert = $serverRequest.Create($ca,$start,$end,[Security.Cryptography.RandomNumberGenerator]::GetBytes(16))
    $runnerID = [Guid]::NewGuid().ToString()
    $clientRequest = New-CertificateRequest "CN=$runnerID" $clientKey
    $clientOids = [Security.Cryptography.OidCollection]::new(); [void]$clientOids.Add([Security.Cryptography.Oid]::new('1.3.6.1.5.5.7.3.2'))
    $clientRequest.CertificateExtensions.Add([Security.Cryptography.X509Certificates.X509EnhancedKeyUsageExtension]::new($clientOids,$true))
    $clientCert = $clientRequest.Create($ca,$start,$end,[Security.Cryptography.RandomNumberGenerator]::GetBytes(16))
    $certificateHash = [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($clientCert.RawData)).ToLowerInvariant()
    $registry = @(@{ runner_id=$runnerID; certificate_sha256=$certificateHash; arch=$Architecture; core_build_ids=$buildIDs; validation_slots=1 })
    Write-ProtectedFile 'runner_ca' $ca.ExportCertificatePem()
    Write-ProtectedFile 'runner_server_cert' $serverCert.ExportCertificatePem()
    Write-ProtectedFile 'runner_server_key' $serverKey.ExportPkcs8PrivateKeyPem()
    Write-ProtectedFile 'runner_client_cert' $clientCert.ExportCertificatePem()
    Write-ProtectedFile 'runner_client_key' $clientKey.ExportPkcs8PrivateKeyPem()
    Write-ProtectedFile 'runner_registry' (ConvertTo-Json -InputObject $registry -Depth 5)
    Write-ProtectedFile 'runner.env' "PROXYLOOM_RUNNER_ID=$runnerID`n"
    Write-Output 'Created dedicated 90-day development mTLS identity. CA signing key was not persisted.'
} finally {
    foreach ($item in @($ca,$serverCert,$clientCert,$caKey,$serverKey,$clientKey)) { if ($null -ne $item) { $item.Dispose() } }
}
