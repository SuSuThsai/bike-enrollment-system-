#requires -Version 5.1
<#
.SYNOPSIS
    运行 k6 混合场景压测（tests/load/mixed.js），跑完做对账与泄漏检查。

.DESCRIPTION
    流程：
      1. 读取 .env（JWT_SECRET、MOCK_PAY_SECRET、MySQL 连接）；
      2. 以 LOCK_TTL_SECONDS=10 重启 api 容器（临时把锁 TTL 压缩到 10 秒）；
      3. 创建专用赛事 + 组别（total_slots 足够大），预置 chain / setup 用户池；
      4. 自检锁 TTL 确实生效（创建一支探针锁，查 expires_at 只比 now 大 ~10s）；
      5. 运行 k6（60% 完整链路 / 20% 锁定后不提交 / 20% 重复回调 5 次，持续 3 分钟）；
      6. 执行 go run ./cmd/reconcile，原样输出对账结果；
      7. 检查锁泄漏与支付超时遗漏，并输出数量；
      8. 可选 -ResetAfter：清理本组数据、用户与组别，并恢复 api 的默认锁 TTL。

    退出码：0 = 对账通过且两项检查为 0；1 = 有异常。

.EXAMPLE
    powershell -NoProfile -ExecutionPolicy Bypass -File tests\load\run-mixed.ps1 -UseDocker -ResetAfter

.EXAMPLE
    # 10 秒冒烟（验证脚本链路，不跑满 3 分钟）
    powershell -NoProfile -ExecutionPolicy Bypass -File tests\load\run-mixed.ps1 -UseDocker -Duration 10s -ChainUserPool 2000 -ResetAfter
#>
[CmdletBinding()]
param(
    [string]$BaseUrl = 'http://127.0.0.1:8080/api/v1',
    [string]$DockerBaseUrl = 'http://api:8080/api/v1',
    [string]$DockerNetwork = 'bike-enroll-backend',
    [string]$Duration = '3m',
    [int]$TotalSlots = 20000,
    [int]$ChainVus = 30,
    [int]$AbandonVus = 10,
    [int]$DuplicateVus = 10,
    [int]$ChainUserPool = 12000,
    [int]$SetupOrders = 20,
    [int]$LockTtlSeconds = 10,
    [string]$GroupId = '',
    [string]$EventId = '',
    [string]$DisclaimerVersion = 'v1.0',
    [string]$K6Path = 'k6',
    [switch]$UseDocker,
    [switch]$ResetAfter,
    # 跳过测后重启 api（用于保留现场排查锁泄漏）；恢复默认 TTL 需手动执行 docker compose up -d api
    [switch]$KeepApi
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$root = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$envPath = Join-Path $root '.env'
if (-not (Test-Path -LiteralPath $envPath)) {
    throw "未找到 $envPath；请先按 docs/deployment.md 执行 Copy-Item .env.example .env"
}

# ---------------------------------------------------------------------------
# 工具函数
# ---------------------------------------------------------------------------
function Read-DotEnv {
    param([string]$Path)
    $map = @{}
    foreach ($line in Get-Content -LiteralPath $Path) {
        $trimmed = $line.Trim()
        if (-not $trimmed -or $trimmed.StartsWith('#')) { continue }
        $index = $trimmed.IndexOf('=')
        if ($index -lt 1) { continue }
        $key = $trimmed.Substring(0, $index).Trim()
        $value = $trimmed.Substring($index + 1).Trim().Trim('"').Trim("'")
        $map[$key] = $value
    }
    return $map
}

function Invoke-Native {
    param([string[]]$Arguments)
    $previous = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $output = & docker @Arguments 2>&1
        $exitCode = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $previous
    }
    return [pscustomobject]@{ ExitCode = $exitCode; Lines = @($output) }
}

