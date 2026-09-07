param([switch]$WriteTasks)
$ErrorActionPreference = 'Stop'
$repo = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
$planPath = Join-Path $repo 'docs/PLAN.md'
$tasksPath = Join-Path $repo 'docs/PLAN.tasks.json'
if ($WriteTasks -and (Test-Path -LiteralPath $tasksPath)) {
    throw 'Task list already exists. Refusing to overwrite task status, review or evidence history; run without -WriteTasks to validate.'
}
$tasks = @()
$workPackage = $null
foreach ($line in Get-Content -LiteralPath $planPath) {
    if ($line -match '^## 6\.\d+ (WP-\d+) ') { $workPackage = $Matches[1] }
    if ($line -notmatch '^\| \*\*(T-\d{3}) (.+?)\*\*<br>(M\d) · (.+?)<br>(\d+)–(\d+) 人日 \| (.+?) \| (.+?) \| (.+?) \|$') { continue }
    $row = $Matches.Clone()
    $tasks += [ordered]@{
        id = $row[1]; work_package = $workPackage; title = $row[2]
        milestone = $row[3]; role = $row[4]
        estimate_person_days = [ordered]@{ min = [int]$row[5]; max = [int]$row[6] }
        implementation = $row[7]
        dependencies = @([regex]::Matches($row[8], 'T-\d{3}') | ForEach-Object Value)
        acceptance = $row[9].Replace('<br>', "`n")
        status = $(if ($row[1] -in @('T-001', 'T-002', 'T-003')) { 'in_progress' } else { 'not_started' })
    }
}
if ($WriteTasks) {
    $document = [ordered]@{
        schema_version = 1; source = 'docs/PLAN.md'; baseline_date = '2026-09-07'
        status_note = '首轮仅 T-001～T-003 实施中；依赖完成和评审尚待确认，不代表 ready、done 或 G0 通过。其他任务保留初始基线。'
        tasks = $tasks
    }
    [IO.File]::WriteAllText($tasksPath, ($document | ConvertTo-Json -Depth 10) + "`n", [Text.UTF8Encoding]::new($false))
}
$actual = (Get-Content -LiteralPath $tasksPath -Raw | ConvertFrom-Json).tasks
if ($tasks.Count -ne 60 -or $actual.Count -ne 60) { throw 'Expected exactly 60 tasks' }
$ids = @($actual.id | Sort-Object -Unique)
if ($ids.Count -ne 60) { throw 'Duplicate IDs' }
foreach ($n in 1..60) { if ('T-{0:d3}' -f $n -notin $ids) { throw "Missing ID $n" } }
foreach ($expected in $tasks) {
    $entry = $actual | Where-Object id -EQ $expected.id
    foreach ($key in @('work_package', 'title', 'milestone', 'role', 'implementation', 'acceptance')) {
        if ($entry.$key -cne $expected[$key]) { throw "$($entry.id): $key differs from PLAN" }
    }
    if (($entry.dependencies -join ',') -cne ($expected.dependencies -join ',')) { throw "$($entry.id): dependencies differ" }
    foreach ($bound in @('min', 'max')) {
        if ($entry.estimate_person_days.$bound -ne $expected.estimate_person_days[$bound]) { throw "$($entry.id): estimate differs" }
    }
    if ($entry.status -notin @('not_started','ready','in_progress','in_review','done','blocked')) { throw 'Unknown status' }
    foreach ($dep in $entry.dependencies) { if ($dep -notin $ids) { throw "Unknown dependency: $dep" } }
}
$visited = @()
while ($visited.Count -lt 60) {
    $available = @($actual | Where-Object { $_.id -notin $visited -and @($_.dependencies | Where-Object { $_ -notin $visited }).Count -eq 0 })
    if ($available.Count -eq 0) { throw 'Dependency cycle' }
    $visited += $available.id
}
'PASS: 60 unique sequential IDs; all metadata, direct dependencies and estimates match PLAN; dependency DAG is acyclic.'
$totals = @{ M0 = @(11,30,51); M1 = @(25,86,141); M2 = @(10,34,54); M3 = @(14,48,78) }
foreach ($stage in @('M0','M1','M2','M3')) {
    $group = @($actual | Where-Object milestone -EQ $stage)
    $min = ($group.estimate_person_days.min | Measure-Object -Sum).Sum
    $max = ($group.estimate_person_days.max | Measure-Object -Sum).Sum
    if ($group.Count -ne $totals[$stage][0] -or $min -ne $totals[$stage][1] -or $max -ne $totals[$stage][2]) { throw "$stage totals differ" }
    "PASS: $stage tasks=$($group.Count), estimate=$min-$max person-days"
}
'PASS: P0 total = 60 tasks, 198-324 person-days.'
