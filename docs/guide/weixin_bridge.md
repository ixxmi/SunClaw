# Weixin Bridge 协议与参考实现

这份文档描述 SunClaw 当前 `weixin` 通道对接的 bridge 协议。

当前仓库里的 `weixin` channel 现在同时支持两种接入方式：

- `mode: bridge` 通过 HTTP bridge 对接
- `mode: direct` 直接对接腾讯 iLink API

这份文档描述的是 `mode: bridge` 的协议。选择 bridge 的原因很直接：

- 主项目不绑定某一个微信底层库
- 扫码登录、心跳保活、风控处理放在 bridge 层更容易替换
- 你可以先用 mock bridge 跑通，再接真实实现

## 1. 什么时候触发扫码

SunClaw 侧的触发时机已经固定：

1. `weixin` channel 启动时
2. 先请求 `GET /session/status`
3. 如果未登录，再请求 `POST /session/start`
4. 只要 bridge 返回：
   - `needs_scan: true`
   - 或 `qr_code_url` 非空
   - 或 `qr_code_base64` 非空
   就表示此时应该扫码
5. 运行过程中如果 `GET /messages` 或 `POST /send` 返回 `401` / `403`，SunClaw 会再次触发登录检查

当前实现里，这个重试有 30 秒节流，避免疯狂触发二维码刷新。

## 2. 必需接口

### `GET /session/status`

返回当前登录态：

```json
{
  "authenticated": false,
  "needs_scan": true,
  "session_id": "wxsess-123",
  "qr_code_url": "https://bridge.example.com/session/scan?session_id=wxsess-123",
  "qr_code_base64": "PHN2Zy4uLg==",
  "expires_at": 1770000000,
  "message": "scan me"
}
```

### `POST /session/start`

作用：

- 如果当前还没登录，开始一轮扫码登录流程
- 返回值结构和 `/session/status` 一样

### `GET /messages`

返回待处理入站消息列表：

```json
{
  "messages": [
    {
      "id": "wxim-1",
      "sender_id": "wx-user",
      "chat_id": "wx-chat",
      "text": "你好",
      "type": "text",
      "timestamp": 1710000000,
      "media": []
    }
  ]
}
```

兼容说明：

- 也可以直接返回数组 `[]`
- SunClaw 会兼容 `messages` 和 `data` 两种包裹字段

### `POST /send`

SunClaw 主动发消息时会发到这里：

```json
{
  "chat_id": "wx-chat",
  "text": "hello",
  "content": "hello",
  "reply_to": "msg-1",
  "media": [
    {
      "type": "image",
      "url": "https://example.com/a.png",
      "name": "a.png",
      "mimetype": "image/png"
    }
  ]
}
```

建议：

- 未登录时返回 `401` 或 `403`
- 已接收则返回 `200`

## 3. 可选但推荐的 mock / debug 接口

参考 bridge 额外提供了这些接口，方便联调：

- `POST /session/scan`
  模拟扫码完成
- `GET /session/qrcode`
  返回 mock 二维码 SVG 预览
- `POST /session/reset`
  重置登录态
- `POST /messages/inject`
  注入一条入站消息
- `GET /sent`
  查看已发送消息
- `GET /debug/state`
  查看完整 mock 状态

## 4. 如何启动参考 bridge

当前仓库已经提供 CLI：

```bash
sunclaw weixin-bridge serve --addr 127.0.0.1:19090
```

如果你希望 bridge 返回可访问的 `qr_code_url`，可以再加：

```bash
sunclaw weixin-bridge serve \
  --addr 0.0.0.0:19090 \
  --public-base-url https://bridge.example.com
```

## 5. 如何手动演练整条链路

1. 启动 bridge

```bash
sunclaw weixin-bridge serve --addr 127.0.0.1:19090
```

2. 配置 SunClaw

```yaml
channels:
  weixin:
    enabled: true
    bridge_url: http://127.0.0.1:19090
```

3. 启动 SunClaw

```bash
sunclaw start
```

4. 模拟扫码完成

```bash
curl -X POST http://127.0.0.1:19090/session/start
curl -X POST http://127.0.0.1:19090/session/scan
```

5. 注入一条微信消息

```bash
curl -X POST http://127.0.0.1:19090/messages/inject \
  -H 'Content-Type: application/json' \
  -d '{
    "sender_id": "wx-user-1",
    "chat_id": "wx-chat-1",
    "content": "图片里是什么？"
  }'
```

6. 查看 bridge 收到的主动发送

```bash
curl http://127.0.0.1:19090/sent
```

## 6. 接真实微信实现时怎么替换

替换方式很简单：

- 保持 HTTP 协议不变
- 把 mock bridge 的会话状态替换成真实微信登录态
- 把 `/messages` 的消息来源替换成真实微信事件
- 把 `/send` 的发送动作替换成真实微信发消息

如果你的底层库和 `picoclaw/pkg/channels/weixin` 一样也是“扫码登录 + 事件回调 + 主动发消息”的模型，那么只要把它包成这套 HTTP 协议，SunClaw 这一侧就不用再改。
