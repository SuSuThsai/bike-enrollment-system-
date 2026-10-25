// k6 混合场景压测：持续 3 分钟，三类 VU 按 60% / 20% / 20% 混合。
//
//   60% VU：锁定 → 提交报名 → Mock 回调支付成功（完整链路）
//   20% VU：锁定 → 等待 16 秒（LOCK_TTL 已临时压缩为 10 秒）→ 不提交，触发超时释放
//   20% VU：对同一订单并发重复回调 5 次（验证幂等）
//
// 前置（由 run-mixed.ps1 完成）：
//   - API 以 LOCK_TTL_SECONDS=10 重启（默认 15 分钟）；
//   - 预置用户池：mix-<RUN_TAG>-c-<n>（链路人）与 mix-<RUN_TAG>-s-<n>（setup 订单人）；
//   - 传入专用组别（total_slots 足够大，避免压测中途售罄）。
//
// 运行：
//   k6 run -e BASE_URL=... -e GROUP_ID=... -e RUN_TAG=... -e JWT_SECRET=... \
//          -e MOCK_PAY_SECRET=... -e DISCLAIMER_VERSION=v1.0 tests/load/mixed.js

import http from 'k6/http';
import crypto from 'k6/crypto';
import encoding from 'k6/encoding';
import exec from 'k6/execution';
import { Counter, Trend } from 'k6/metrics';
import { fail, sleep } from 'k6';

const BASE_URL = (__ENV.BASE_URL || 'http://127.0.0.1:8080/api/v1').replace(/\/+$/, '');
const API_ORIGIN = BASE_URL.replace(/\/api\/v1$/, '');
const GROUP_ID = (__ENV.GROUP_ID || '').trim();
const RUN_TAG = (__ENV.RUN_TAG || '').trim();
const JWT_SECRET = __ENV.JWT_SECRET || '';
const MOCK_PAY_SECRET = __ENV.MOCK_PAY_SECRET || '';
const DISCLAIMER_VERSION = __ENV.DISCLAIMER_VERSION || 'v1.0';

const DURATION = __ENV.DURATION || '3m';
const CHAIN_VUS = Number(__ENV.CHAIN_VUS || 30);
const ABANDON_VUS = Number(__ENV.ABANDON_VUS || 10);
const DUP_VUS = Number(__ENV.DUP_VUS || 10);
const CHAIN_USER_POOL = Number(__ENV.CHAIN_USER_POOL || 12000);
const SETUP_ORDERS = Number(__ENV.SETUP_ORDERS || 20);
const ABANDON_WAIT_SECONDS = Number(__ENV.ABANDON_WAIT_SECONDS || 16);
const CHAIN_SLEEP_SECONDS = Number(__ENV.CHAIN_SLEEP_SECONDS || 0.5);
const DUP_SLEEP_SECONDS = Number(__ENV.DUP_SLEEP_SECONDS || 0.2);
const DUP_CALLBACKS = Number(__ENV.DUP_CALLBACKS || 5);

if (!GROUP_ID) {
  fail('缺少 GROUP_ID');
}
if (!RUN_TAG) {
  fail('缺少 RUN_TAG（用户池前缀，需与运行器预置的用户一致）');
}
if (JWT_SECRET.length < 32) {
  fail('缺少有效 JWT_SECRET（至少 32 字节）');
}
if (!MOCK_PAY_SECRET) {
  fail('缺少 MOCK_PAY_SECRET，/internal/mock-pay/* 会返回 403');
}

// ---------------------------------------------------------------------------
// 指标
// ---------------------------------------------------------------------------
const lockOK = new Counter('chain_lock_200');
const lockFail = new Counter('chain_lock_other');
const submitOK = new Counter('chain_submit_201');
const submitFail = new Counter('chain_submit_other');
const payOK = new Counter('chain_notify_200');
const payFail = new Counter('chain_notify_other');
const chainComplete = new Counter('chain_complete');
const abandonLockOK = new Counter('abandon_lock_200');
const abandonLockFail = new Counter('abandon_lock_other');
const dupNotifyOK = new Counter('dup_notify_200');
const dupNotifyFail = new Counter('dup_notify_other');
const chainLatency = new Trend('chain_iteration_ms', true);

