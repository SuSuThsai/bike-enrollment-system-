#requires -Version 5.1
<#
.SYNOPSIS
    运行 k6 支付回调幂等压测（tests/load/notify-idempotent.js），并对账校验。

.DESCRIPTION
    流程：
      1. 读取仓库根目录 .env 中的 JWT_SECRET、MOCK_PAY_SECRET 与 MySQL 连接信息；
      2. 预检：组别必须是干净状态（无 lock/报名/订单，remaining = total_slots）；
      3. 从 groups 表取 event_id 与 disclaimer_version，传给 k6；
      4. k6 setup() 创建 ORDERS 笔 pending_payment 订单（lock 已消费），
         然后 ORDERS×DUPLICATES 个 VU 各发一次 mock 支付回调；
      5. 对账：
           - 每笔 Order.status 均为 paid，paid_at 已设置且 updated_at = paid_at（只迁移一次）；
           - 每笔 Enrollment.status 均为 paid，且 paid_at 与 Order 一致；
           - Redis remaining 与「回调前」相同（回调不扣减/回补名额）；
           - 从 API 日志核对每单 applied=1 次、duplicate=DUPLICATES-1 次；
      6. 可选 -ResetAfter：删除本组别的报名/订单/锁/协议签署记录，清空 lock:* 辅助 key，
         并调用 POST /admin/groups/{group_id}/rebuild-remaining?force=true 复位名额。

    退出码：0 = 全部断言通过；1 = 有断言失败或执行异常。

.EXAMPLE
    powershell -NoProfile -ExecutionPolicy Bypass -File tests\load\run-notify-idempotent.ps1 -UseDocker -ResetAfter
