#requires -Version 5.1
<#
.SYNOPSIS
    运行 k6 抢名额压测（tests/load/lock-oversell.js），并对账校验是否超卖。

.DESCRIPTION
    流程：
      1. 读取仓库根目录 .env 中的 JWT_SECRET 与 MySQL 连接信息；
      2. 预检：Redis 计数存在且等于 total_slots、MySQL 无 active lock（否则压测结果无意义）；
      3. 调用 k6（本机二进制，或 -UseDocker 用 grafana/k6 容器）；
      4. 读取 tests/load/results/lock-oversell-summary.json；
      5. 直连 Redis / MySQL 做对账，输出四项结果并断言：
           成功锁定数 <= total_slots
           Redis remaining + MySQL active lock 数 = total_slots
      6. 可选 -ResetAfter：删除本次压测的 lock 记录，并调用
         POST /admin/groups/{group_id}/rebuild-remaining?force=true 把计数复位。

    退出码：0 = 全部断言通过；1 = 有断言失败或执行异常。

.EXAMPLE
    # 本机已安装 k6
    .\tests\load\run-lock-oversell.ps1

.EXAMPLE
    # 未安装 k6，用容器跑；测完自动复位
    .\tests\load\run-lock-oversell.ps1 -UseDocker -ResetAfter
#>
[CmdletBinding()]
param(
    [string]$GroupId = '88f8f9de-1d46-4356-9cfd-68e01dd68b10',
    [string]$BaseUrl = 'http://127.0.0.1:8080/api/v1',
    [int]$Vus = 200,
    [int]$IterationsPerVu = 10,
    [string]$K6Path = 'k6',
    [switch]$UseDocker,
    [string]$DockerNetwork = 'bike-enroll-backend',
    [string]$DockerBaseUrl = 'http://api:8080/api/v1',
    [string]$ResetBaseUrl = 'http://127.0.0.1:8080/api/v1',
    [switch]$ResetAfter,
    [switch]$SkipPrecheck
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
    # PowerShell 5.1 下 $ErrorActionPreference='Stop' 会把原生命令的 stderr 当成终止错误，
    # 这里局部降级为 Continue，改由退出码判断真实失败。
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
    return [pscustomobject]@{
        ExitCode = $exitCode
        Lines    = @($output)
    }
}

function Get-MySQLScalar {
    param([string]$Sql)
    # 密码通过 MYSQL_PWD 传入，避免 "Using a password" 警告污染输出
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
    # 按 pattern 扫描并分批删除；返回删除数量。lock:* 都是辅助 key，MySQL 才是权威状态。
    param([string]$Pattern, [int]$ChunkSize = 40)
    $scanResult = Invoke-Native -Arguments @('compose', 'exec', '-T', 'redis', 'redis-cli', '--scan', '--pattern', $Pattern)
    if ($scanResult.ExitCode -ne 0) {
        throw "扫描 Redis key 失败（pattern=$Pattern, exit=$($scanResult.ExitCode)）"
    }
    $keys = @($scanResult.Lines | Where-Object { "$_".Trim() -ne '' })
    if ($keys.Count -eq 0) {
        return 0
    }
    $removed = 0
    for ($offset = 0; $offset -lt $keys.Count; $offset += $ChunkSize) {
        $last = [Math]::Min($offset + $ChunkSize - 1, $keys.Count - 1)
        $chunk = $keys[$offset..$last]
        $delResult = Invoke-Native -Arguments (@('compose', 'exec', '-T', 'redis', 'redis-cli', 'DEL') + $chunk)
        if ($delResult.ExitCode -ne 0) {
            throw "删除 Redis key 失败（pattern=$Pattern, exit=$($delResult.ExitCode)）"
        }
        $countLine = @($delResult.Lines | Where-Object { "$_".Trim() -match '^\d+$' }) | Select-Object -First 1
        if ($countLine) {
            $removed += [int]$countLine
        }
    }
    return $removed
}

