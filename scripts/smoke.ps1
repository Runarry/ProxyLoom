#Requires -Version 7.0
[CmdletBinding()]
param(
    [switch]$Build,
    [switch]$KeepRunning,
    [ValidatePattern('^proxyloom-smoke(?:-[a-z0-9]+)*$')][string]$ProjectName = ('proxyloom-smoke-' + [Guid]::NewGuid().ToString('N').Substring(0,12)),
    [ValidateRange(1024,65535)][int]$Port = 18080
)
$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$compose = Join-Path $repoRoot 'deploy/compose.dev.yaml'
$reportPath = Join-Path $repoRoot 'deploy/smoke-report.json'
$oldPort = $env:PROXYLOOM_DEV_PORT
$env:PROXYLOOM_DEV_PORT = "$Port"
$steps = [Collections.Generic.List[object]]::new()
$startedAt = [DateTime]::UtcNow.ToString('o')
$completed = $false
$ownsProject = $false
$authSecrets = [Collections.Generic.List[string]]::new()
Import-Module Microsoft.PowerShell.Utility -ErrorAction Stop
$authSession = [Microsoft.PowerShell.Commands.WebRequestSession]::new()
$authOrigin = "http://127.0.0.1:$Port"
$platform = (& docker version --format '{{.Server.Os}}/{{.Server.Arch}}' 2>$null)
if ($LASTEXITCODE -ne 0) { throw 'Docker Linux engine is unavailable.' }
foreach ($kind in @('container', 'network', 'volume')) {
    $inventoryArgs = @($kind, 'ls', '--quiet', '--filter', "label=com.docker.compose.project=$ProjectName")
    if ($kind -eq 'container') { $inventoryArgs += '--all' }
    $existingResources = & docker @inventoryArgs 2>&1
    if ($LASTEXITCODE -ne 0) { throw 'Could not inspect existing Docker resources.' }
    if (($existingResources -join '').Trim()) { throw 'Smoke project already has resources; choose a new project name. Existing resources were not changed.' }
}

function Invoke-Compose([string[]]$Arguments, [switch]$Capture) {
    $output = & docker compose --project-name $ProjectName --file $compose @Arguments 2>&1
    if ($LASTEXITCODE -ne 0) { throw "Compose command failed ($($Arguments[0])). Inspect local Docker state; raw output is suppressed to protect secrets." }
    if ($Capture) { return ($output -join "`n") }
}
function Assert-Step([string]$Name, [bool]$Condition) {
    if (-not $Condition) { throw "Smoke assertion failed: $Name" }
    $steps.Add(@{name=$Name; result='pass'})
    Write-Output "PASS $Name"
}
function Wait-Status([string]$Path, [int]$Expected) {
    $deadline = [DateTime]::UtcNow.AddSeconds(60)
    do {
        try {
            $response = Invoke-WebRequest -Uri "http://127.0.0.1:$Port$Path" -SkipHttpErrorCheck -TimeoutSec 3 -NoProxy
            if ([int]$response.StatusCode -eq $Expected) { return $true }
        } catch { }
        Start-Sleep -Milliseconds 500
    } while ([DateTime]::UtcNow -lt $deadline)
    return $false
}
function Assert-NoSecrets([string]$Content) {
    foreach ($value in $authSecrets) {
        if ($value -and $Content.Contains($value)) { throw 'An authentication secret was found in captured output. Raw output has not been printed.' }
    }
    foreach ($name in @('db_bootstrap_password','db_runtime_password','db_migration_password','database_dsn','migration_dsn','master_key','token_pepper','content_hmac_key','setup_token')) {
        $bytes = [IO.File]::ReadAllBytes((Join-Path $repoRoot "deploy/secrets/local/$name"))
        $representations = @([Text.Encoding]::UTF8.GetString($bytes), [Convert]::ToBase64String($bytes), [Convert]::ToHexString($bytes).ToLowerInvariant(), [Convert]::ToHexString($bytes))
        foreach ($value in $representations) {
            if ($value.Length -gt 8 -and $Content.Contains($value)) { throw 'A development secret was found in captured output. Raw output has not been printed.' }
        }
    }
}
function Invoke-SmokeAuth([string]$Path, [string]$Method = 'GET', [string]$Body = '', [string]$CSRF = '') {
    $headers = @{Origin=$authOrigin}
    if ($CSRF) { $headers['X-CSRF-Token'] = $CSRF }
    $arguments = @{Uri="$authOrigin$Path"; Method=$Method; Headers=$headers; WebSession=$authSession; SkipHttpErrorCheck=$true; TimeoutSec=10; NoProxy=$true}
    if ($Method -eq 'POST') { $arguments.ContentType='application/json'; $arguments.Body=$Body }
    try { return Invoke-WebRequest @arguments } catch { throw 'Authentication smoke request failed; raw request and response suppressed.' }
}
function Read-AuthResult($Response) {
    try { $body = $Response.Content | ConvertFrom-Json } catch { throw 'Authentication smoke JSON response invalid.' }
    if ($body.data.csrf_token) { $authSecrets.Add([string]$body.data.csrf_token) }
    foreach ($cookie in $authSession.Cookies.GetCookies([Uri]$authOrigin)) { if ($cookie.Name -eq 'proxyloom_session') { $authSecrets.Add($cookie.Value) } }
    return $body
}

