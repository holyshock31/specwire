# Tasks: bridge-admin-ui

## 1. Bridge 多 token 验签

- [x] 1.1 config.go：`SPECWIRE_WEBHOOK_SECRETS` 解析（逗号分隔、whsec_ 前缀与 base64 校验）；`SPECWIRE_WEBHOOK_SECRET` 兼容并入；启动校验至少一个有效
- [x] 1.2 handler.go：verifySignature 遍历 secrets 任一匹配；日志不泄露 token
- [x] 1.3 测试：多 token 各自验签、任一不匹配 401、单值兼容、非法列表启动失败

## 2. GitLab hook 编排 API（gitlab.go 扩展）

- [x] 2.1 gitlab.go：CreateHook（url+events+signing_token）、UpdateHook、ListHooks、DeleteHook（复用 HTTP 客户端模式）
- [x] 2.2 token 生成：crypto/rand 32 字节 → `whsec_` + base64
- [x] 2.3 测试：stub GitLab API 断言 hook 创建/更新参数

## 3. 配置管理 API（admin.go）

- [x] 3.1 GET /admin/api/config：配置快照（项目/mapping/hook 状态，不含 token 明文）
- [x] 3.2 POST /admin/api/projects：添加项目（校验 GitLab 项目与 Multica project 存在）+ DELETE 移除
- [x] 3.3 POST /admin/api/hooks/{path}：创建/更新项目 hook（生成 token → GitLab API → 记入内存 SECRETS）
- [x] 3.4 POST /admin/api/hooks/{path}/rotate：token 轮换
- [x] 3.5 POST /admin/api/apply：原子写回 `.env`（临时文件 + rename）
- [x] 3.6 admin 路由安全：`SPECWIRE_ADMIN_TOKEN`（未配置仅回环可访问）
- [x] 3.7 测试：config 快照、添加/移除项目、hook 创建、apply 写回

## 4. 管理页面

- [x] 4.1 admin/static/index.html：单页（项目表/添加表单/hook 操作/配置总览/重启提示），go:embed 嵌入
- [x] 4.2 页面与 API 联调（fetch 调 /admin/api/*）
- [x] 4.3 重启提示逻辑（apply 后提示 `docker compose up -d`）

## 5. 文档与部署

- [x] 5.1 README 增加管理页面入口与使用说明
- [x] 5.2 重新 build 镜像 + 部署（端口：管理页面走独立端口或同端口 /admin 路径）
- [x] 5.3 端到端：页面添加 webdeck 项目 → 自动建 hook + token → 验证两项目事件均正常

## 6. 联调验收

- [x] 6.1 specwire-poc 与 webdeck 双项目事件均通过验签与建卡
- [x] 6.2 token 轮换后旧 token 失效、新 token 生效
- [x] 6.3 配置保存重启后持久生效