function Get-RedisKeyCount {
    param([string]$Pattern)
    $scanResult = Invoke-Native -Arguments @('compose', 'exec', '-T', 'redis', 'redis-cli', '--scan', '--pattern', $Pattern)
    if ($scanResult.ExitCode -ne 0) {
        throw "扫描 Redis key 失败（pattern=$Pattern）"
    }
    return @($scanResult.Lines | Where-Object { "$_".Trim() -ne '' }).Count
}

function New-AdminToken {
    param([string]$Secret)
    $base64Url = {
        param([string]$Text)
        $bytes = [Text.Encoding]::UTF8.GetBytes($Text)
        [Convert]::ToBase64String($bytes).TrimEnd('=').Replace('+', '-').Replace('/', '_')
    }
    $header = & $base64Url '{"alg":"HS256","typ":"JWT"}'
    $now = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
    $payload = & $base64Url (@{
            role = 'admin'
            sub  = 'load-test-admin'
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
if (-not $jwtSecret -or $jwtSecret.Length -lt 32) {
    throw '.env 中的 JWT_SECRET 缺失或不足 32 字节，无法本地签发压测 JWT'
}

$script:MySqlUser = if ($envValues.ContainsKey('MYSQL_USER')) { $envValues['MYSQL_USER'] } else { 'bike' }
$script:MySqlPassword = if ($envValues.ContainsKey('MYSQL_PASSWORD')) { $envValues['MYSQL_PASSWORD'] } else { throw 'MYSQL_PASSWORD 未设置：请在 .env 或环境变量中提供，不要使用默认弱口令' }
$script:MySqlDatabase = if ($envValues.ContainsKey('MYSQL_DATABASE')) { $envValues['MYSQL_DATABASE'] } else { 'bike_enroll' }

$summaryPath = Join-Path $root 'tests/load/results/lock-oversell-summary.json'
$plannedRequests = $Vus * $IterationsPerVu

Push-Location $root
$failedAssertions = 0
try {
    # -----------------------------------------------------------------------
    # 1. 预检：必须是干净的名额状态
    # -----------------------------------------------------------------------
    $totalSlots = [int](Get-MySQLScalar "SELECT total_slots FROM ``groups`` WHERE id = '$GroupId';")
    if ($totalSlots -le 0) {
        throw "组别 $GroupId 不存在或 total_slots 非法"
    }

    if (-not $SkipPrecheck) {
        $preRemaining = Get-RedisValue "group:${GroupId}:remaining"
        if (-not $preRemaining) {
            throw "Redis 不存在 group:${GroupId}:remaining。请先重建名额：POST /admin/groups/$GroupId/rebuild-remaining"
        }
        $preActive = [int](Get-MySQLScalar "SELECT COUNT(*) FROM enrollment_locks WHERE group_id = '$GroupId' AND status = 'active';")
        if ([int]$preRemaining -ne $totalSlots -or $preActive -ne 0) {
            throw @"
组别不是干净状态，压测结果没有意义：
  Redis remaining = $preRemaining（期望 $totalSlots）
  MySQL active locks = $preActive（期望 0）
请先复位：删除 enrollment_locks 中该组别的记录，再调用
  POST /admin/groups/$GroupId/rebuild-remaining?force=true
"@
        }
        Write-Host "[预检] 组别 $GroupId 干净：remaining=$preRemaining / total_slots=$totalSlots / active locks=0"
    }

    # -----------------------------------------------------------------------
    # 2. 运行 k6
    # -----------------------------------------------------------------------
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $summaryPath) | Out-Null
    Remove-Item -LiteralPath $summaryPath -ErrorAction SilentlyContinue

    Write-Section "运行 k6：$Vus VU × $IterationsPerVu 次 = $plannedRequests 次请求"
    $k6Args = @(
        'run',
        '-e', "BASE_URL=$BaseUrl",
        '-e', "GROUP_ID=$GroupId",
        '-e', "JWT_SECRET=$jwtSecret",
        '-e', "VUS=$Vus",
        '-e', "ITERATIONS=$IterationsPerVu",
        '-e', "MAX_SUCCESS=$totalSlots",
        'tests/load/lock-oversell.js'
    )

    if ($UseDocker) {
        Write-Host "[模式] docker run grafana/k6（网络 $DockerNetwork）"
        & docker run --rm -i "--network=$DockerNetwork" `
            -v "${root}:/work" -w /work `
            -e "BASE_URL=$DockerBaseUrl" `
            -e "GROUP_ID=$GroupId" `
            -e "JWT_SECRET=$jwtSecret" `
            -e "VUS=$Vus" `
            -e "ITERATIONS=$IterationsPerVu" `
            -e "MAX_SUCCESS=$totalSlots" `
            grafana/k6:latest run tests/load/lock-oversell.js
    }
    else {
        $k6Command = Get-Command $K6Path -ErrorAction SilentlyContinue
        if (-not $k6Command) {
            throw "未找到 k6（$K6Path）。可安装 k6，或用 -UseDocker 以容器方式运行（需 grafana/k6 镜像）"
        }
        & $k6Path @k6Args
    }
    $k6Exit = $LASTEXITCODE
    if ($k6Exit -ne 0) {
        Write-Warning "k6 退出码 $k6Exit（99 通常表示 threshold 断言失败，例如 200 数量超过 $totalSlots）"
    }

    # -----------------------------------------------------------------------
    # 3. 读取 k6 汇总
    # -----------------------------------------------------------------------
    if (-not (Test-Path -LiteralPath $summaryPath)) {
        throw "未生成 $summaryPath（k6 可能提前失败，退出码 $k6Exit）"
    }
    $summary = Get-Content -LiteralPath $summaryPath -Raw | ConvertFrom-Json

    $success = [int]$summary.http_200
    $soldOut = [int]$summary.http_409
    $other = [int]$summary.http_other
    $actualRequests = [int]$summary.http_requests

    # -----------------------------------------------------------------------
    # 4. 对账查询（压测结束后立刻执行，此时 lock 尚未过期）
    # -----------------------------------------------------------------------
    $redisKey = "group:${GroupId}:remaining"
    $redisRaw = Get-RedisValue $redisKey
    $activeLocks = [int](Get-MySQLScalar "SELECT COUNT(*) FROM enrollment_locks WHERE group_id = '$GroupId' AND status = 'active';")

    $remaining = $null
    if ($redisRaw -ne '') { $remaining = [int]$redisRaw }

    Write-Section '压测结果'
    Write-Host ("请求计划 / 实际      : {0} / {1}" -f $plannedRequests, $actualRequests)
    Write-Host ("HTTP 200 成功锁定    : {0}" -f $success)
    Write-Host ("HTTP 409 名额已满    : {0}" -f $soldOut)
    Write-Host ("其它状态码           : {0}" -f $other)
    Write-Host ("k6 运行时长          : {0} ms" -f $summary.duration_ms)
    if ($summary.lock_latency_ms) {
        $latency = $summary.lock_latency_ms
        Write-Host ("lock 延迟(ms)        : avg={0:N2} p95={1:N2} p99={2:N2} max={3:N2}" -f `
                $latency.avg, $latency.'p(95)', $latency.'p(99)', $latency.max)
    }

    Write-Section '对账结果'
    Write-Host ("1) 成功锁定数（HTTP 200）           : {0}" -f $success)
    if ($null -eq $remaining) {
        Write-Host ("2) Redis {0,-28}: <key 缺失>" -f $redisKey)
    }
    else {
        Write-Host ("2) Redis {0,-28}: {1}" -f $redisKey, $remaining)
    }
    Write-Host ("3) MySQL active lock 记录数         : {0}" -f $activeLocks)
    Write-Host ("   groups.total_slots               : {0}" -f $totalSlots)

    Write-Section '断言'
    $assertOversell = $success -le $totalSlots
    if ($assertOversell) {
        Write-Host ("[PASS] 成功锁定数 {0} <= total_slots {1}" -f $success, $totalSlots)
    }
    else {
        Write-Host ("[FAIL] 成功锁定数 {0} > total_slots {1}（发生超卖）" -f $success, $totalSlots)
        $failedAssertions++
    }

    if ($null -eq $remaining) {
        Write-Host '[FAIL] Redis 计数 key 缺失，无法完成对账等式校验'
        $failedAssertions++
    }
    else {
        $sum = $remaining + $activeLocks
        if ($sum -eq $totalSlots) {
            Write-Host ("[PASS] Redis remaining {0} + active locks {1} = {2} = total_slots" -f $remaining, $activeLocks, $sum)
        }
        else {
            Write-Host ("[FAIL] Redis remaining {0} + active locks {1} = {2} != total_slots {3}" -f $remaining, $activeLocks, $sum, $totalSlots)
            $failedAssertions++
        }
    }

    $responseTotal = $success + $soldOut + $other
    if ($responseTotal -eq $plannedRequests) {
        Write-Host ("[PASS] 状态码合计 {0} = 计划请求数 {1}" -f $responseTotal, $plannedRequests)
    }
    else {
        Write-Host ("[WARN] 状态码合计 {0} != 计划请求数 {1}（实际 http_reqs={2}）" -f $responseTotal, $plannedRequests, $actualRequests)
    }
    if ($other -gt 0) {
        Write-Host ("[WARN] 存在 {0} 个非 200/409 响应，请查看 k6 汇总中的 status_counts" -f $other)
    }

    # -----------------------------------------------------------------------
    # 5. 可选复位：删除压测锁记录 + 重建名额
    # -----------------------------------------------------------------------
    if ($ResetAfter) {
        Write-Section '复位压测环境'

        $deleted = Get-MySQLScalar "DELETE FROM enrollment_locks WHERE group_id = '$GroupId'; SELECT ROW_COUNT();"
        Write-Host ("已删除该组别 lock 记录：{0} 行" -f $deleted)

        # lock:* 全部是辅助 key（lock:{token} 只是过期辅助、lock:{user}:{group} 是 15s 串行化锁，
        # lock:rollback:{token} 是回补幂等键）；权威状态在 MySQL，压测复位时统一清空，
        # 避免上一轮残留的 token TTL 干扰下一轮判断。
        $removedAuxKeys = Remove-RedisKeysByPattern -Pattern 'lock:*'
        Write-Host ("已清理 Redis 辅助 key（lock:*）：{0} 个" -f $removedAuxKeys)

        $token = New-AdminToken -Secret $jwtSecret
        $headers = @{ Authorization = "Bearer $token" }
        $resetUri = "$ResetBaseUrl/admin/groups/$GroupId/rebuild-remaining?force=true"
        $response = Invoke-RestMethod -Method Post -Uri $resetUri -Headers $headers -ContentType 'application/json'
        $previous = if ($response.PSObject.Properties.Name -contains 'previous_remaining') {
            $response.previous_remaining
        }
        else {
            'n/a'
        }
        Write-Host ("名额重建：mode={0} remaining={1} previous={2}" -f `
                $response.mode, $response.remaining_slots, $previous)

        $resetRemaining = Get-RedisValue "group:${GroupId}:remaining"
        if ([int]$resetRemaining -eq $totalSlots) {
            Write-Host ("[PASS] 复位后 Redis remaining = {0}" -f $resetRemaining)
        }
        else {
            Write-Host ("[FAIL] 复位后 Redis remaining = {0}，期望 {1}" -f $resetRemaining, $totalSlots)
            $failedAssertions++
        }

        $leftoverLocks = Get-RedisKeyCount -Pattern 'lock:*'
        if ($leftoverLocks -eq 0) {
            Write-Host '[PASS] 复位后 lock:* 辅助 key 已清空'
        }
        else {
            Write-Host ("[FAIL] 复位后仍有 {0} 个 lock:* 辅助 key" -f $leftoverLocks)
            $failedAssertions++
        }
    }
    else {
        Write-Host ''
        Write-Host "提示：本次压测占用了 $success 个名额（锁 TTL 15 分钟）。"
        Write-Host "      需要立刻复位请加 -ResetAfter，或等待 lock 过期任务回补。"
    }
}
finally {
    Pop-Location
}

Write-Section '结论'
if ($failedAssertions -eq 0) {
    Write-Host '全部断言通过：未超卖，且 Redis 计数与 MySQL 活跃锁对账平衡。'
    exit 0
}

Write-Host ("有 {0} 项断言失败。" -f $failedAssertions)
exit 1
