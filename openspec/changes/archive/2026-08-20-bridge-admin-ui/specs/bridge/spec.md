# bridge Specification（delta：多 token 验签）

## MODIFIED Requirements

### Requirement: Bridge 校验并过滤 GitLab 事件

Bridge 是常驻 HTTP 服务（Docker Compose 部署），接收 GitLab webhook：
- 验签：Standard Webhooks HMAC-SHA256（`webhook-id`/`webhook-timestamp`/`webhook-signature`，±5 分钟防重放），非法请求 401
- **多 token 支持**：接受 `SPECWIRE_WEBHOOK_SECRETS`（逗号分隔）中的任一 signing token；单值 `SPECWIRE_WEBHOOK_SECRET` 兼容（视为单元素列表）
- 过滤：只处理 `Push Hook` 与 `Issue Hook`、只处理 main ref（push）、project allowlist（`SPECWIRE_ALLOWED_PROJECTS`）

#### Scenario: 非法签名请求被拒绝

请求签名不匹配或时间戳过期，Bridge 返回 401 且不产生任何副作用。

#### Scenario: 非目标项目事件被忽略

非 allowlist 项目的 push 事件返回 `200 ignored`，不建卡。

#### Scenario: 多项目各自 token 均可验签

specwire-poc 与 webdeck 的 hook 使用不同 signing token，两者事件均通过验签并正常处理。

#### Scenario: 任一 token 不匹配时拒绝

请求签名与配置的全部 token 均不匹配，Bridge 返回 401 且不产生任何副作用。

#### Scenario: 旧单值配置继续可用

仅配置 `SPECWIRE_WEBHOOK_SECRET` 时行为与升级前一致（单 token 验签）。
