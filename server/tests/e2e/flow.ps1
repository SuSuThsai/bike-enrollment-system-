# Milestone 3 Step 5 - end-to-end flow driver.
#
# Calls the running API at http://localhost:8080/api/v1:
#   login (wx.login is unavailable in this environment, so a dev JWT is minted)
#   -> browse -> lock -> submit enrollment -> prepay
#
# The script never prints JWT_SECRET or the rider JWT.
# It creates one event and one group (10 slots, price = 1 cent), seeds one rider
# in MySQL, then prints the API responses plus the resulting database/Redis state.
#
# Usage:
#   powershell -ExecutionPolicy Bypass -File server/tests/e2e/flow.ps1

param(
    [string]$BaseUrl = 'http://localhost:8080/api/v1',
    [string]$EnvFile = 'D:\ridingCode+wechat+webside\.env',
    [string]$ComposeDir = 'D:\ridingCode+wechat+webside'
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

function Convert-ToBase64Url([byte[]]$bytes) {
    [Convert]::ToBase64String($bytes).TrimEnd('=').Replace('+', '-').Replace('/', '_')
}

function New-DevJwt([string]$secret, [string]$subject, [string]$role) {
    $header = '{"alg":"HS256","typ":"JWT"}'
    $now = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
    $payload = (@{
        sub = $subject
        role = $role
        iss = 'bike-enroll-api'
        iat = $now
        exp = $now + 3600
    } | ConvertTo-Json -Compress)
    $h = Convert-ToBase64Url([Text.Encoding]::UTF8.GetBytes($header))
    $p = Convert-ToBase64Url([Text.Encoding]::UTF8.GetBytes($payload))
    $hmac = [System.Security.Cryptography.HMACSHA256]::new([Text.Encoding]::UTF8.GetBytes($secret))
    $sig = Convert-ToBase64Url($hmac.ComputeHash([Text.Encoding]::UTF8.GetBytes("$h.$p")))
    return "$h.$p.$sig"
}

function Invoke-Http {
    param(
        [Parameter(Mandatory = $true)][string]$Method,
        [Parameter(Mandatory = $true)][string]$Path,
        [string]$Token,
        [string]$JsonBody
    )

    $tmpBody = [IO.Path]::GetTempFileName()
    $tmpOut = [IO.Path]::GetTempFileName()
    try {
        $curlArgs = @('-sS', '-o', $tmpOut, '-w', '%{http_code}', '-X', $Method, "$BaseUrl$Path")
        if ($Token) {
            $curlArgs += @('-H', "Authorization: Bearer $Token")
        }
        if ($JsonBody) {
            [IO.File]::WriteAllText($tmpBody, $JsonBody, (New-Object Text.UTF8Encoding($false)))
            $curlArgs += @('-H', 'Content-Type: application/json; charset=utf-8', '--data-binary', "@$tmpBody")
        }
        $status = & curl.exe @curlArgs
        $body = if (Test-Path $tmpOut) { Get-Content -Raw -Encoding UTF8 $tmpOut } else { '' }
        return [pscustomobject]@{ Status = [int]$status; Body = $body }
    }
    finally {
        Remove-Item $tmpBody, $tmpOut -Force -ErrorAction SilentlyContinue
    }
}

function Invoke-MySQL {
    param([Parameter(Mandatory = $true)][string]$Sql)
    $Sql | & docker compose --project-directory $ComposeDir exec -T mysql sh -c 'export MYSQL_PWD="$MYSQL_PASSWORD"; mysql -u"$MYSQL_USER" "$MYSQL_DATABASE" -t'
}

function Get-RedisValue {
    param([Parameter(Mandatory = $true)][string]$Key)
    return ((& docker compose --project-directory $ComposeDir exec -T redis redis-cli GET $Key) -join '').Trim()
}

function Write-Section([string]$Title) {
    Write-Output ''
    Write-Output "=== $Title ==="
}

# ---------- credentials ----------
$secretLine = Get-Content $EnvFile | Where-Object { $_ -match '^JWT_SECRET=' } | Select-Object -First 1
if (-not $secretLine) { throw "JWT_SECRET not found in $EnvFile" }
$jwtSecret = ($secretLine -replace '^JWT_SECRET=', '').Trim()
if ($jwtSecret.Length -lt 32) { throw 'JWT_SECRET must be at least 32 bytes' }

$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$startTime = (Get-Date).ToUniversalTime().AddDays(30).ToString('yyyy-MM-ddTHH:mm:ssZ')

# ---------- 0. login ----------
Write-Section '0. login'
$login = Invoke-Http -Method POST -Path '/auth/wechat-login' -JsonBody '{"code":"e2e-dummy-code"}'
Write-Output "wechat_login_http=$($login.Status)"
Write-Output "wechat_login_body=$($login.Body)"

$riderID = [guid]::NewGuid().ToString()
$riderOpenID = "e2e-openid-$stamp"
$riderSQL = @"
INSERT INTO users (id, openid, unionid, nickname, avatar_url, role, created_at, updated_at)
VALUES ('$riderID', '$riderOpenID', '', 'E2E Rider', '', 'rider', NOW(3), NOW(3));
"@
Invoke-MySQL $riderSQL | Out-Null
$riderToken = New-DevJwt $jwtSecret $riderID 'rider'
Write-Output "login_fallback=dev-jwt rider_id=$riderID openid=$riderOpenID"
Write-Output 'note=wx.login cannot run because WECHAT_APPID/WECHAT_SECRET are empty; JWT is minted with the same JWT_SECRET.'

# ---------- 1. admin creates event and group ----------
Write-Section '1. admin creates event and group'
$adminToken = New-DevJwt $jwtSecret ([guid]::NewGuid().ToString()) 'admin'
$startDate = [datetime]::Parse($startTime).ToUniversalTime()
$eventBody = (@{
    title = "M3S5 E2E Event $stamp"
    cover_url = 'https://example.com/e2e-cover.png'
    registration_open_at = (Get-Date).ToUniversalTime().AddHours(-1).ToString('yyyy-MM-ddTHH:mm:ssZ')
    registration_deadline = $startDate.AddDays(-1).ToString('yyyy-MM-ddTHH:mm:ssZ')
    start_time = $startDate.ToString('yyyy-MM-ddTHH:mm:ssZ')
    end_time = $startDate.AddHours(4).ToString('yyyy-MM-ddTHH:mm:ssZ')
    location = 'Hangzhou Qiandao Lake'
    description = 'Milestone 3 Step 5 end-to-end integration event'
    rules = 'E2E test rules'
} | ConvertTo-Json -Compress)
$eventResp = Invoke-Http -Method POST -Path '/admin/events' -Token $adminToken -JsonBody $eventBody
Write-Output "create_event_http=$($eventResp.Status)"
Write-Output "create_event_body=$($eventResp.Body)"
if ($eventResp.Status -ne 201) { throw "create event failed: HTTP $($eventResp.Status)" }
$event = $eventResp.Body | ConvertFrom-Json

$groupBody = (@{
    event_id = $event.id
    name = 'E2E Group A'
    price = 1
    total_slots = 10
    start_time = $startTime
    form_schema = @{
        fields = @(
            @{ name = 'real_name'; label = 'Real Name'; type = 'text'; required = $true },
            @{ name = 'id_card'; label = 'ID Card'; type = 'text'; required = $true },
            @{ name = 'phone'; label = 'Phone'; type = 'text'; required = $true }
        )
    }
    disclaimer_version = 'v1'
} | ConvertTo-Json -Depth 10 -Compress)
$groupResp = Invoke-Http -Method POST -Path '/admin/groups' -Token $adminToken -JsonBody $groupBody
Write-Output "create_group_http=$($groupResp.Status)"
Write-Output "create_group_body=$($groupResp.Body)"
if ($groupResp.Status -ne 201) { throw "create group failed: HTTP $($groupResp.Status)" }
$group = $groupResp.Body | ConvertFrom-Json

# ---------- 2. browse ----------
Write-Section '2. browse'
$listResp = Invoke-Http -Method GET -Path '/events?registration_status=open&page=1&page_size=20'
Write-Output "list_events_http=$($listResp.Status)"
Write-Output "list_events_body=$($listResp.Body)"
$detailResp = Invoke-Http -Method GET -Path "/events/$($event.id)"
Write-Output "event_detail_http=$($detailResp.Status)"
Write-Output "event_detail_body=$($detailResp.Body)"
if ($detailResp.Status -ne 200) { throw "event detail failed: HTTP $($detailResp.Status)" }

# ---------- 3. lock ----------
Write-Section '3. lock'
$lockBody = (@{ group_id = $group.id } | ConvertTo-Json -Compress)
$lockResp = Invoke-Http -Method POST -Path '/enrollments/lock' -Token $riderToken -JsonBody $lockBody
Write-Output "lock_http=$($lockResp.Status)"
Write-Output "lock_body=$($lockResp.Body)"
if ($lockResp.Status -ne 200) { throw "lock failed: HTTP $($lockResp.Status)" }
$lock = $lockResp.Body | ConvertFrom-Json

# ---------- 4. submit enrollment ----------
Write-Section '4. submit enrollment'
$signedAt = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
$submitBody = (@{
    lock_token = $lock.lock_token
    group_id = $group.id
    form_data = @{
        real_name = 'E2E Rider'
        id_card = '330101199001011234'
        phone = '13800138000'
    }
    disclaimer_version = 'v1'
    disclaimer_signed_at = $signedAt
} | ConvertTo-Json -Depth 8 -Compress)
$submitResp = Invoke-Http -Method POST -Path '/enrollments' -Token $riderToken -JsonBody $submitBody
Write-Output "submit_http=$($submitResp.Status)"
Write-Output "submit_body=$($submitResp.Body)"
if ($submitResp.Status -ne 201) { throw "submit failed: HTTP $($submitResp.Status)" }
$submit = $submitResp.Body | ConvertFrom-Json
$orderID = $submit.order.id
$enrollmentID = $submit.enrollment.id

# ---------- 5. prepay ----------
Write-Section '5. prepay'
$prepayBody = (@{ order_id = $orderID } | ConvertTo-Json -Compress)
$prepayResp = Invoke-Http -Method POST -Path '/payments/prepay' -Token $riderToken -JsonBody $prepayBody
Write-Output "prepay_http=$($prepayResp.Status)"
Write-Output "prepay_body=$($prepayResp.Body)"

# ---------- 6. state after the flow ----------
Write-Section '6. database and redis state'
Invoke-MySQL @"
SELECT id, user_id, group_id, order_id, status, created_at FROM enrollments WHERE id = '$enrollmentID';
SELECT id, enrollment_id, amount, status, paid_at, transaction_id, created_at FROM orders WHERE id = '$orderID';
SELECT token, user_id, group_id, status, expires_at, consumed_at FROM enrollment_locks WHERE token = '$($lock.lock_token)';
SELECT id, openid, role FROM users WHERE id = '$riderID';
"@
Write-Output "redis_group_remaining=$(Get-RedisValue "group:$($group.id):remaining")"

$statsResp = Invoke-Http -Method GET -Path "/admin/stats/overview?event_id=$($event.id)" -Token $adminToken
Write-Output "admin_stats_http=$($statsResp.Status)"
Write-Output "admin_stats_body=$($statsResp.Body)"

Write-Section 'summary'
$summary = [ordered]@{
    event_id = $event.id
    group_id = $group.id
    group_price = $group.price
    group_total_slots = $group.total_slots
    rider_id = $riderID
    lock_token = $lock.lock_token
    lock_expires_at = $lock.expires_at
    remaining_after_lock = $lock.remaining_slots
    enrollment_id = $enrollmentID
    order_id = $orderID
    prepay_http = $prepayResp.Status
}
$summary | ConvertTo-Json -Depth 5
