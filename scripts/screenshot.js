/**
 * 项目截图脚本（Playwright）
 *
 * 输出：
 *   docs/screenshots/website-list.png       官网赛事列表
 *   docs/screenshots/website-detail.png     官网赛事详情
 *   docs/screenshots/admin-enrollments.png  管理后台报名名单
 *
 * 规格：视口 1440x900，deviceScaleFactor=2（输出 2880x1800），截图前等待网络空闲。
 *
 * 用法：
 *   node screenshot.js
 *   node screenshot.js --event <event_id>
 *   node screenshot.js --admin-token <JWT>
 *   node screenshot.js --scroll-to ".event-grid"   # 截图前滚动到指定元素（可选）
 *   node screenshot.js --scroll-y 690              # 截图前滚动指定像素（可选）
 *
 * 环境变量：
 *   WEBSITE_URL     官网地址，默认 http://localhost:5174
 *   ADMIN_URL       后台地址，默认 http://localhost:5173
 *   ADMIN_TOKEN     管理后台 JWT（必填，否则报名名单页只会显示"设置 Token"）
 *   EVENT_ID        详情页赛事 ID；缺省时自动从 API 取一场"报名中"的赛事
 *   API_BASE        后端地址，默认 http://localhost:8080/api/v1
 *   CHROME_CHANNEL  默认 chrome（驱动本机已安装的 Chrome，免下载 Chromium）
 */
import { chromium } from 'playwright-core'
import { mkdir, readFile } from 'node:fs/promises'
import path from 'node:path'

const HERE = import.meta.dirname
const ROOT = path.resolve(HERE, '..')

// ---------- 参数解析 ----------
function arg(name, fallback) {
  const i = process.argv.indexOf(`--${name}`)
  if (i !== -1 && process.argv[i + 1]) return process.argv[i + 1]
  return fallback
}

const WEBSITE_URL = arg('website-url', process.env.WEBSITE_URL || 'http://localhost:5174')
const ADMIN_URL = arg('admin-url', process.env.ADMIN_URL || 'http://localhost:5173')
const ADMIN_TOKEN = arg('admin-token', process.env.ADMIN_TOKEN || '')
const API_BASE = arg('api-base', process.env.API_BASE || 'http://localhost:8080/api/v1')
const CHROME_CHANNEL = arg('channel', process.env.CHROME_CHANNEL || 'chrome')
const OUT_DIR = path.resolve(ROOT, arg('out', 'docs/screenshots'))

const VIEWPORT = { width: 1440, height: 900 }
const SCALE = 2
const TOKEN_KEY = 'bike_admin_token'
// 可选：截图前滚动位置。默认 0 = 页面顶部（列表页只露出赛事卡片上半部分时，
// 可用 --scroll-to 把卡片完整纳入 1440x900 视口）。
const SCROLL_TO = arg('scroll-to', process.env.SCROLL_TO || '')
const SCROLL_Y = Number(arg('scroll-y', process.env.SCROLL_Y || '0'))

// ---------- 自动挑选赛事 ----------
async function resolveEventId() {
  const explicit = arg('event', process.env.EVENT_ID || '')
  if (explicit) return { id: explicit, source: '参数' }

  const res = await fetch(`${API_BASE}/events?page=1&page_size=50`)
  if (!res.ok) throw new Error(`拉取赛事列表失败：HTTP ${res.status}`)
  const { items = [] } = await res.json()
  const published = items.filter((e) => e.lifecycle_status === 'published')
  const open = published.find((e) => e.registration_status === 'open')
  const picked = open || published[0] || items[0]
  if (!picked) throw new Error('数据库中没有可用赛事，无法截取详情页')
  return { id: picked.id, source: `API 自动选取（${picked.title}）` }
}

// ---------- 读取 PNG 尺寸（校验 dsf 是否生效） ----------
async function pngSize(file) {
  const buf = await readFile(file)
  // PNG 签名 8 字节 + IHDR 长度 4 + 类型 4，随后 width/height 各 4 字节（大端）
  return { width: buf.readUInt32BE(16), height: buf.readUInt32BE(20), bytes: buf.length }
}