// ---------------------------------------------------------------------------
// 场景：60% / 20% / 20% 的 VU 分配，同时起跑，持续 3 分钟
// ---------------------------------------------------------------------------
export const options = {
  scenarios: {
    chain: {
      executor: 'constant-vus',
      exec: 'chainFlow',
      vus: CHAIN_VUS,
      duration: DURATION,
      startTime: '0s',
    },
    abandon: {
      executor: 'constant-vus',
      exec: 'abandonFlow',
      vus: ABANDON_VUS,
      duration: DURATION,
      startTime: '0s',
    },
    duplicate: {
      executor: 'constant-vus',
      exec: 'duplicateFlow',
      vus: DUP_VUS,
      duration: DURATION,
      startTime: '0s',
    },
  },
  thresholds: {
    chain_complete: ['count>0'],
    dup_notify_200: ['count>0'],
  },
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
};

// ---------------------------------------------------------------------------
// JWT / 工具
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
  return { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` };
}

function lockSlot(token) {
  return http.post(
    `${BASE_URL}/enrollments/lock`,
    JSON.stringify({ group_id: GROUP_ID }),
    { headers: authHeaders(token), tags: { name: 'POST /enrollments/lock' } },
  );
}

// ---------------------------------------------------------------------------
// setup：用预置用户创建 SETUP_ORDERS 笔 pending_payment 订单，供「重复回调」场景复用
// ---------------------------------------------------------------------------
export function setup() {
  const orders = [];
  const signedAt = new Date().toISOString();

  for (let i = 0; i < SETUP_ORDERS; i += 1) {
    const userID = `mix-${RUN_TAG}-s-${i}`;
    const token = makeToken(userID);

    const lockRes = lockSlot(token);
    if (lockRes.status !== 200) {
      fail(`setup 第 ${i + 1} 笔订单锁定失败：HTTP ${lockRes.status} ${lockRes.body}`);
    }
    const lockToken = lockRes.json('lock_token');
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
      fail(`setup 第 ${i + 1} 笔订单提交失败：HTTP ${submitRes.status} ${submitRes.body}`);
    }
    orders.push({ orderId: submitRes.json('order').id });
  }

  return { orders };
}

// ---------------------------------------------------------------------------
// 60%：锁定 → 提交 → Mock 回调支付成功
// ---------------------------------------------------------------------------
export function chainFlow() {
  const startedAt = Date.now();
  const index = exec.scenario.iterationInTest;
  if (index >= CHAIN_USER_POOL) {
    fail(`chain 用户池耗尽：iteration=${index} >= ${CHAIN_USER_POOL}，请调大 CHAIN_USER_POOL`);
  }

  const userID = `mix-${RUN_TAG}-c-${index}`;
  const token = makeToken(userID);

  const lockRes = lockSlot(token);
  if (lockRes.status !== 200) {
    lockFail.add(1);
    sleep(CHAIN_SLEEP_SECONDS);
    return;
  }
  lockOK.add(1);

  const signedAt = new Date().toISOString();
  const submitRes = http.post(
    `${BASE_URL}/enrollments`,
    JSON.stringify({
      lock_token: lockRes.json('lock_token'),
      group_id: GROUP_ID,
      form_data: { source: 'mixed-chain' },
      disclaimer_version: DISCLAIMER_VERSION,
      disclaimer_signed_at: signedAt,
    }),
    { headers: authHeaders(token), tags: { name: 'POST /enrollments' } },
  );
  if (submitRes.status !== 201) {
    submitFail.add(1);
    sleep(CHAIN_SLEEP_SECONDS);
    return;
  }
  submitOK.add(1);

  const orderID = submitRes.json('order').id;
  const payRes = http.post(
    `${API_ORIGIN}/internal/mock-pay/${orderID}/success`,
    null,
    {
      headers: { 'X-Mock-Secret': MOCK_PAY_SECRET },
      tags: { name: 'POST /internal/mock-pay/:order_id/success' },
    },
  );
  if (payRes.status === 200) {
    payOK.add(1);
    chainComplete.add(1);
  } else {
    payFail.add(1);
  }

  chainLatency.add(Date.now() - startedAt);
  sleep(CHAIN_SLEEP_SECONDS);
}

// ---------------------------------------------------------------------------
// 20%：锁定 → 等待 16 秒（LOCK_TTL=10s）→ 不提交，触发超时释放
// ---------------------------------------------------------------------------
export function abandonFlow() {
  const userID = `mix-${RUN_TAG}-a-${__VU}-${__ITER}`;
  const token = makeToken(userID);

  const lockRes = lockSlot(token);
  if (lockRes.status === 200) {
    abandonLockOK.add(1);
  } else {
    abandonLockFail.add(1);
  }

  // 超过压缩后的 LOCK_TTL（10s），让 lock 进入可被过期任务回收的状态
  sleep(ABANDON_WAIT_SECONDS);
}

// ---------------------------------------------------------------------------
// 20%：对同一订单并发重复回调 5 次
// ---------------------------------------------------------------------------
export function duplicateFlow(data) {
  const index = exec.scenario.iterationInTest;
  const order = data.orders[index % data.orders.length];
  const url = `${API_ORIGIN}/internal/mock-pay/${order.orderId}/success`;

  const requests = [];
  for (let i = 0; i < DUP_CALLBACKS; i += 1) {
    requests.push([
      'POST',
      url,
      null,
      {
        headers: { 'X-Mock-Secret': MOCK_PAY_SECRET },
        tags: { name: 'POST /internal/mock-pay/:order_id/success (dup)' },
      },
    ]);
  }

  const responses = http.batch(requests);
  for (const response of responses) {
    if (response.status === 200) {
      dupNotifyOK.add(1);
    } else {
      dupNotifyFail.add(1);
    }
  }

  sleep(DUP_SLEEP_SECONDS);
}

// ---------------------------------------------------------------------------
// 汇总
// ---------------------------------------------------------------------------
function metricCount(data, name) {
  const metric = data.metrics[name];
  if (!metric || !metric.values || typeof metric.values.count !== 'number') {
    return 0;
  }
  return metric.values.count;
}

export function handleSummary(data) {
  const summary = {
    scenario: 'mixed',
    generated_at: new Date().toISOString(),
    base_url: BASE_URL,
    group_id: GROUP_ID,
    run_tag: RUN_TAG,
    duration: DURATION,
    vus: { chain: CHAIN_VUS, abandon: ABANDON_VUS, duplicate: DUP_VUS },
    chain: {
      lock_200: metricCount(data, 'chain_lock_200'),
      lock_other: metricCount(data, 'chain_lock_other'),
      submit_201: metricCount(data, 'chain_submit_201'),
      submit_other: metricCount(data, 'chain_submit_other'),
      notify_200: metricCount(data, 'chain_notify_200'),
      notify_other: metricCount(data, 'chain_notify_other'),
      complete: metricCount(data, 'chain_complete'),
      iteration_ms: data.metrics.chain_iteration_ms ? data.metrics.chain_iteration_ms.values : null,
    },
    abandon: {
      lock_200: metricCount(data, 'abandon_lock_200'),
      lock_other: metricCount(data, 'abandon_lock_other'),
      wait_seconds: ABANDON_WAIT_SECONDS,
    },
    duplicate: {
      notify_200: metricCount(data, 'dup_notify_200'),
      notify_other: metricCount(data, 'dup_notify_other'),
      callbacks_per_iteration: DUP_CALLBACKS,
    },
    http_requests: metricCount(data, 'http_reqs'),
    duration_ms: Math.round(data.state ? data.state.testRunDurationMs : 0),
  };

  const headline =
    `\n[mixed] chain: lock=${summary.chain.lock_200}/${summary.chain.lock_200 + summary.chain.lock_other}` +
    ` submit=${summary.chain.submit_201}/${summary.chain.submit_201 + summary.chain.submit_other}` +
    ` notify=${summary.chain.notify_200}/${summary.chain.notify_200 + summary.chain.notify_other}` +
    ` | abandon locks=${summary.abandon.lock_200}` +
    ` | dup callbacks=${summary.duplicate.notify_200}` +
    ` | http_reqs=${summary.http_requests} duration=${summary.duration_ms}ms\n`;

  return {
    'tests/load/results/mixed-summary.json': JSON.stringify(summary, null, 2),
    stdout: headline,
  };
}