function Get-MySQLScalar {
    param([string]$Sql)
    $composeArgs = @(
        'compose', 'exec', '-T', '-e', "MYSQL_PWD=$($script:MySqlPassword)", 'mysql', 'mysql',
        "-u$($script:MySqlUser)",
        '--default-character-set=utf8mb4', $script:MySqlDatabase,
        '-N', '-B', '-e', $Sql
    )
    $result = Invoke-Native -Arguments $composeArgs
    $lines = @($result.Lines | Where-Object { "$_".Trim() -ne '' -and "$_" -notmatch 'Using a password' })
    if ($result.ExitCode -ne 0) {
        throw "SQL 执行失败（exit=$($result.ExitCode)）：$Sql`n$($lines -join "`n")"
    }
    if (-not $lines) {
        throw "SQL 查询无返回：$Sql"
    }
    return ("$($lines[0])").Trim()
}

function Invoke-MySQLExec {
    param([string]$Sql)
    $composeArgs = @(
        'compose', 'exec', '-T', '-e', "MYSQL_PWD=$($script:MySqlPassword)", 'mysql', 'mysql',
        "-u$($script:MySqlUser)",
        '--default-character-set=utf8mb4', $script:MySqlDatabase,
        '-e', $Sql
    )
    $result = Invoke-Native -Arguments $composeArgs
    if ($result.ExitCode -ne 0) {
        $lines = @($result.Lines | Where-Object { "$_".Trim() -ne '' })
        throw "SQL 执行失败（exit=$($result.ExitCode)）：$Sql`n$($lines -join "`n")"
    }
}

function Get-MySQLRows {
    param([string]$Sql)
    $composeArgs = @(
        'compose', 'exec', '-T', '-e', "MYSQL_PWD=$($script:MySqlPassword)", 'mysql', 'mysql',
        "-u$($script:MySqlUser)",
        '--default-character-set=utf8mb4', $script:MySqlDatabase,
        '-N', '-B', '-e', $Sql
    )
    $result = Invoke-Native -Arguments $composeArgs
    if ($result.ExitCode -ne 0) {
        throw "SQL 执行失败（exit=$($result.ExitCode)）：$Sql"
    }
    return @($result.Lines | Where-Object { "$_".Trim() -ne '' -and "$_" -notmatch 'Using a password' })
}

function Get-MySQLRootRows {
    # 诊断用：读 information_schema.innodb_trx / performance_schema 需要 PROCESS 权限
    param([string]$Sql)
    if (-not $script:MySqlRootPassword) { return @() }
    $composeArgs = @(
        'compose', 'exec', '-T', 'mysql', 'mysql', '-uroot', "-p$($script:MySqlRootPassword)",
        '--default-character-set=utf8mb4', $script:MySqlDatabase,
        '-N', '-B', '-e', $Sql
    )
    $result = Invoke-Native -Arguments $composeArgs
    if ($result.ExitCode -ne 0) { return @() }
    return @($result.Lines | Where-Object { "$_".Trim() -ne '' -and "$_" -notmatch 'Using a password' })
}

function Invoke-MySQLFile {
    # 大批量 INSERT 走 stdin，避免 Windows 命令行长度限制
    param([string]$Path)
    $previous = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $output = Get-Content -LiteralPath $Path -Raw |
            & docker compose exec -T -e "MYSQL_PWD=$($script:MySqlPassword)" mysql mysql `
                "-u$($script:MySqlUser)" --default-character-set=utf8mb4 $script:MySqlDatabase 2>&1
        $exitCode = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $previous
    }
    if ($exitCode -ne 0) {
        throw "批量 SQL 执行失败（exit=$exitCode）：$($output -join "`n")"
    }
}

function Get-RedisValue {
    param([string]$Key)
    $result = Invoke-Native -Arguments @('compose', 'exec', '-T', 'redis', 'redis-cli', 'GET', $Key)
    if ($result.ExitCode -ne 0) {
        throw "读取 Redis key 失败（exit=$($result.ExitCode)）：$Key"
    }
    $value = @($result.Lines | Where-Object { "$_".Trim() -ne '' }) | Select-Object -First 1
    if ($null -eq $value) { return '' }
    return "$value".Trim()
}