// ---------- 主流程 ----------
async function main() {
  await mkdir(OUT_DIR, { recursive: true })

  if (!ADMIN_TOKEN) {
    throw new Error(
      '缺少 ADMIN_TOKEN：报名名单页需要 JWT 才能显示数据。\n' +
      '  获取方式：浏览器打开管理后台 → 粘贴 role=admin 的 JWT → \n' +
      '            DevTools Console 执行 localStorage.getItem("bike_admin_token")\n' +
      '  然后：node screenshot.js --admin-token "<JWT>"',
    )
  }

  const { id: eventId, source } = await resolveEventId()
  console.log(`赛事 ID：${eventId}（${source}）`)

  const browser = await chromium.launch({ channel: CHROME_CHANNEL, headless: true })
  const results = []
  try {
    const context = await browser.newContext({ viewport: VIEWPORT, deviceScaleFactor: SCALE })
    // 必须在页面加载前写入 token，否则首屏请求不带 Authorization
    await context.addInitScript(
      ([key, token]) => { localStorage.setItem(key, token) },
      [TOKEN_KEY, ADMIN_TOKEN],
    )

    async function shoot({ file, url, expectText, expectRows }) {
      const page = await context.newPage()
      try {
        const resp = await page.goto(url, { waitUntil: 'networkidle', timeout: 45000 })
        if (!resp || !resp.ok()) throw new Error(`HTTP ${resp ? resp.status() : 'no response'}`)

        // 断言页面真的渲染出了目标内容，避免"路由没匹配"也截出一张空图
        const body = await page.locator('body').innerText()
        if (expectText && !body.includes(expectText)) {
          throw new Error(`页面未出现预期内容「${expectText}」，实际标题：${await page.title()}`)
        }
        if (expectRows) {
          await page.waitForFunction(
            (min) => document.querySelectorAll('tbody tr').length >= min,
            expectRows,
            { timeout: 20000 },
          )
        }

        // 可选滚动，用于把目标区块完整纳入视口
        if (SCROLL_TO) {
          const target = page.locator(SCROLL_TO).first()
          await target.scrollIntoViewIfNeeded({ timeout: 5000 })
        } else if (Number.isFinite(SCROLL_Y) && SCROLL_Y > 0) {
          await page.evaluate((y) => window.scrollTo(0, y), SCROLL_Y)
        }

        // networkidle 之后再等一帧，确保字体与过渡动画稳定
        await page.waitForTimeout(400)

        const out = path.join(OUT_DIR, file)
        await page.screenshot({ path: out, fullPage: false })
        const meta = await pngSize(out)
        const rows = await page.locator('tbody tr').count()
        results.push({ file, url, out, ...meta, rows })
        console.log(`  ✓ ${file}  ${meta.width}x${meta.height}  ${(meta.bytes / 1024).toFixed(1)} KB`)
      } finally {
        await page.close()
      }
    }

    console.log('开始截图…')
    await shoot({
      file: 'website-list.png',
      url: `${WEBSITE_URL}/`,
      expectText: '赛事列表',
    })
    await shoot({
      file: 'website-detail.png',
      url: `${WEBSITE_URL}/events/${eventId}`,
      expectText: '赛事介绍',
    })
    await shoot({
      file: 'admin-enrollments.png',
      url: `${ADMIN_URL}/enrollments`,
      expectText: '报名名单',
      expectRows: 1,
    })
  } finally {
    await browser.close()
  }

  console.log('\n截图完成：')
  for (const r of results) {
    console.log(`  ${r.file.padEnd(24)} ${r.out}`)
    console.log(`  ${''.padEnd(24)} ${r.bytes} bytes  ${r.width}x${r.height}  表格行数=${r.rows}`)
  }
}

main().catch((err) => {
  console.error(`\n截图失败：${err.message}`)
  process.exit(1)
})
