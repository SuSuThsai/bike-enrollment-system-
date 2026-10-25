// k6 压测脚本：支付回调幂等性（同一订单并发 10 次重复回调）。
//
// 流程：
//   setup()  用 10 个不同 user_id 走真实报名链路（POST /enrollments/lock →
//            POST /enrollments），生成 10 笔 pending_payment 订单（lock 已消费），
//            再通过公开详情接口记录「回调前」的 Redis remaining；
//   default  100 个 VU 各发 1 次 POST /internal/mock-pay/{order_id}/success，
//            每个订单正好由 10 个不同 VU 并发触发（模拟微信重复回调）；
//   summary  按状态码汇总并写 tests/load/results/notify-idempotent-summary.json。
//
// 期望：100 次回调全部 200；每单只发生一次 pending_payment → paid 迁移，
//       其余 9 次命中「重复回调直接 COMMIT」分支；Redis remaining 不变。
//
// 用法（推荐用同目录的 run-notify-idempotent.ps1，它会创建订单并做对账断言）：
//   k6 run \
//     -e BASE_URL=http://127.0.0.1:8080/api/v1 \
//     -e GROUP_ID=88f8f9de-1d46-4356-9cfd-68e01dd68b10 \
//     -e EVENT_ID=3f38d71d-6535-4cfd-ab37-26d131f2bbfc \
//     -e JWT_SECRET=<与 API 相同的 JWT_SECRET> \
//     -e MOCK_PAY_SECRET=<与 API 相同的 MOCK_PAY_SECRET> \
//     -e DISCLAIMER_VERSION=v1.0 \
//     -e ORDERS=10 -e DUPLICATES=10 \
//     tests/load/notify-idempotent.js

import http from 'k6/http';
import crypto from 'k6/crypto';
import encoding from 'k6/encoding';
import { Counter, Gauge, Trend } from 'k6/metrics';
import { fail } from 'k6';

// ---------------------------------------------------------------------------
// 环境变量
// ---------------------------------------------------------------------------
const BASE_URL = (__ENV.BASE_URL || 'http://127.0.0.1:8080/api/v1').replace(/\/+$/, '');
// mock 支付触发入口不在 /api/v1 下，需要单独拼 origin
const API_ORIGIN = BASE_URL.replace(/\/api\/v1$/, '');
const GROUP_ID = (__ENV.GROUP_ID || '').trim();
const EVENT_ID = (__ENV.EVENT_ID || '').trim();
const JWT_SECRET = __ENV.JWT_SECRET || '';
const MOCK_PAY_SECRET = __ENV.MOCK_PAY_SECRET || '';
const DISCLAIMER_VERSION = __ENV.DISCLAIMER_VERSION || 'v1.0';
const ORDERS = Number(__ENV.ORDERS || 10);
const DUPLICATES = Number(__ENV.DUPLICATES || 10);
// 运行器会预置 users 表记录，必须使用同一个前缀（mock 回调会校验订单所属用户存在）
const RUN_TAG = (__ENV.RUN_TAG || `notify-${Date.now().toString(36)}`).trim();

if (!GROUP_ID) {
  fail('缺少环境变量 GROUP_ID');
}
if (!EVENT_ID) {
  fail('缺少环境变量 EVENT_ID（用于读取回调前的 Redis remaining）');
}
if (JWT_SECRET.length < 32) {
  fail('缺少有效 JWT_SECRET（至少 32 字节），脚本需要它本地签发压测 JWT');
}
if (!MOCK_PAY_SECRET) {
  fail('缺少 MOCK_PAY_SECRET，/internal/mock-pay/* 会返回 403');
}
if (!Number.isInteger(ORDERS) || ORDERS < 1) {
  fail(`ORDERS 非法：${__ENV.ORDERS}`);
}
if (!Number.isInteger(DUPLICATES) || DUPLICATES < 1) {
  fail(`DUPLICATES 非法：${__ENV.DUPLICATES}`);
}

// ---------------------------------------------------------------------------
// 指标
// ---------------------------------------------------------------------------
const KNOWN_STATUSES = [200, 400, 403, 404, 409, 422, 429, 500];
const statusCounters = {};
for (const code of KNOWN_STATUSES) {
  statusCounters[code] = new Counter(`notify_http_${code}`);
}
const otherStatusCounter = new Counter('notify_http_other');
const notifyLatency = new Trend('notify_latency_ms', true);
// setup 阶段读到的「回调前」Redis remaining；每个 VU 写入同一个值，Gauge 取最后一次。
const remainingBeforeGauge = new Gauge('redis_remaining_before');

// ---------------------------------------------------------------------------
// 场景：VU 数 = 订单数 × 每单重复次数，每 VU 只发 1 次回调
// ---------------------------------------------------------------------------
export const options = {
  scenarios: {
    duplicate_callbacks: {
      executor: 'per-vu-iterations',
      vus: ORDERS * DUPLICATES,
      iterations: 1,
      maxDuration: '60s',
      gracefulStop: '0s',
    },
  },
  thresholds: {
    // 全部回调都应返回 200（重复回调同样 200，不返回 409）
    notify_http_200: [`count==${ORDERS * DUPLICATES}`],
  },
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
};

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

function authHeaders(token) {
  return {
    'Content-Type': 'application/json',
    Authorization: `Bearer ${token}`,
  };
}