try {
    Invoke-Compose -Arguments @('config','--quiet')
    if ($Build) { Invoke-Compose -Arguments @('build','api','runner') }
    $ownsProject = $true
    Invoke-Compose -Arguments @('up','--detach','--wait','--wait-timeout','120')
    Assert-Step 'api ready' (Wait-Status '/readyz' 200)
    Assert-Step 'api live' (Wait-Status '/healthz' 200)
    Assert-Step 'frontend served' (Wait-Status '/' 200)
    Assert-Step 'node API requires authentication' (Wait-Status '/api/v1/nodes' 401)
    Assert-Step 'subscription API requires authentication' (Wait-Status '/api/v1/subscriptions' 401)
    Assert-Step 'invalid subscription token returns 404' (Wait-Status '/s/EXAMPLE_ONLY/test' 404)
    Assert-Step 'internal jobs are not mounted on public API' (Wait-Status '/internal/v1/jobs' 404)

    $anonymous = Invoke-SmokeAuth '/api/v1/auth/me'
    Assert-Step 'management requires a session' ($anonymous.StatusCode -eq 401)
    $smokePassword = [Convert]::ToBase64String([Security.Cryptography.RandomNumberGenerator]::GetBytes(32))
    $authSecrets.Add($smokePassword)
    $setupToken = [IO.File]::ReadAllText((Join-Path $repoRoot 'deploy/secrets/local/setup_token'))
    $setupBody = @{setup_token=$setupToken; username='smokeadmin'; password=$smokePassword} | ConvertTo-Json -Compress
    $initialized = Invoke-SmokeAuth '/api/v1/setup' 'POST' $setupBody
    Assert-Step 'administrator initializes through Linux API' ($initialized.StatusCode -eq 201)
    $currentAdmin = Read-AuthResult $initialized
    Assert-Step 'administrator role matches the public contract' ($currentAdmin.data.role -eq 'administrator' -and $currentAdmin.data.csrf_token.Length -eq 43)
    $duplicateSetup = Invoke-SmokeAuth '/api/v1/setup' 'POST' $setupBody
    Assert-Step 'initialization cannot be reused' ($duplicateSetup.StatusCode -eq 409)
    $noCSRF = Invoke-SmokeAuth '/api/v1/auth/reauth' 'POST' (@{password=$smokePassword} | ConvertTo-Json -Compress)
    Assert-Step 'authenticated write requires CSRF' ($noCSRF.StatusCode -eq 403)
    $reauth = Invoke-SmokeAuth '/api/v1/auth/reauth' 'POST' (@{password=$smokePassword} | ConvertTo-Json -Compress) $currentAdmin.data.csrf_token
    Assert-Step 'explicit reauthentication succeeds' ($reauth.StatusCode -eq 200)
    $currentAdmin = Read-AuthResult $reauth

    $presetResponse = Invoke-SmokeAuth '/api/v1/client-presets'
    Assert-Step 'startup provisions five reviewed presets' ($presetResponse.StatusCode -eq 200)
    $presetPage = $presetResponse.Content | ConvertFrom-Json
    $presetItems = @($presetPage.data)
    $badPresets = @($presetItems | Where-Object {
        $_.preset.platform -ne 'linux' -or $_.preset.import_method -ne 'file' -or
        $_.preset.local_listener.protocol -ne 'socks5' -or $_.preset.local_listener.listen -ne '127.0.0.1' -or
        $_.preset.local_listener.port -ne 1080 -or $_.preset.dns_mode -ne 'profile'
    })
    Assert-Step 'all five presets use reviewed Linux file constraints' ($presetItems.Count -eq 5 -and $badPresets.Count -eq 0 -and -not $presetPage.page.next_cursor)
    $disabledControls = @($presetItems | Where-Object { -not $_.preset.control_api.enabled })
    $enabledControls = @($presetItems | Where-Object { $_.preset.control_api.enabled })
    $badControls = @($enabledControls | Where-Object {
        $_.preset.control_api.listen -ne '127.0.0.1' -or
        -not (($_.preset.core_family -eq 'sing-box' -and $_.preset.control_api.port -eq 17812) -or
              ($_.preset.core_family -eq 'mihomo' -and $_.preset.control_api.port -eq 17813))
    })
    Assert-Step 'original presets stay disabled and controlled variants are explicit' ($disabledControls.Count -eq 3 -and $enabledControls.Count -eq 2 -and $badControls.Count -eq 0)
    $presetBaseline = $presetItems | Sort-Object { $_.metadata.resource_id } | ConvertTo-Json -Depth 16 -Compress

    Invoke-Compose -Arguments @('run','--rm','--no-deps','migrate')
    Invoke-Compose -Arguments @('run','--rm','--no-deps','migrate','migrate','up')
    $migrationStatus = Invoke-Compose -Arguments @('run','--rm','--no-deps','migrate','migrate','status') -Capture
    Assert-NoSecrets $migrationStatus
    $statusLine = @($migrationStatus -split "`n" | Where-Object { $_ -match '^\{"applied":' }) | Select-Object -Last 1
    $statusObject = $statusLine | ConvertFrom-Json
    $expectedMigrations = @(Get-ChildItem -LiteralPath (Join-Path $repoRoot 'migrations') -Filter '*.up.sql' -File).Count
    Assert-Step 'migrations repeat safely' ($statusObject.current -eq $true -and $statusObject.applied -eq $expectedMigrations -and $statusObject.latest -eq $expectedMigrations -and $statusObject.pending -eq 0)

    # Two real migration processes contend on the same PostgreSQL transaction lock.
    $migrationProcesses = [Collections.Generic.List[Diagnostics.Process]]::new()
    try {
        foreach ($attempt in 1..2) {
            $startInfo = [Diagnostics.ProcessStartInfo]::new('docker')
            $startInfo.UseShellExecute = $false
            $startInfo.CreateNoWindow = $true
            $startInfo.RedirectStandardOutput = $true
            $startInfo.RedirectStandardError = $true
            foreach ($argument in @('compose','--project-name',$ProjectName,'--file',$compose,'run','--rm','--no-deps','migrate')) { $startInfo.ArgumentList.Add($argument) }
            $migrationProcesses.Add([Diagnostics.Process]::Start($startInfo))
        }
        foreach ($process in $migrationProcesses) {
            if (-not $process.WaitForExit(45000)) { throw 'Concurrent migration process timed out.' }
            Assert-NoSecrets ($process.StandardOutput.ReadToEnd() + $process.StandardError.ReadToEnd())
            Assert-Step "concurrent migration process $($process.Id)" ($process.ExitCode -eq 0)
        }
    } finally {
        foreach ($process in $migrationProcesses) {
            if (-not $process.HasExited) { $process.Kill($true); $process.WaitForExit() }
            $process.Dispose()
        }
    }

    $sql = "SELECT has_database_privilege('proxyloom','proxyloom','CONNECT') AND has_table_privilege('proxyloom','public.proxyloom_schema_migrations','SELECT') AND NOT has_schema_privilege('proxyloom','public','CREATE') AND NOT has_table_privilege('proxyloom','public.proxyloom_schema_migrations','UPDATE') AND NOT (SELECT rolsuper OR rolcreatedb OR rolcreaterole FROM pg_roles WHERE rolname='proxyloom');"
    $permissions = Invoke-Compose -Arguments @('exec','-T','postgres','psql','-U','proxyloom_bootstrap','-d','proxyloom','-Atc',$sql) -Capture
    Assert-Step 'runtime database privileges are restricted' ($permissions.Trim() -eq 't')

    $missingOutput = & docker compose --project-name $ProjectName --file $compose run --rm --no-deps --env PROXYLOOM_MASTER_KEY_FILE=/nonexistent api serve 2>&1
    $missingExit = $LASTEXITCODE
    Assert-NoSecrets ($missingOutput -join "`n")
    Assert-Step 'missing key fails startup' ($missingExit -ne 0)

    Invoke-Compose -Arguments @('stop','runner')
    Assert-Step 'runner offline does not block api readiness' (Wait-Status '/readyz' 200)
    Invoke-Compose -Arguments @('start','runner')
    Invoke-Compose -Arguments @('stop','postgres')
    Assert-Step 'database outage blocks readiness' (Wait-Status '/readyz' 503)
    Assert-Step 'database outage preserves liveness' (Wait-Status '/healthz' 200)
    $unavailableAuth = Invoke-SmokeAuth '/api/v1/auth/me'
    Assert-Step 'database outage rejects authenticated reads' ($unavailableAuth.StatusCode -eq 503)
    Invoke-Compose -Arguments @('start','postgres')
    Assert-Step 'database recovery restores readiness' (Wait-Status '/readyz' 200)
    Invoke-Compose -Arguments @('restart','api')
    Assert-Step 'api restart preserves migration state' (Wait-Status '/readyz' 200)
    $restoredAuth = Invoke-SmokeAuth '/api/v1/auth/me'
    Assert-Step 'API restart preserves the database session' ($restoredAuth.StatusCode -eq 200)
    $currentAdmin = Read-AuthResult $restoredAuth
    $restartedPresets = Invoke-SmokeAuth '/api/v1/client-presets'
    Assert-Step 'restarted API can read presets' ($restartedPresets.StatusCode -eq 200)
    $restartedPage = $restartedPresets.Content | ConvertFrom-Json
    $restartedBaseline = @($restartedPage.data) | Sort-Object { $_.metadata.resource_id } | ConvertTo-Json -Depth 16 -Compress
    Assert-Step 'API restart preserves preset identities revisions and values' ($restartedBaseline -ceq $presetBaseline)
    $logout = Invoke-SmokeAuth '/api/v1/auth/logout' 'POST' '{}' $currentAdmin.data.csrf_token
    Assert-Step 'logout revokes the session' ($logout.StatusCode -eq 200)
    $afterLogout = Invoke-SmokeAuth '/api/v1/auth/me'
    Assert-Step 'logged out requests are unauthorized' ($afterLogout.StatusCode -eq 401)

    $runnerId = Invoke-Compose -Arguments @('ps','--quiet','runner') -Capture
    $runnerInspection = & docker inspect $runnerId | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0) { throw 'Runner inspection failed.' }
    $networkNames = @($runnerInspection[0].NetworkSettings.Networks.PSObject.Properties.Name)
    Assert-Step 'runner excluded from database network' (-not ($networkNames -match '_data$'))
    Assert-Step 'runner mounts no secrets' (@($runnerInspection[0].Mounts | Where-Object { $_.Destination -like '/run/secrets*' }).Count -eq 0)
    Assert-Step 'runner has no published ports' ($null -eq $runnerInspection[0].HostConfig.PortBindings -or @($runnerInspection[0].HostConfig.PortBindings.PSObject.Properties).Count -eq 0)

    Invoke-Compose -Arguments @('stop','api','runner')
    $apiId = Invoke-Compose -Arguments @('ps','--all','--quiet','api') -Capture
    $apiState = (& docker inspect --format '{{json .State}}' $apiId) | ConvertFrom-Json
    $runnerState = (& docker inspect --format '{{json .State}}' $runnerId) | ConvertFrom-Json
    Assert-Step 'services exit gracefully' ($apiState.ExitCode -eq 0 -and $runnerState.ExitCode -eq 0 -and -not $apiState.OOMKilled -and -not $runnerState.OOMKilled)
    $logs = Invoke-Compose -Arguments @('logs','--no-color') -Capture
    Assert-NoSecrets $logs
    Assert-Step 'captured logs contain no development secrets' $true
    $completed = $true
    if ($KeepRunning) { Invoke-Compose -Arguments @('start','api','runner') }
} finally {
    $imageIDs = @(& docker image inspect --format '{{.Id}}' proxyloom-api:m0-dev proxyloom-runner:m0-dev 2>$null)
    $cleanupResult = 'not_owned'
    $cleanupFailed = $false
    if ($ownsProject -and (-not $KeepRunning -or -not $completed)) {
        & docker compose --project-name $ProjectName --file $compose down --volumes --timeout 15 2>&1 | Out-Null
        $cleanupFailed = $LASTEXITCODE -ne 0
        foreach ($kind in @('container','network','volume')) {
            $inventoryArgs = @($kind,'ls','--quiet','--filter',"label=com.docker.compose.project=$ProjectName")
            if ($kind -eq 'container') { $inventoryArgs += '--all' }
            $remaining = & docker @inventoryArgs 2>&1
            if ($LASTEXITCODE -ne 0 -or ($remaining -join '').Trim()) { $cleanupFailed = $true }
        }
        $cleanupResult = if ($cleanupFailed) { 'fail' } else { 'pass' }
    } elseif ($ownsProject) { $cleanupResult = 'retained_by_request' }
    if ($cleanupFailed) { $completed = $false }
    $report = @{started_at=$startedAt; ended_at=[DateTime]::UtcNow.ToString('o'); project=$ProjectName; platform=$platform; completed=$completed; cleanup=$cleanupResult; image_ids=$imageIDs; checks=@($steps.ToArray()); scope='T-002/T-006/T-022 development Linux API authentication and preset startup smoke; not G1 or production acceptance'}
    [IO.File]::WriteAllText($reportPath, ($report | ConvertTo-Json -Depth 8), [Text.UTF8Encoding]::new($false))
    $env:PROXYLOOM_DEV_PORT = $oldPort
    if ($cleanupFailed) { throw 'Owned smoke resources were not completely cleaned; inspect the smoke report.' }
}
