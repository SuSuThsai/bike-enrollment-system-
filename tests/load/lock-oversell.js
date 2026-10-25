// k6 压测脚本：并发抢名额，验证 Redis Lua 原子扣减不发生超卖。
//
// 场景：VUS 个虚拟用户，每个用户在 1 秒内连续发起 ITERATIONS 次
//       POST /api/v1/enrollments/lock，每次使用全新 user_id（压测 JWT 预生成）。
// 期望：成功（HTTP 200）次数 <= total_slots，其余为 409 GROUP_SOLD_OUT。
//
// 用法（推荐用同目录的 run-lock-oversell.ps1，它会顺带做对账断言）：
//   k6 run \
//     -e BASE_URL=http://127.0.0.1:8080/api/v1 \
//     -e GROUP_ID=88f8f9de-1d46-4356-9cfd-68e01dd68b10 \
//     -e JWT_SECRET=<与 API 相同的 JWT_SECRET> \
//     -e VUS=200 -e ITERATIONS=10 -e MAX_SUCCESS=100 \
//     tests/load/lock-oversell.js
//
// 说明：
//   - JWT 由脚本用 JWT_SECRET 本地签发（HS256），不依赖登录接口；
//     user_id 取 `load-{VU}-{ITER}`，长度远小于 DB 的 char(36)，且每次请求唯一，
//     因此不会触发同一用户的 lock:{user_id}:{group_id} 串行化（RATE_LIMITED）。
//   - 409 属于预期结果，通过 http.expectedStatuses 声明，不计入 http_req_failed。
//   - 汇总写入 tests/load/results/lock-oversell-summary.json，供对账脚本读取。

import http from 'k6/http';
import crypto from 'k6/crypto';
import encoding from 'k6/encoding';
import { Counter, Trend } from 'k6/metrics';
import { fail } from 'k6';

// ---------------------------------------------------------------------------
// 环境变量与校验（init 阶段失败会直接中止压测）
// ---------------------------------------------------------------------------
const BASE_URL = (__ENV.BASE_URL || 'http://127.0.0.1:8080/api/v1').replace(/\/+$/, '');
const GROUP_ID = (__ENV.GROUP_ID || '').trim();
const JWT_SECRET = __ENV.JWT_SECRET || '';
const VUS = Number(__ENV.VUS || 200);
const ITERATIONS = Number(__ENV.ITERATIONS || 10);
const MAX_SUCCESS = Number(__ENV.MAX_SUCCESS || 100);

if (!GROUP_ID) {
  fail('缺少环境变量 GROUP_ID（压测组别 ID）');
}
if (JWT_SECRET.length < 32) {
  fail('缺少有效 JWT_SECRET（至少 32 字节），脚本需要它本地签发压测 JWT');
}
if (!Number.isInteger(VUS) || VUS < 1) {
  fail(`VUS 非法：${__ENV.VUS}`);
}
if (!Number.isInteger(ITERATIONS) || ITERATIONS < 1) {
  fail(`ITERATIONS 非法：${__ENV.ITERATIONS}`);
}

// ---------------------------------------------------------------------------
// 指标：按状态码分别计数，便于汇总与断言
// ---------------------------------------------------------------------------
const KNOWN_STATUSES = [200, 401, 403, 409, 422, 429, 500];
const statusCounters = {};
for (const code of KNOWN_STATUSES) {
  statusCounters[code] = new Counter(`lock_http_${code}`);
}
const otherStatusCounter = new Counter('lock_http_other');
const lockLatency = new Trend('lock_latency_ms', true);

// ---------------------------------------------------------------------------
// k6 选项：200 VU × 10 次，无 think time，形成 1 秒内的 2000 次突发
// ---------------------------------------------------------------------------
export const options = {
  scenarios: {
    lock_burst: {
      executor: 'per-vu-iterations',
      vus: VUS,
      iterations: ITERATIONS,
      maxDuration: '30s',
      gracefulStop: '0s',
    },
  },
  thresholds: {
    // 成功数不得超过名额上限；超卖会直接判压测失败
    lock_http_200: [`count<=${MAX_SUCCESS}`],
  },
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
};

const lockURL = `${BASE_URL}/enrollments/lock`;
// 兼容旧版 k6：没有 expectedStatuses 时不声明预期状态码
const responseCallback =
  typeof http.expectedStatuses === 'function' ? http.expectedStatuses(200, 409) : undefined;

// ---------------------------------------------------------------------------
// JWT 生成（HS256，与 server/internal/pkg/jwt 的 Claims 对齐）
// ---------------------------------------------------------------------------
function toBase64Url(input) {
  const base64 = encoding.b64encode(input);
  return base64.replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

function makeToken(userID) {
  const now = Math.floor(Date.now() / 1000);
  const header = toBase64Url(JSON.stringify({ alg: 'HS256', typ: 'JWT' }));
  const payload = toBase64Url(
    JSON.stringify({
      role: 'rider',
      sub: userID,
      iss: 'bike-enroll-api',
      iat: now,
      exp: now + 3600,
    }),
  );
  const signature = crypto
    .hmac('sha256', JWT_SECRET, `${header}.${payload}`, 'base64')
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/, '');
  return `${header}.${payload}.${signature}`;
}

// ---------------------------------------------------------------------------
// 主流程
// ---------------------------------------------------------------------------
export default function () {
  // __VU + __ITER 保证 2000 次请求的 user_id 互不相同
  const userID = `load-${__VU}-${__ITER}`;
  const token = makeToken(userID);

  const params = {
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${token}`,
    },
    tags: { name: 'POST /enrollments/lock' },
  };
  if (responseCallback) {
    params.responseCallback = responseCallback;
  }

  const res = http.post(lockURL, JSON.stringify({ group_id: GROUP_ID }), params);

  const counter = statusCounters[res.status] || otherStatusCounter;
  counter.add(1);
  lockLatency.add(res.timings.duration);
}

// ---------------------------------------------------------------------------
// 汇总输出：落盘 JSON，供 run-lock-oversell.ps1 做对账
// ---------------------------------------------------------------------------
function metricCount(data, name) {
  const metric = data.metrics[name];
  if (!metric || !metric.values || typeof metric.values.count !== 'number') {
    return 0;
  }
  return metric.values.count;
}

export function handleSummary(data) {
  const statusCounts = {};
  for (const code of KNOWN_STATUSES) {
    statusCounts[code] = metricCount(data, `lock_http_${code}`);
  }
  statusCounts.other = metricCount(data, 'lock_http_other');

  const summary = {
    scenario: 'lock-oversell',
    generated_at: new Date().toISOString(),
    base_url: BASE_URL,
    group_id: GROUP_ID,
    vus: VUS,
    iterations_per_vu: ITERATIONS,
    planned_requests: VUS * ITERATIONS,
    http_200: statusCounts[200],
    http_409: statusCounts[409],
    http_other: statusCounts.other,
    status_counts: statusCounts,
    http_requests: metricCount(data, 'http_reqs'),
    duration_ms: Math.round(data.state ? data.state.testRunDurationMs : 0),
    lock_latency_ms: data.metrics.lock_latency_ms ? data.metrics.lock_latency_ms.values : null,
  };

  const headline =
    `\n[lock-oversell] 计划=${summary.planned_requests} 实际=${summary.http_requests} ` +
    `200=${summary.http_200} 409=${summary.http_409} other=${summary.http_other} ` +
    `耗时=${summary.duration_ms}ms\n`;

  return {
    'tests/load/results/lock-oversell-summary.json': JSON.stringify(summary, null, 2),
    stdout: headline,
  };
}