// ---------------------------------------------------------------------------
// setup：创建 10 笔 pending_payment 订单（每笔对应一个已消费的 lock）
// ---------------------------------------------------------------------------
export function setup() {
  const orders = [];
  const signedAt = new Date().toISOString();

  for (let i = 0; i < ORDERS; i += 1) {
    // 每次运行带独立前缀，避免与历史数据的 (user_id, group_id) 唯一键冲突
    const userID = `${RUN_TAG}-${i}`.slice(0, 36);
    const token = makeToken(userID);

    const lockRes = http.post(
      `${BASE_URL}/enrollments/lock`,
      JSON.stringify({ group_id: GROUP_ID }),
      { headers: authHeaders(token), tags: { name: 'setup POST /enrollments/lock' } },
    );
    if (lockRes.status !== 200) {
      fail(`第 ${i + 1} 笔订单锁定名额失败：HTTP ${lockRes.status} ${lockRes.body}`);
    }
    const lockToken = lockRes.json('lock_token');
    if (!lockToken) {
      fail(`第 ${i + 1} 笔订单未返回 lock_token：${lockRes.body}`);
    }

    const submitRes = http.post(
      `${BASE_URL}/enrollments`,
      JSON.stringify({
        lock_token: lockToken,
        group_id: GROUP_ID,
        form_data: {},
        disclaimer_version: DISCLAIMER_VERSION,
        disclaimer_signed_at: signedAt,
      }),
      { headers: authHeaders(token), tags: { name: 'setup POST /enrollments' } },
    );
    if (submitRes.status !== 201) {
      fail(`第 ${i + 1} 笔订单提交报名失败：HTTP ${submitRes.status} ${submitRes.body}`);
    }
    const order = submitRes.json('order');
    const enrollment = submitRes.json('enrollment');
    if (!order || !order.id || !enrollment || !enrollment.id) {
      fail(`第 ${i + 1} 笔订单响应缺少 order/enrollment：${submitRes.body}`);
    }
    if (order.status !== 'pending_payment') {
      fail(`第 ${i + 1} 笔订单状态应为 pending_payment，实际 ${order.status}`);
    }

    orders.push({
      orderId: order.id,
      enrollmentId: enrollment.id,
      userID,
    });
  }

  // 回调前 Redis remaining：公开详情接口的 remaining_slots 直接读 Redis
  const detail = http.get(`${BASE_URL}/events/${EVENT_ID}`, {
    tags: { name: 'setup GET /events/:id' },
  });
  if (detail.status !== 200) {
    fail(`读取赛事详情失败（HTTP ${detail.status}）：${detail.body}`);
  }
  const groups = detail.json('groups') || [];
  const target = groups.filter((group) => group.id === GROUP_ID)[0];
  if (!target) {
    fail(`赛事 ${EVENT_ID} 下未找到组别 ${GROUP_ID}`);
  }

  return {
    orders,
    remainingBefore: target.remaining_slots,
    runTag: RUN_TAG,
  };
}

// ---------------------------------------------------------------------------
// 主流程：每个 VU 对「自己负责的订单」发 1 次重复回调
// ---------------------------------------------------------------------------
export default function (data) {
  const orderCount = data.orders.length;
  const orderIndex = (__VU - 1) % orderCount;
  const order = data.orders[orderIndex];

  // 所有 VU 写入同一个值（回调前的 Redis remaining）
  remainingBeforeGauge.add(data.remainingBefore);

  const params = {
    headers: { 'X-Mock-Secret': MOCK_PAY_SECRET },
    tags: { name: 'POST /internal/mock-pay/:order_id/success' },
  };
  if (typeof http.expectedStatuses === 'function') {
    params.responseCallback = http.expectedStatuses(200);
  }

  const res = http.post(`${API_ORIGIN}/internal/mock-pay/${order.orderId}/success`, null, params);

  const counter = statusCounters[res.status] || otherStatusCounter;
  counter.add(1);
  notifyLatency.add(res.timings.duration);
}

// ---------------------------------------------------------------------------
// 汇总输出
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
    statusCounts[code] = metricCount(data, `notify_http_${code}`);
  }
  statusCounts.other = metricCount(data, 'notify_http_other');

  const gauge = data.metrics.redis_remaining_before;
  const remainingBefore =
    gauge && gauge.values && typeof gauge.values.value === 'number' ? gauge.values.value : null;

  const summary = {
    scenario: 'notify-idempotent',
    generated_at: new Date().toISOString(),
    base_url: BASE_URL,
    group_id: GROUP_ID,
    event_id: EVENT_ID,
    run_tag: RUN_TAG,
    orders: ORDERS,
    duplicates_per_order: DUPLICATES,
    planned_requests: ORDERS * DUPLICATES,
    http_200: statusCounts[200],
    http_other: statusCounts.other,
    status_counts: statusCounts,
    http_requests: metricCount(data, 'http_reqs'),
    duration_ms: Math.round(data.state ? data.state.testRunDurationMs : 0),
    redis_remaining_before: remainingBefore,
    notify_latency_ms: data.metrics.notify_latency_ms ? data.metrics.notify_latency_ms.values : null,
  };

  const headline =
    `\n[notify-idempotent] 订单=${ORDERS} 每单回调=${DUPLICATES} 计划=${summary.planned_requests} ` +
    `实际=${summary.http_requests} 200=${summary.http_200} other=${summary.http_other} ` +
    `remaining_before=${remainingBefore} 耗时=${summary.duration_ms}ms\n`;

  return {
    'tests/load/results/notify-idempotent-summary.json': JSON.stringify(summary, null, 2),
    stdout: headline,
  };
}
