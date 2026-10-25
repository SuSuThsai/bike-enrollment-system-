// 本地开发默认直连后端；真机调试请在微信开发者工具中关闭域名校验，
// 上线前替换为已备案的 HTTPS 域名。
module.exports = {
  API_BASE: 'http://127.0.0.1:8080/api/v1',

  // 在微信公众平台申请订阅消息模板后填入；为空时无法拉起授权弹窗。
  SUBSCRIBE_TEMPLATE_IDS: [],
}