function Remove-RedisKeysByPattern {
    param([string]$Pattern, [int]$ChunkSize = 40)
    $scanResult = Invoke-Native -Arguments @('compose', 'exec', '-T', 'redis', 'redis-cli', '--scan', '--pattern', $Pattern)
    if ($scanResult.ExitCode -ne 0) {
        throw "扫描 Redis key 失败（pattern=$Pattern）"
    }
    $keys = @($scanResult.Lines | Where-Object { "$_".Trim() -ne '' })
    if ($keys.Count -eq 0) { return 0 }
    $removed = 0
    for ($offset = 0; $offset -lt $keys.Count; $offset += $ChunkSize) {
        $last = [Math]::Min($offset + $ChunkSize - 1, $keys.Count - 1)
        $chunk = $keys[$offset..$last]
        $delResult = Invoke-Native -Arguments (@('compose', 'exec', '-T', 'redis', 'redis-cli', 'DEL') + $chunk)
        if ($delResult.ExitCode -ne 0) {
            throw "删除 Redis key 失败（pattern=$Pattern）"
        }
        $countLine = @($delResult.Lines | Where-Object { "$_".Trim() -match '^\d+$' }) | Select-Object -First 1
        if ($countLine) { $removed += [int]$countLine }
    }
    return $removed
}

function New-Jwt {
    param([string]$Secret, [string]$Subject, [string]$Role)
    $base64Url = {
        param([string]$Text)
        $bytes = [Text.Encoding]::UTF8.GetBytes($Text)
        [Convert]::ToBase64String($bytes).TrimEnd('=').Replace('+', '-').Replace('/', '_')
    }
    $header = & $base64Url '{"alg":"HS256","typ":"JWT"}'
    $now = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
    $payload = & $base64Url (@{
            role = $Role
            sub  = $Subject
            iss  = 'bike-enroll-api'
            iat  = $now
            exp  = $now + 3600
        } | ConvertTo-Json -Compress)
    $hmac = [System.Security.Cryptography.HMACSHA256]::new([Text.Encoding]::UTF8.GetBytes($Secret))
    $signature = [Convert]::ToBase64String(
        $hmac.ComputeHash([Text.Encoding]::UTF8.GetBytes("$header.$payload"))
    ).TrimEnd('=').Replace('+', '-').Replace('/', '_')
    return "$header.$payload.$signature"
}

function Write-Section {
    param([string]$Title)
    Write-Host ''
    Write-Host "=== $Title ==="
}

# ---------------------------------------------------------------------------
# 0. 读取配置
# ---------------------------------------------------------------------------
$envValues = Read-DotEnv -Path $envPath
$jwtSecret = if ($envValues.ContainsKey('JWT_SECRET')) { $envValues['JWT_SECRET'] } else { '' }
$mockPaySecret = if ($envValues.ContainsKey('MOCK_PAY_SECRET')) { $envValues['MOCK_PAY_SECRET'] } else { '' }
if (-not $jwtSecret -or $jwtSecret.Length -lt 32) { throw '.env 中的 JWT_SECRET 缺失或不足 32 字节' }
if (-not $mockPaySecret) { throw '.env 中的 MOCK_PAY_SECRET 为空' }

$script:MySqlUser = if ($envValues.ContainsKey('MYSQL_USER')) { $envValues['MYSQL_USER'] } else { 'bike' }
$script:MySqlPassword = if ($envValues.ContainsKey('MYSQL_PASSWORD')) { $envValues['MYSQL_PASSWORD'] } else { throw 'MYSQL_PASSWORD 未设置：请在 .env 或环境变量中提供，不要使用默认弱口令' }
$script:MySqlRootPassword = if ($envValues.ContainsKey('MYSQL_ROOT_PASSWORD')) { $envValues['MYSQL_ROOT_PASSWORD'] } else { '' }
$script:MySqlDatabase = if ($envValues.ContainsKey('MYSQL_DATABASE')) { $envValues['MYSQL_DATABASE'] } else { 'bike_enroll' }

$tag = 'mx-' + [DateTimeOffset]::UtcNow.ToUnixTimeSeconds() + '-' + (Get-Random -Minimum 100 -Maximum 999)
$summaryPath = Join-Path $root 'tests/load/results/mixed-summary.json'
$sqlFilePath = Join-Path $env:TEMP "mixed-users-$tag.sql"