#>
[CmdletBinding()]
param(
    [string]$GroupId = '88f8f9de-1d46-4356-9cfd-68e01dd68b10',
    [string]$BaseUrl = 'http://127.0.0.1:8080/api/v1',
    [string]$EventId = '',
    [string]$DisclaimerVersion = '',
    [int]$Orders = 10,
    [int]$Duplicates = 10,
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
    # PowerShell 5.1 下 $ErrorActionPreference='Stop' 会把原生命令 stderr 当终止错误，
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
            sub  = 'notify-load-admin'
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
if (-not $jwtSecret -or $jwtSecret.Length -lt 32) {
    throw '.env 中的 JWT_SECRET 缺失或不足 32 字节'
}
if (-not $mockPaySecret) {
    throw '.env 中的 MOCK_PAY_SECRET 为空，/internal/mock-pay/* 会返回 403'
}

$script:MySqlUser = if ($envValues.ContainsKey('MYSQL_USER')) { $envValues['MYSQL_USER'] } else { 'bike' }
$script:MySqlPassword = if ($envValues.ContainsKey('MYSQL_PASSWORD')) { $envValues['MYSQL_PASSWORD'] } else { throw 'MYSQL_PASSWORD 未设置：请在 .env 或环境变量中提供，不要使用默认弱口令' }
$script:MySqlDatabase = if ($envValues.ContainsKey('MYSQL_DATABASE')) { $envValues['MYSQL_DATABASE'] } else { 'bike_enroll' }

$summaryPath = Join-Path $root 'tests/load/results/notify-idempotent-summary.json'
$plannedRequests = $Orders * $Duplicates

Push-Location $root
$failedAssertions = 0
try {
    # -----------------------------------------------------------------------
    # 1. 预检 + 读取组别配置
    # -----------------------------------------------------------------------
    $groupConfig = Get-MySQLScalar "SELECT CONCAT_WS('|', event_id, disclaimer_version, total_slots) FROM ``groups`` WHERE id = '$GroupId';"
    $configParts = $groupConfig -split '\|'
    if ($configParts.Count -lt 3) {
        throw "组别 $GroupId 不存在"
    }
    $dbEventId = $configParts[0]
    $dbDisclaimerVersion = $configParts[1]
    $totalSlots = [int]$configParts[2]

    if (-not $EventId) { $EventId = $dbEventId }
    if (-not $DisclaimerVersion) { $DisclaimerVersion = $dbDisclaimerVersion }
    Write-Host "[配置] group=$GroupId event=$EventId total_slots=$totalSlots disclaimer_version=$DisclaimerVersion"

    if (-not $SkipPrecheck) {
        $preRemaining = Get-RedisValue "group:${GroupId}:remaining"
        if (-not $preRemaining) {
            throw "Redis 不存在 group:${GroupId}:remaining。请先重建名额：POST /admin/groups/$GroupId/rebuild-remaining"
        }
        $preCounts = (Get-MySQLScalar "SELECT CONCAT_WS('|', (SELECT COUNT(*) FROM enrollment_locks WHERE group_id = '$GroupId'), (SELECT COUNT(*) FROM enrollments WHERE group_id = '$GroupId'), (SELECT COUNT(*) FROM orders WHERE group_id = '$GroupId'), (SELECT COUNT(*) FROM disclaimer_signatures WHERE group_id = '$GroupId'));") -split '\|'
        $preLocks = [int]$preCounts[0]
        $preEnrollments = [int]$preCounts[1]
        $preOrders = [int]$preCounts[2]
        $preDisclaimers = [int]$preCounts[3]

        if ([int]$preRemaining -ne $totalSlots -or $preLocks -ne 0 -or $preEnrollments -ne 0 -or $preOrders -ne 0 -or $preDisclaimers -ne 0) {
            throw ("组别不是干净状态，压测结果没有意义：remaining={0}（期望 {1}）, locks={2}, enrollments={3}, orders={4}, disclaimers={5}。请先复位（见 README 的 -ResetAfter 说明）。" -f `
                    $preRemaining, $totalSlots, $preLocks, $preEnrollments, $preOrders, $preDisclaimers)
        }
        Write-Host "[预检] 组别干净：remaining=$preRemaining / total_slots=$totalSlots / lock=0 / enrollment=0 / order=0"
    }

    # -----------------------------------------------------------------------
    # 1.5 预置压测用户：mock 回调内部会校验订单所属用户存在（users 表）
    # -----------------------------------------------------------------------
    $runTag = 'notify-' + [DateTimeOffset]::UtcNow.ToUnixTimeSeconds() + '-' + (Get-Random -Minimum 1000 -Maximum 9999)
    $userValues = @()
    for ($i = 0; $i -lt $Orders; $i++) {
        $userId = "$runTag-$i"
        $openid = "wx-$runTag-$i"
        $userValues += "('$userId','$openid','','load-user','','rider',NOW(3),NOW(3))"
    }
    $seedSql = "INSERT INTO users (id, openid, unionid, nickname, avatar_url, role, created_at, updated_at) VALUES " + ($userValues -join ',') + ';'
    Invoke-MySQLExec -Sql $seedSql
    Write-Host "[预置] 已创建 $Orders 个压测用户（前缀 $runTag），供订单与 mock 回调使用"

    # -----------------------------------------------------------------------
    # 2. 运行 k6
    # -----------------------------------------------------------------------
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $summaryPath) | Out-Null
    Remove-Item -LiteralPath $summaryPath -ErrorAction SilentlyContinue

    Write-Section "运行 k6：创建 $Orders 笔 pending_payment 订单，每单并发 $Duplicates 次回调"
    $k6Env = @(
        "BASE_URL=$BaseUrl",
        "GROUP_ID=$GroupId",
        "EVENT_ID=$EventId",
        "RUN_TAG=$runTag",
        "JWT_SECRET=$jwtSecret",
        "MOCK_PAY_SECRET=$mockPaySecret",
        "DISCLAIMER_VERSION=$DisclaimerVersion",
        "ORDERS=$Orders",
        "DUPLICATES=$Duplicates"
    )

    if ($UseDocker) {
        Write-Host "[模式] docker run grafana/k6（网络 $DockerNetwork）"
        $dockerArgs = @(
            'run', '--rm', '-i', "--network=$DockerNetwork",
            '-v', "$($root):/work", '-w', '/work'
        )
        $dockerArgs += @('-e', "BASE_URL=$DockerBaseUrl")
        foreach ($item in ($k6Env | Where-Object { -not $_.StartsWith('BASE_URL=') })) {
            $dockerArgs += @('-e', $item)
        }
        $dockerArgs += @('grafana/k6:latest', 'run', 'tests/load/notify-idempotent.js')
        & docker @dockerArgs
    }
    else {
        $k6Command = Get-Command $K6Path -ErrorAction SilentlyContinue
        if (-not $k6Command) {
            throw "未找到 k6（$K6Path）。可安装 k6，或用 -UseDocker 以容器方式运行"
        }
        $k6Args = @('run')
        foreach ($item in $k6Env) {
            $k6Args += @('-e', $item)
        }
        $k6Args += @('tests/load/notify-idempotent.js')
        & $K6Path @k6Args
    }
    $k6Exit = $LASTEXITCODE
    if ($k6Exit -ne 0) {
        Write-Warning "k6 退出码 $k6Exit（99 通常表示 threshold 断言失败，例如回调未全部返回 200）"
    }

    # -----------------------------------------------------------------------
    # 3. 读取 k6 汇总
    # -----------------------------------------------------------------------
    if (-not (Test-Path -LiteralPath $summaryPath)) {
        throw "未生成 $summaryPath（k6 可能提前失败，退出码 $k6Exit）"
    }
    $summary = Get-Content -LiteralPath $summaryPath -Raw | ConvertFrom-Json

    $http200 = [int]$summary.http_200
    $httpOther = [int]$summary.http_other
    $actualRequests = [int]$summary.http_requests
    $remainingBefore = $summary.redis_remaining_before
    if ($null -eq $remainingBefore -or "$remainingBefore" -eq '') {
        Write-Warning 'k6 汇总中缺少 redis_remaining_before，无法比较回调前后 remaining'
        $remainingBefore = $null
    }
    else {
        $remainingBefore = [int]$remainingBefore
    }

    # -----------------------------------------------------------------------
    # 4. 对账查询
    # -----------------------------------------------------------------------
    $sqlRows = Get-MySQLRows "SELECT CONCAT_WS('|', o.id, o.status, IFNULL(DATE_FORMAT(o.paid_at, '%Y-%m-%d %H:%i:%s'), ''), IF(o.updated_at = o.paid_at, 1, 0), IF(o.paid_at IS NULL, 0, 1), IFNULL(o.transaction_id, ''), IFNULL(e.status, ''), IF(e.paid_at = o.paid_at, 1, 0), IFNULL(DATE_FORMAT(e.paid_at, '%Y-%m-%d %H:%i:%s'), '')) FROM orders o LEFT JOIN enrollments e ON e.order_id = o.id WHERE o.group_id = '$GroupId' ORDER BY o.created_at;"

    $orderRows = @()
    foreach ($row in $sqlRows) {
        $parts = $row -split '\|'
        if ($parts.Count -lt 9) { continue }
        $orderRows += [pscustomobject]@{
            OrderID       = $parts[0]
            OrderStatus   = $parts[1]
            OrderPaidAt   = $parts[2]
            UpdatedEqPaid = ($parts[3] -eq '1')
            PaidAtSet     = ($parts[4] -eq '1')
            TransactionID = $parts[5]
            EnrollStatus  = $parts[6]
            EnrollPaidEq  = ($parts[7] -eq '1')
            EnrollPaidAt  = $parts[8]
        }
    }

    $redisRaw = Get-RedisValue "group:${GroupId}:remaining"
    $remainingAfter = $null
    if ($redisRaw -ne '') { $remainingAfter = [int]$redisRaw }

    # 从 API 日志核对：
    #   - 每单应恰好 1 次 applied、Duplicates-1 次 duplicate；
    #   - 每单收到 Duplicates 次 mock 回调，且 TransactionID 互不相同
    #     （mock 每次随机生成；只有这样才能证明幂等键不是 TransactionID）。
    $logResult = Invoke-Native -Arguments @('compose', 'logs', '--tail', '20000', 'api')
    $logLines = @($logResult.Lines)
    $appliedCounts = @{}
    $duplicateCounts = @{}
    $mockCallCounts = @{}
    $txIdsByOrder = @{}
    foreach ($order in $orderRows) {
        $appliedCounts[$order.OrderID] = 0
        $duplicateCounts[$order.OrderID] = 0
        $mockCallCounts[$order.OrderID] = 0
        $txIdsByOrder[$order.OrderID] = @()
    }
    if ($logResult.ExitCode -eq 0 -and $orderRows.Count -gt 0) {
        $orderIdRegex = ($orderRows | ForEach-Object { [regex]::Escape($_.OrderID) }) -join '|'
        $relevant = @($logLines | Where-Object { "$_" -match $orderIdRegex })
        foreach ($order in $orderRows) {
            $needle = [regex]::Escape($order.OrderID)
            $orderLines = @($relevant | Where-Object { "$_" -match $needle })
            $appliedCounts[$order.OrderID] = @($orderLines | Where-Object { "$_" -match 'payment notify applied' }).Count
            $duplicateCounts[$order.OrderID] = @($orderLines | Where-Object { "$_" -match 'duplicate payment notify ignored' }).Count

            $mockLines = @($orderLines | Where-Object { "$_" -match 'mock pay triggered' })
            $mockCallCounts[$order.OrderID] = $mockLines.Count
            $txIds = @()
            foreach ($line in $mockLines) {
                $match = [regex]::Match("$line", '"transaction_id":"([^"]+)"')
                if ($match.Success) {
                    $txIds += $match.Groups[1].Value
                }
            }
            $txIdsByOrder[$order.OrderID] = @($txIds | Select-Object -Unique)
        }
    }
    else {
        Write-Warning '无法读取 API 日志，跳过「回调次数 / TransactionID」取证'
    }

    # -----------------------------------------------------------------------
    # 5. 输出
    # -----------------------------------------------------------------------
    Write-Section '回调结果'
    Write-Host ("订单数 / 每单回调数      : {0} / {1}" -f $Orders, $Duplicates)
    Write-Host ("请求计划 / 实际          : {0} / {1}" -f $plannedRequests, $actualRequests)
    Write-Host ("HTTP 200                 : {0}" -f $http200)
    Write-Host ("其它状态码               : {0}" -f $httpOther)
    Write-Host ("k6 运行时长              : {0} ms" -f $summary.duration_ms)
    if ($summary.notify_latency_ms) {
        $latency = $summary.notify_latency_ms
        Write-Host ("回调延迟(ms)             : avg={0:N2} p95={1:N2} p99={2:N2} max={3:N2}" -f `
                $latency.avg, $latency.'p(95)', $latency.'p(99)', $latency.max)
    }

    Write-Section '逐单状态'
    Write-Host ("{0,-14} {1,-6} {2,-20} {3,-14} {4,-8} {5,-6} {6,-6} {7,-8}" -f `
            'order(前12)', 'order', 'paid_at', 'updated=paid', 'enroll', 'applied', 'dup', 'txuniq')
    foreach ($order in $orderRows) {
        $shortId = $order.OrderID.Substring(0, [Math]::Min(12, $order.OrderID.Length))
        Write-Host ("{0,-14} {1,-6} {2,-20} {3,-14} {4,-8} {5,-6} {6,-6} {7,-8}" -f `
                $shortId, $order.OrderStatus, $order.OrderPaidAt,
                $(if ($order.UpdatedEqPaid) { 'yes' } else { 'NO' }),
                $order.EnrollStatus, $appliedCounts[$order.OrderID], $duplicateCounts[$order.OrderID],
                $txIdsByOrder[$order.OrderID].Count)
    }

    Write-Section '对账结果'
    Write-Host ("1) Order.status='paid' 数量        : {0} / {1}" -f @($orderRows | Where-Object { $_.OrderStatus -eq 'paid' }).Count, $Orders)
    Write-Host ("2) Enrollment.status='paid' 数量   : {0} / {1}" -f @($orderRows | Where-Object { $_.EnrollStatus -eq 'paid' }).Count, $Orders)
    Write-Host ("   paid_at 已设置 / updated_at=paid_at : {0} / {1}" -f `
        @($orderRows | Where-Object { $_.PaidAtSet }).Count,
        @($orderRows | Where-Object { $_.UpdatedEqPaid }).Count)
    if ($null -eq $remainingBefore) {
        Write-Host '3) Redis remaining                  : (回调前值缺失)'
    }
    else {
        Write-Host ("3) Redis remaining 回调前 / 回调后  : {0} / {1}" -f $remainingBefore, $remainingAfter)
    }

    # -----------------------------------------------------------------------
    # 6. 断言
    # -----------------------------------------------------------------------
    Write-Section '断言'

    $paidOrders = @($orderRows | Where-Object { $_.OrderStatus -eq 'paid' }).Count
    if ($orderRows.Count -eq $Orders -and $paidOrders -eq $Orders) {
        Write-Host ("[PASS] {0} 笔订单全部为 paid" -f $Orders)
    }
    else {
        Write-Host ("[FAIL] 订单数 {0}（期望 {1}），paid 数量 {2}" -f $orderRows.Count, $Orders, $paidOrders)
        $failedAssertions++
    }

    $paidEnrollments = @($orderRows | Where-Object { $_.EnrollStatus -eq 'paid' }).Count
    if ($paidEnrollments -eq $Orders) {
        Write-Host ("[PASS] {0} 笔 Enrollment 全部为 paid" -f $Orders)
    }
    else {
        Write-Host ("[FAIL] Enrollment paid 数量 {0}（期望 {1}）" -f $paidEnrollments, $Orders)
        $failedAssertions++
    }

    $paidAtProblems = @($orderRows | Where-Object { -not $_.PaidAtSet -or -not $_.UpdatedEqPaid }).Count
    if ($paidAtProblems -eq 0) {
        Write-Host '[PASS] paid_at 均已设置，且 updated_at = paid_at（只发生一次状态迁移）'
    }
    else {
        Write-Host ("[FAIL] {0} 笔订单 paid_at 缺失或 updated_at != paid_at（疑似重复迁移）" -f $paidAtProblems)
        $failedAssertions++
    }

    if ($null -eq $remainingBefore) {
        Write-Host '[FAIL] 缺少回调前 remaining，无法校验回调不改动名额'
        $failedAssertions++
    }
    elseif ($null -eq $remainingAfter) {
        Write-Host '[FAIL] Redis remaining key 缺失'
        $failedAssertions++
    }
    elseif ($remainingBefore -eq $remainingAfter) {
        Write-Host ("[PASS] Redis remaining 回调前后一致：{0}" -f $remainingAfter)
    }
    else {
        Write-Host ("[FAIL] Redis remaining 发生变化：{0} -> {1}（支付回调不应改写名额）" -f $remainingBefore, $remainingAfter)
        $failedAssertions++
    }

    if ($http200 -eq $plannedRequests -and $httpOther -eq 0) {
        Write-Host ("[PASS] {0} 次回调全部返回 200（重复回调同样 200）" -f $http200)
    }
    else {
        Write-Host ("[FAIL] 回调状态异常：200={0} / 其它={1}，期望 200={2}" -f $http200, $httpOther, $plannedRequests)
        $failedAssertions++
    }

    $logEvidence = ($appliedCounts.Values | Measure-Object -Sum).Sum + ($duplicateCounts.Values | Measure-Object -Sum).Sum
    if ($logEvidence -gt 0) {
        $badOrders = @($orderRows | Where-Object {
                $appliedCounts[$_.OrderID] -ne 1 -or $duplicateCounts[$_.OrderID] -ne ($Duplicates - 1)
            })
        if ($badOrders.Count -eq 0) {
            Write-Host ("[PASS] 日志取证：每单 applied=1、duplicate={0}" -f ($Duplicates - 1))
        }
        else {
            Write-Host ("[FAIL] {0} 笔订单的 applied/duplicate 日志次数异常" -f $badOrders.Count)
            $failedAssertions++
        }

        # 关键取证：mock 每次回调都会生成随机 TransactionID。
        # 只有「每单 10 个 TransactionID 互不相同」时，才能排除「事务号相同导致的假幂等」。
        $badMockCalls = @($orderRows | Where-Object { $mockCallCounts[$_.OrderID] -ne $Duplicates })
        if ($badMockCalls.Count -eq 0) {
            Write-Host ("[PASS] 每单收到 {0} 次 mock 回调请求（日志 verified）" -f $Duplicates)
        }
        else {
            Write-Host ("[FAIL] {0} 笔订单的 mock 回调请求次数不等于 {1}" -f $badMockCalls.Count, $Duplicates)
            $failedAssertions++
        }

        $badDistinct = @($orderRows | Where-Object { $txIdsByOrder[$_.OrderID].Count -ne $Duplicates })
        if ($badDistinct.Count -eq 0) {
            Write-Host ("[PASS] 每单的 {0} 个 TransactionID 互不相同（同一订单收到不同事务号）" -f $Duplicates)
        }
        else {
            Write-Host ("[FAIL] {0} 笔订单的 TransactionID 去重后不足 {1} 个" -f $badDistinct.Count, $Duplicates)
            $failedAssertions++
        }

        $badWinner = @($orderRows | Where-Object { $txIdsByOrder[$_.OrderID] -notcontains $_.TransactionID })
        if ($badWinner.Count -eq 0) {
            Write-Host '[PASS] 落库 transaction_id 属于该单回调集合（只写入一次，未被后续回调覆盖）'
        }
        else {
            Write-Host ("[FAIL] {0} 笔订单落库的 transaction_id 不在其回调事务号集合内" -f $badWinner.Count)
            $failedAssertions++
        }
    }
    else {
        Write-Host '[WARN] 未能从 API 日志取证 applied/duplicate/TransactionID（已跳过相关断言）'
    }

    # -----------------------------------------------------------------------
    # 7. 可选复位
    # -----------------------------------------------------------------------
    if ($ResetAfter) {
        Write-Section '复位压测环境'
        foreach ($table in @('disclaimer_signatures', 'orders', 'enrollments', 'enrollment_locks')) {
            $deleted = Get-MySQLScalar "DELETE FROM $table WHERE group_id = '$GroupId'; SELECT ROW_COUNT();"
            Write-Host ("已删除 {0,-22}: {1} 行" -f $table, $deleted)
        }
        $removedAuxKeys = Remove-RedisKeysByPattern -Pattern 'lock:*'
        Write-Host ("已清理 Redis 辅助 key（lock:*）：{0} 个" -f $removedAuxKeys)

        $deletedUsers = Get-MySQLScalar "DELETE FROM users WHERE openid LIKE 'wx-$runTag-%'; SELECT ROW_COUNT();"
        Write-Host ("已删除 {0,-22}: {1} 行" -f 'users（压测用户）', $deletedUsers)

        $token = New-AdminToken -Secret $jwtSecret
        $headers = @{ Authorization = "Bearer $token" }
        $resetUri = "$ResetBaseUrl/admin/groups/$GroupId/rebuild-remaining?force=true"
        $response = Invoke-RestMethod -Method Post -Uri $resetUri -Headers $headers -ContentType 'application/json'
        $previous = if ($response.PSObject.Properties.Name -contains 'previous_remaining') { $response.previous_remaining } else { 'n/a' }
        Write-Host ("名额重建：mode={0} remaining={1} previous={2}" -f $response.mode, $response.remaining_slots, $previous)

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
        Write-Host '提示：本次压测留下 10 笔 paid 订单与报名记录，Redis remaining 已减少 10。'
        Write-Host '      需要立刻复位请加 -ResetAfter。'
    }
}
finally {
    Pop-Location
}

Write-Section '结论'
if ($failedAssertions -eq 0) {
    Write-Host '全部断言通过：重复回调幂等，每单只迁移一次，Redis 名额未被改动。'
    exit 0
}

Write-Host ("有 {0} 项断言失败。" -f $failedAssertions)
exit 1
