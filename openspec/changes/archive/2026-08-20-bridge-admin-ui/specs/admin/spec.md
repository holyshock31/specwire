# admin Specification

## Purpose

定义 SpecWire 配置管理界面（Bridge 内嵌 `/admin`）的行为契约：项目管理、hook 生命周期、token 管理与配置持久化。

## ADDED Requirements

### Requirement: 项目配置可视化管理

管理页面展示全部 GitLab 项目（allowlist 项目 + 各自映射的 Multica project），支持添加/移除项目（同步更新 allowlist 与映射配置）。页面基于 Bridge 配置只读快照 + 变更 API，不直接暴露 `.env` 文件。

#### Scenario: 添加项目

管理员在页面输入 GitLab 项目路径并选择对应 Multica project，保存后 Bridge 配置更新，新项目进入 allowlist 与映射。

#### Scenario: 移除项目

管理员移除项目后，其 allowlist 与映射条目一并删除；已存在的历史卡不受影响。

### Requirement: Hook 生命周期自动化

页面为每个项目管理 GitLab webhook：创建时自动生成独立 signing token（`whsec_` + 32 字节随机 key）并调用 GitLab API 配置（push+issues 事件、项目唯一 token）；展示各项目 hook 状态（存在/缺失/token 轮换）；支持 token 轮换（重新生成并更新 hook 与 Bridge 配置）。

#### Scenario: 项目无 hook 时创建

新项目添加后，页面显示 hook 缺失并提供"创建"操作；点击后自动完成 token 生成 + GitLab API 配置。

#### Scenario: token 轮换

管理员点击轮换，生成新 token、更新 GitLab hook（signing_token）与 Bridge 配置中的对应项；旧 token 不再接受。

### Requirement: 配置持久化与生效

变更写回 `.env`（保留现有加载机制与注释），页面提示"需重启生效"；Bridge 启动时校验配置一致性（多 secret 至少一个、allowlist 与映射匹配）。

#### Scenario: 保存后重启生效

页面保存配置并提示重启；重启后新配置生效，旧配置不残留。

#### Scenario: 非法配置被拒绝保存

页面提交的配置包含非法值（如空 allowlist、非法 Multica 项目），保存被拒绝并显示错误。