Push-Location $root
$createdGroup = $false
$createdEvent = $false
$failedChecks = 0
try {
    # -----------------------------------------------------------------------
    # 1. 临时把锁 TTL 压缩到 LockTtlSeconds 并重启 api
    # -----------------------------------------------------------------------
    Write-Section "重启 api：LOCK_TTL_SECONDS=$LockTtlSeconds"
    $env:LOCK_TTL_SECONDS = "$LockTtlSeconds"
    $composeUp = Invoke-Native -Arguments @('compose', 'up', '-d', 'api')
    if ($composeUp.ExitCode -ne 0) {
        throw "docker compose up -d api 失败：$($composeUp.Lines -join ' ')"
    }

    $healthUrl = ($BaseUrl -replace '/api/v1$', '') + '/healthz'
    $healthy = $false
    for ($i = 0; $i -lt 60; $i++) {
        Start-Sleep -Milliseconds 500
        try {
            $resp = Invoke-WebRequest -Uri $healthUrl -UseBasicParsing -TimeoutSec 2
            if ([int]$resp.StatusCode -eq 200) { $healthy = $true; break }
        }
        catch { }
    }
    if (-not $healthy) { throw "api 未在 30 秒内就绪：$healthUrl" }
    Write-Host "[api] 已以 LOCK_TTL_SECONDS=$LockTtlSeconds 就绪"

    $adminToken = New-Jwt -Secret $jwtSecret -Subject 'mixed-load-admin' -Role 'admin'
    $adminHeaders = @{ Authorization = "Bearer $adminToken" }
    $startTime = (Get-Date).AddDays(3).ToString('yyyy-MM-ddTHH:mm:sszzz')
    $startDate = (Get-Date).AddDays(3)
    $registrationOpenAt = (Get-Date).AddHours(-1).ToString('yyyy-MM-ddTHH:mm:sszzz')
    $registrationDeadline = $startDate.AddHours(-1).ToString('yyyy-MM-ddTHH:mm:sszzz')
    $endTime = $startDate.AddHours(4).ToString('yyyy-MM-ddTHH:mm:sszzz')

    # -----------------------------------------------------------------------
    # 2. 专用赛事 + 组别
    # -----------------------------------------------------------------------
    if (-not $GroupId) {
        Write-Section '创建专用赛事与组别'
        $eventBody = @{
            title                 = "混合压测赛事-$tag"
            registration_open_at  = $registrationOpenAt
            registration_deadline = $registrationDeadline
            start_time            = $startTime
            end_time              = $endTime
            location              = '压测场地'
            description           = '混合场景压测（自动创建，可用 -ResetAfter 清理）'
            rules                 = ''
        } | ConvertTo-Json
        $event = Invoke-RestMethod -Method Post -Uri "$BaseUrl/admin/events" -Headers $adminHeaders -ContentType 'application/json' -Body $eventBody
        $EventId = $event.id
        $createdEvent = $true

        $groupBody = @{
            event_id           = $EventId
            name               = '混合组'
            price              = 1
            total_slots        = $TotalSlots
            start_time         = $startTime
            form_schema        = @{ fields = @() }
            disclaimer_version = $DisclaimerVersion
        } | ConvertTo-Json -Depth 5
        $group = Invoke-RestMethod -Method Post -Uri "$BaseUrl/admin/groups" -Headers $adminHeaders -ContentType 'application/json' -Body $groupBody
        $GroupId = $group.id
        $createdGroup = $true
    }
    if (-not $EventId) {
        $EventId = Get-MySQLScalar "SELECT event_id FROM ``groups`` WHERE id = '$GroupId';"
    }
    Write-Host "[组别] group=$GroupId event=$EventId total_slots=$TotalSlots"

    # -----------------------------------------------------------------------
    # 3. 自检锁 TTL 是否真的生效
    # -----------------------------------------------------------------------
    $probeSubject = "mix-$tag-probe"
    $probeToken = New-Jwt -Secret $jwtSecret -Subject $probeSubject -Role 'rider'
    $probeLock = Invoke-RestMethod -Method Post -Uri "$BaseUrl/enrollments/lock" `
        -Headers @{ Authorization = "Bearer $probeToken" } -ContentType 'application/json' `
        -Body (@{ group_id = $GroupId } | ConvertTo-Json)
    $probeRespSeconds = [int](Get-MySQLScalar "SELECT TIMESTAMPDIFF(SECOND, NOW(3), expires_at) FROM enrollment_locks WHERE token = '$($probeLock.lock_token)';")
    if ($probeRespSeconds -gt 60) {
        throw ("LOCK_TTL_SECONDS 未生效：探针锁 expires_at 还有 {0} 秒（期望约 {1} 秒）。请确认镜像已包含该配置（docker compose build api）。" -f $probeRespSeconds, $LockTtlSeconds)
    }
    Write-Host "[自检] 锁 TTL 生效：探针锁剩余 $probeRespSeconds 秒（目标 $LockTtlSeconds 秒）"
    Invoke-MySQLExec "DELETE FROM enrollment_locks WHERE token = '$($probeLock.lock_token)';"
    Invoke-RestMethod -Method Post -Uri "$BaseUrl/admin/groups/$GroupId/rebuild-remaining?force=true" -Headers $adminHeaders -ContentType 'application/json' | Out-Null

    # -----------------------------------------------------------------------
    # 4. 预置用户池（chain + setup）
    # -----------------------------------------------------------------------
    Write-Section "预置用户：chain=$ChainUserPool，setup=$SetupOrders"
    $rows = New-Object System.Collections.Generic.List[string]
    for ($i = 0; $i -lt $ChainUserPool; $i++) {
        $id = "mix-$tag-c-$i"
        $rows.Add("('$id','wx-$id','','load','','rider',NOW(3),NOW(3))")
    }
    for ($i = 0; $i -lt $SetupOrders; $i++) {
        $id = "mix-$tag-s-$i"
        $rows.Add("('$id','wx-$id','','load','','rider',NOW(3),NOW(3))")
    }
    $sb = New-Object System.Text.StringBuilder
    $chunkSize = 500
    for ($offset = 0; $offset -lt $rows.Count; $offset += $chunkSize) {
        $lastIndex = [Math]::Min($offset + $chunkSize - 1, $rows.Count - 1)
        [void]$sb.AppendLine("INSERT INTO users (id, openid, unionid, nickname, avatar_url, role, created_at, updated_at) VALUES " + ($rows.GetRange($offset, $lastIndex - $offset + 1) -join ',') + ';')
    }
    [IO.File]::WriteAllText($sqlFilePath, $sb.ToString(), [Text.UTF8Encoding]::new($false))
    Invoke-MySQLFile -Path $sqlFilePath
    Write-Host "[用户] 已预置 $($rows.Count) 个用户（前缀 mix-$tag-）"

    # -----------------------------------------------------------------------
    # 5. 运行 k6 混合场景
    # -----------------------------------------------------------------------
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $summaryPath) | Out-Null
    Remove-Item -LiteralPath $summaryPath -ErrorAction SilentlyContinue

    Write-Section "运行 k6 混合场景：$Duration（chain=$ChainVus / abandon=$AbandonVus / duplicate=$DuplicateVus VU）"
    $k6StartedAt = (Get-Date).ToString('yyyy-MM-ddTHH:mm:sszzz')
    $k6Env = @(
        "BASE_URL=$BaseUrl",
        "GROUP_ID=$GroupId",
        "RUN_TAG=$tag",
        "JWT_SECRET=$jwtSecret",
        "MOCK_PAY_SECRET=$mockPaySecret",
        "DISCLAIMER_VERSION=$DisclaimerVersion",
        "DURATION=$Duration",
        "CHAIN_VUS=$ChainVus",
        "ABANDON_VUS=$AbandonVus",
        "DUP_VUS=$DuplicateVus",
        "CHAIN_USER_POOL=$ChainUserPool",
        "SETUP_ORDERS=$SetupOrders",
        "ABANDON_WAIT_SECONDS=16"
    )

    if ($UseDocker) {
        Write-Host "[模式] docker run grafana/k6（网络 $DockerNetwork）"
        $dockerArgs = @('run', '--rm', '-i', "--network=$DockerNetwork", '-v', "$($root):/work", '-w', '/work')
        $dockerArgs += @('-e', "BASE_URL=$DockerBaseUrl")
        foreach ($item in ($k6Env | Where-Object { -not $_.StartsWith('BASE_URL=') })) {
            $dockerArgs += @('-e', $item)
        }
        $dockerArgs += @('grafana/k6:latest', 'run', 'tests/load/mixed.js')
        & docker @dockerArgs
    }
    else {
        $k6Command = Get-Command $K6Path -ErrorAction SilentlyContinue
        if (-not $k6Command) { throw "未找到 k6（$K6Path）。可安装 k6，或用 -UseDocker" }
        $k6Args = @('run')
        foreach ($item in $k6Env) { $k6Args += @('-e', $item) }
        $k6Args += @('tests/load/mixed.js')
        & $K6Path @k6Args
    }
    $k6Exit = $LASTEXITCODE
    if ($k6Exit -ne 0) {
        Write-Warning "k6 退出码 $k6Exit（99 表示 threshold 失败；其余为脚本错误）"
    }

    if (Test-Path -LiteralPath $summaryPath) {
        $summary = Get-Content -LiteralPath $summaryPath -Raw | ConvertFrom-Json
        Write-Host ("[k6] http_reqs={0} duration={1}ms chain_complete={2} abandon_lock_200={3} dup_notify_200={4}" -f `
                $summary.http_requests, $summary.duration_ms, $summary.chain.complete, $summary.abandon.lock_200, $summary.duplicate.notify_200)
    }

    # 先落盘测试期间的 api 日志，便于事后排查锁过期/超时任务的行为（容器随后会重启，日志会丢）
    $logDumpPath = Join-Path $root "tests/load/results/mixed-api-$tag.log"
    $logResult = Invoke-Native -Arguments @('compose', 'logs', '--since', $k6StartedAt, 'api')
    $logResult.Lines | Set-Content -LiteralPath $logDumpPath -Encoding UTF8
    Write-Host ("[日志] api 日志已保存：tests/load/results/mixed-api-$tag.log（{0} 行）" -f $logResult.Lines.Count)

    # -----------------------------------------------------------------------
    # 6. 对账：go run ./cmd/reconcile
    # -----------------------------------------------------------------------
    Write-Section '对账脚本输出（go run ./cmd/reconcile）'
    $env:MYSQL_USER = $script:MySqlUser
    $env:MYSQL_PASSWORD = $script:MySqlPassword
    $env:MYSQL_DATABASE = $script:MySqlDatabase
    $env:GOCACHE = Join-Path $env:TEMP 'gocache'
    $env:GOTMPDIR = Join-Path $env:TEMP 'gotmp'
    New-Item -ItemType Directory -Force -Path $env:GOCACHE, $env:GOTMPDIR | Out-Null

    $reconcileExit = 0
    $reconcileOutput = @()
    Push-Location (Join-Path $root 'server')
    try {
        for ($attempt = 1; $attempt -le 3; $attempt++) {
            $previous = $ErrorActionPreference
            $ErrorActionPreference = 'Continue'
            try {
                $reconcileOutput = @(& go run ./cmd/reconcile 2>&1)
                $reconcileExit = $LASTEXITCODE
            }
            finally {
                $ErrorActionPreference = $previous
            }
            $joined = $reconcileOutput -join "`n"
            if ($reconcileExit -eq 0 -or $joined -notmatch 'job lock held') { break }
            Write-Host "[对账] 任务锁被占用，3 秒后重试（$attempt/3）"
            Start-Sleep -Seconds 3
        }
    }
    finally {
        Pop-Location
    }

    foreach ($line in $reconcileOutput) { Write-Host "$line" }
    Write-Host ("[对账] 退出码 = {0}" -f $reconcileExit)

    # -----------------------------------------------------------------------
    # 7. 附加检查：锁泄漏 / 支付超时遗漏
    # -----------------------------------------------------------------------
    Write-Section '附加检查'
    $lockLeak = [int](Get-MySQLScalar "SELECT COUNT(*) FROM enrollment_locks WHERE status = 'active' AND expires_at < NOW(3) - INTERVAL 60 SECOND;")
    $lockLeakScoped = [int](Get-MySQLScalar "SELECT COUNT(*) FROM enrollment_locks WHERE group_id = '$GroupId' AND status = 'active' AND expires_at < NOW(3) - INTERVAL 60 SECOND;")
    $payMissed = [int](Get-MySQLScalar "SELECT COUNT(*) FROM orders WHERE status = 'pending_payment' AND created_at < NOW(3) - INTERVAL 15 MINUTE;")
    $payMissedScoped = [int](Get-MySQLScalar "SELECT COUNT(*) FROM orders WHERE group_id = '$GroupId' AND status = 'pending_payment' AND created_at < NOW(3) - INTERVAL 15 MINUTE;")

    Write-Host ("锁泄漏数量（active 且 expires_at 已过期超过 1 分钟）      : {0}（本组 {1}）" -f $lockLeak, $lockLeakScoped)
    Write-Host ("超时遗漏数量（pending_payment 超过 15 分钟未释放）        : {0}（本组 {1}）" -f $payMissed, $payMissedScoped)

    # 任务每 60 秒扫描一次，锁在「下一次 tick 之前」最多会自然超期约 60 秒；
    # 打印本组 active 锁的最大超期秒数，用于区分「调度周期边界」与「真漏释放」。
    $maxOverdue = Get-MySQLScalar "SELECT COALESCE(MAX(TIMESTAMPDIFF(SECOND, expires_at, NOW(3))), 0) FROM enrollment_locks WHERE group_id = '$GroupId' AND status = 'active';"
    Write-Host ("[诊断] 本组 active 锁最大超期秒数（≈60 属调度周期边界，>90 需排查）: {0}" -f $maxOverdue)

    if ($lockLeakScoped -gt 0) {
        $leakDumpPath = Join-Path $root "tests/load/results/mixed-leak-$tag.tsv"
        $leakDetailSql = "SELECT CONCAT_WS(CHAR(9), token, user_id, status, DATE_FORMAT(created_at, '%H:%i:%s.%f'), DATE_FORMAT(updated_at, '%H:%i:%s.%f'), DATE_FORMAT(expires_at, '%H:%i:%s.%f'), TIMESTAMPDIFF(SECOND, expires_at, NOW(3))) FROM enrollment_locks WHERE group_id = '$GroupId' AND status = 'active' AND expires_at < NOW(3) - INTERVAL 60 SECOND ORDER BY expires_at;"
        Get-MySQLRows $leakDetailSql | Set-Content -LiteralPath $leakDumpPath -Encoding UTF8
        Write-Host "[诊断] 锁泄漏明细已保存：tests/load/results/mixed-leak-$tag.tsv"
        Write-Host '[诊断] 锁泄漏明细（token | user_id | status | created | updated | expires | 已过期秒数）：'
        foreach ($row in (Get-MySQLRows $leakDetailSql)) { Write-Host "  $row" }
        Write-Host '[诊断] api 日志中的 lock-expiry 记录（自本轮 k6 开始）：'
        $logResult = Invoke-Native -Arguments @('compose', 'logs', '--since', $k6StartedAt, 'api')
        $matched = @($logResult.Lines | Where-Object { "$_" -match 'lock-expiry|reclaim' } | Select-Object -Last 10)
        if ($matched.Count -eq 0) {
            Write-Host '  （无匹配日志）'
        }
        else {
            foreach ($line in $matched) { Write-Host "  $line" }
        }

        # 关键取证：谁还持有这些 enrollment_locks 行（innodb_trx / data_locks 需要 PROCESS 权限，用 root 查）
        try {
            Write-Host '[诊断] 长事务（innodb_trx，age>=5s；trx | state | age | locked_rows | query）：'
            $trxRows = @(Get-MySQLRootRows "SELECT CONCAT_WS(CHAR(9), trx_id, trx_state, TIMESTAMPDIFF(SECOND, trx_started, NOW(3)), trx_rows_locked, LEFT(IFNULL(trx_query,''),120)) FROM information_schema.innodb_trx WHERE TIMESTAMPDIFF(SECOND, trx_started, NOW(3)) >= 5;")
            if ($trxRows.Count -eq 0) { Write-Host '  （无）' } else { foreach ($row in $trxRows) { Write-Host "  $row" } }

            Write-Host '[诊断] enrollment_locks 上的行锁持有者（type | mode | status | trx | thread | sql）：'
            $lockRows = @(Get-MySQLRootRows "SELECT CONCAT_WS(CHAR(9), l.LOCK_TYPE, l.LOCK_MODE, l.LOCK_STATUS, l.ENGINE_TRANSACTION_ID, l.THREAD_ID, LEFT(IFNULL(t.PROCESSLIST_INFO,''),120)) FROM performance_schema.data_locks l LEFT JOIN performance_schema.threads t ON t.THREAD_ID = l.THREAD_ID WHERE l.OBJECT_NAME = 'enrollment_locks' LIMIT 20;")
            if ($lockRows.Count -eq 0) { Write-Host '  （无）' } else { foreach ($row in $lockRows) { Write-Host "  $row" } }

            Write-Host '[诊断] 当前锁等待对（等待方 trx/query | 阻塞方 trx/query）：'
            $waitRows = @(Get-MySQLRootRows "SELECT CONCAT_WS(CHAR(9), w.REQUESTING_ENGINE_TRANSACTION_ID, LEFT(IFNULL(r.PROCESSLIST_INFO,''),100), w.BLOCKING_ENGINE_TRANSACTION_ID, LEFT(IFNULL(b.PROCESSLIST_INFO,''),100)) FROM performance_schema.data_lock_waits w LEFT JOIN performance_schema.threads r ON r.THREAD_ID = w.REQUESTING_ENGINE_THREAD_ID LEFT JOIN performance_schema.threads b ON b.THREAD_ID = w.BLOCKING_ENGINE_THREAD_ID;")
            if ($waitRows.Count -eq 0) { Write-Host '  （无）' } else { foreach ($row in $waitRows) { Write-Host "  $row" } }
        }
        catch {
            Write-Host ("[诊断] 行锁取证失败（不影响后续复位）：{0}" -f $_.Exception.Message)
        }
    }

    if ($reconcileExit -ne 0) { $failedChecks++ }
    if ($lockLeak -ne 0) { $failedChecks++ }
    if ($payMissed -ne 0) { $failedChecks++ }

    # -----------------------------------------------------------------------
    # 8. 可选复位
    # -----------------------------------------------------------------------
    if ($ResetAfter) {
        Write-Section '复位（清理本组数据/用户/组别）'
        foreach ($table in @('disclaimer_signatures', 'orders', 'enrollments', 'enrollment_locks')) {
            $deleted = Get-MySQLScalar "DELETE FROM $table WHERE group_id = '$GroupId'; SELECT ROW_COUNT();"
            Write-Host ("已删除 {0,-22}: {1} 行" -f $table, $deleted)
        }
        $deletedUsers = Get-MySQLScalar "DELETE FROM users WHERE openid LIKE 'wx-mix-$tag-%'; SELECT ROW_COUNT();"
        Write-Host ("已删除 {0,-22}: {1} 行" -f 'users（压测用户）', $deletedUsers)

        Invoke-RestMethod -Method Post -Uri "$BaseUrl/admin/groups/$GroupId/rebuild-remaining?force=true" -Headers $adminHeaders -ContentType 'application/json' | Out-Null
        Remove-RedisKeysByPattern -Pattern "group:$GroupId:remaining" | Out-Null
        Remove-RedisKeysByPattern -Pattern 'lock:*' | Out-Null

        if ($createdGroup) {
            Invoke-RestMethod -Method Delete -Uri "$BaseUrl/admin/groups/$GroupId" -Headers $adminHeaders | Out-Null
            Write-Host '[组别] 已删除'
        }
        if ($createdEvent) {
            Invoke-RestMethod -Method Delete -Uri "$BaseUrl/admin/events/$EventId" -Headers $adminHeaders | Out-Null
            Write-Host '[赛事] 已删除'
        }
    }
}
finally {
    # 恢复 api 的默认锁 TTL，并清理临时文件
    if (Test-Path Env:LOCK_TTL_SECONDS) {
        Remove-Item Env:LOCK_TTL_SECONDS -ErrorAction SilentlyContinue
    }
    if ($KeepApi) {
        Write-Host '[提示] -KeepApi 生效：api 仍运行在临时 LOCK_TTL，未重启；恢复默认请执行 docker compose up -d api'
    }
    else {
        Invoke-Native -Arguments @('compose', 'up', '-d', 'api') | Out-Null
    }
    if (Test-Path -LiteralPath $sqlFilePath) { Remove-Item -LiteralPath $sqlFilePath -Force }
    Pop-Location
}

Write-Section '结论'
if ($failedChecks -eq 0) {
    Write-Host '对账通过，无锁泄漏、无支付超时遗漏。'
    exit 0
}
Write-Host ("有 {0} 项检查未通过。" -f $failedChecks)
exit 1
