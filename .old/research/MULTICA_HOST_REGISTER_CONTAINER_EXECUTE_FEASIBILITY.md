# Multica "宿主注册、容器执行" 可行性调研

> 日期：2026-08-20
> 状态：调研结论（供 SpecWire 决定是否单开项目）
> 证据基础：multica-ai/multica 仓库 `main` 分支源码（daemon 为 Go，位于 `server/internal/daemon/`、`server/pkg/agent/`）+ 官方文档源码（`apps/docs/content/docs/*.zh.mdx`）+ 官方 issue #1370
> 结论：**可行（有条件）**——官方没有此能力且明确不做，但协议层与官方接缝使其成立；需要自研一层 wrapper 封装。

## 1. 结论摘要

"宿主注册、容器执行"指：**Multica 守护进程（daemon）留在宿主机**（负责注册运行时、领取任务、创建任务目录、管理 git），**AI 编程工具进程跑在 Docker 容器里**（真正的文件修改、命令执行、模型调用都在容器内）。

结论：

1. **技术上可行**。全部受支持工具的通信协议都是**纯 stdin/stdout 管道**（stdio JSON-RPC），`docker run -i` / `docker exec -i` 可直接桥接，不需要 TTY、不需要共享 socket、不需要网络。
2. **官方有认可的接缝**：自定义运行时配置（custom runtime profile）明确支持 wrapper 命令，CLI 有 `multica runtime profile create`；wrapper 作为命令名注册即可，无需伪装工具二进制。
3. **官方立场是不支持也不打算做**：issue #1370（用户提了完全相同的需求——担心 agent 污染宿主机）官方答复"独立容器 + 网络调用需要额外封装（如 `docker exec` wrapper），理论可行，但引入额外延迟和故障点，不推荐"；官方方向是"整体容器化"（daemon 一起进容器），并明确**不会做单 runtime 沙箱**（#6233 已移除 Codex 的 workspace-write 沙箱，理由见安全模型文档）。
4. **需要自研解决的工程点**集中在：进程清理（孤儿容器）、工具登录态/会话恢复的持久化、认证文件路径、Remote MCP broker 的网络可达性（特定场景）、UID/路径映射。
5. **建议**：可单开项目，MVP 用 Claude Code 或 Codex + 每任务 `docker run` wrapper；先做原型验证四条关键路径（注册、执行落盘、重试续会话、停止/超时清理），再进入产品化。

## 2. 为什么可行：三个官方接缝

### 2.1 协议层全部是 stdio（决定性证据）

daemon 通过 `agent.Backend` 接口把工具作为子进程拉起，用 stdin/stdout 管道通信：

| 工具 | 协议 | 证据（源码） |
|---|---|---|
| Claude Code | `--output-format stream-json --input-format stream-json`，`cmd.StdoutPipe()`/`cmd.StdinPipe()` | `server/pkg/agent/claude.go:103-108, 720-721` |
| Codex | `codex app-server --listen stdio://`，JSON-RPC 2.0 over stdin/stdout | `server/pkg/agent/codex.go:257-258, 1050-1055`；`--listen` 在自定义参数中被屏蔽（codex.go:33） |
| DeepSeek Harness (dsh) | 自有 stdio JSON 协议（"The adapter is intentionally independent of ACP"） | `server/pkg/agent/dsh.go:26` |
| 其他（pi/omp、opencode、cursor 等） | 同一套 stdio 启动边界 | `server/pkg/agent/launch.go`（唯一 spawn 入口 `Command.exec`） |

结论：daemon ↔ 工具之间只有 stdin/stdout 两条管道。容器化只需要"wrapper 把宿主 daemon 的 stdin/stdout 转发进容器"，这是 `docker run -i` / `docker exec -i` 的原生能力。

### 2.2 自定义运行时配置 = 官方认可的 wrapper 接缝

- 官方文档《守护进程与运行时》"自定义运行时配置"一节：wrapper、固定版本可执行文件、追加固定参数均支持；命令字段不允许管道/重定向/`&&`/`;`/反引号/环境变量展开，"需要这些行为时，把它们放进 wrapper script，再把该脚本作为命令填写"。
- 源码 `server/pkg/agent/launch.go`：`Command{Path, Prefix}`，argv 顺序为 `<Path> <Prefix...> <protocol args...> <ExtraArgs...> <CustomArgs...>`；注释明确以 `ccms start q36` 这类 wrapper 为例（MUL-3284，GH #7046）。
- 协议关键 flag（`-p`、`--output-format`、`--input-format`、`--permission-mode` 等）会由 `FilterLaunchPrefix` 从 fixed_args 中剔除（claude.go:704-705 `blockedWithValue`；daemon.go:2823/4079/6195 调用），避免 wrapper 覆盖协议；wrapper 只需原样透传 daemon 追加的参数。
- 官方 CLI：`multica runtime profile create --protocol-family <fam> --command-name <cmd> --display-name <name>`（`server/cmd/multica/cmd_runtime_profile.go`）；注释原文："launched via a site-specific command_name (e.g. a wrapper that injects credentials)"——wrapper 就是官方预设的用法。
- **版本门槛**：自定义 profile 的版本探测是 best-effort，探测失败以空版本照常注册（daemon.go:2800-2830，"an empty version is acceptable"）。因此 wrapper 不需要伪装 `--version`。
- 对比：若不用 custom profile 而是把 wrapper 命名为 `claude` 放到 PATH 上冒名顶替，会走内置探测 `detectBuiltinRuntimes` 的最小版本门（daemon.go:2392+，并发 `--version` 探测），易碎且不必要——**优先用 custom runtime profile**。

### 2.3 每任务环境都在宿主机 daemon 侧创建，以 CWD + 环境变量传给工具

- 每任务环境根目录（env root）由 daemon 在宿主机创建：`WorkDir = {RootDir}/workdir/`（`execenv.go:234-237`），`ExecOptions.Cwd = env.WorkDir`（daemon.go:7026）——容器只需把 env root **bind-mount 到同一绝对路径**并 `-w` 到 workdir。
- Codex 每任务 `CODEX_HOME` 在 env root 内创建，`auth.json` 符号链接到共享 `~/.codex/auth.json`，`config.json/toml` 拷贝隔离（`execenv/codex_home.go`）——容器内需要同路径可达工具登录态（见 §3.3）。
- git 由 daemon 自己在宿主机管理：`git fetch`、每任务 `git worktree add` 并重放脏状态（`execenv/git.go`、`execenv/local_worktree.go`）——容器只需看到挂载后的 workdir。
- Skill 注入位置全部在 workdir/CODEX_HOME 内（`.claude/skills/`、`$CODEX_HOME/skills/`、`.dsh/skills/`，官方《AI 编程工具对照》表格）——随 bind-mount 自然进入容器。
- 任务级 `MULTICA_TOKEN`、workspaces root 等由 `taskMulticaEnvironment` 注入（daemon.go:151-155），wrapper 透传即可。

## 3. 需要自研解决的工程点（风险清单）

1. **进程清理 / 孤儿容器**：daemon 通过进程组终止工具：SIGTERM → 1 秒宽限 → SIGKILL，且结束后还会清扫残留进程组（`processtree/controller_unix.go`，`gracefulStopTimeout = 1s`）。`docker run`/`docker exec` 客户端进程被 SIGKILL 后**容器进程不会自动退出**（容器生命周期独立于 CLI 客户端）。wrapper 必须自行处理：trap 信号转发 `docker kill`、`--cidfile` + 退出时清理、容器内 `--init` 且 stdin EOF 即退出。这是整个方案里最需要测试的环节。
2. **会话恢复**：daemon 把 `ResumeSessionID` 传给后端（daemon.go:7034），工具从自身状态目录恢复会话（Claude 的 `~/.claude` 会话、Codex 的 CODEX_HOME sessions、dsh 类似）。容器若每次销毁，重试/续会话会失败——工具 home/会话目录需要**持久 volume**（按运行时固定，不按任务销毁）。
3. **认证**：Codex 每任务 CODEX_HOME 的 `auth.json` 符号链接指向宿主机 `~/.codex/auth.json`；容器内该路径不存在则登录态断裂。两条出路：①容器镜像/volume 内单独完成工具登录（登录态不依赖宿主机）；②把宿主机工具 home 只读挂载进容器同一路径。推荐①（更干净，符合隔离目标）。
4. **Remote MCP broker（特定场景）**：当任务配置了"远程 MCP"连接时，daemon 在**宿主机** `127.0.0.1:0` 起 HTTP broker 并把 `http://127.0.0.1:<port>/<path>` 写进工具 MCP 配置（`remote_mcp_broker.go:112,139-140`）。容器内 127.0.0.1 指不到宿主 → 该场景需要 `--network host`（Linux）或 wrapper 解析端口做转发。**不使用 remote MCP 则无此问题**。
5. **路径与 UID**：bind-mount 需容器内外同绝对路径；容器用户 UID 需与宿主机 daemon 用户匹配（或 user namespace 映射），否则工具写出的文件权限错乱、daemon 后续 git 操作（宿主侧）读不了。
6. **性能与故障面**：每次任务 `docker run` 有镜像启动开销；官方在 issue #1370 明确指出 wrapper 层"引入额外延迟和故障点"——需要常驻容器（`docker exec`）或预热镜像来缓解。
7. **升级风险**：custom profile 依赖协议 flag 的透传与 daemon 的 spawn 行为，随 multica 版本演进可能变化（文档也提示"升级后请跑一个任务确认"）；不属于官方支持面，需自己跟踪。
8. **MULTICA_AGENT_TEMP_BASE / AF_UNIX**：工具子进程可能在任务临时目录绑 AF_UNIX socket（环境变量文档）；工具在容器内时该 socket 在容器内自洽，daemon 不直连，无额外问题（仅路径长度注意）。

## 4. 架构方案对比

| 方案 | 隔离效果 | 复杂度 | 官方支持 | 备注 |
|---|---|---|---|---|
| A. 整体容器化（daemon + 工具同镜像） | 强（注册+执行都在容器） | 低 | **官方推荐**（安全模型文档） | 最简单、最不易踩坑；"注册也在容器里" |
| B. 宿主注册 + 每任务 `docker run`（wrapper） | 强（仅执行在容器） | 中 | 无，但方向获官方认可 | 本文目标方案；镜像启动开销 |
| C. 宿主注册 + 常驻容器 `docker exec`（wrapper） | 强（仅执行在容器） | 中高 | 无；官方在 #1370 提过此路径 | 避免每次启动开销；需容器守护/健康检查 |
| D. 冒名二进制（PATH 上放同名 wrapper） | 取决于内部实现 | 低 | 无 | 不推荐：内置版本门、探测行为、升级易碎 |

## 5. 官方立场（先例 issue #1370）

[multica-ai/multica #1370](https://github.com/multica-ai/multica/issues/1370)（[Question] How to use hermes docker container as a runtime，open）：

- 用户动机与本项目完全一致："担心 hermes 操作到宿主机上的其他文件或者搞坏宿主机的各种环境依赖"。
- 官方答复：没有官方的 "runtime as Docker container" 方案，短期无计划；"agent runtime 都是通过 **stdio JSON-RPC** 与 daemon 通信的本地可执行文件，独立容器 + 网络调用需要额外封装（比如 `docker exec <container> hermes acp`）"，**理论可行**但引入延迟和故障点，不推荐。
- 官方纠正了"挂载二进制到宿主机执行"的误解：那**不产生任何沙箱效果**（进程、文件系统、网络都是宿主机的）。
- 官方明确未来方向：**不做单 runtime 沙箱**（#6233 已移除 Linux Codex 残留的 `workspace-write` 沙箱），把边界交给部署方（[安全模型文档](https://multica.ai/docs/zh/security-model)：专用 Unix 用户 / 容器 / 虚拟机）。
- issue 保留 open，用于跟踪"独立容器 runtime + 远程调用"这个真实需求——即本项目所做的事，官方在等社区方案。

## 6. 单开项目建议（若决定开发）

**结论先行：值得做，先原型后产品化。** 官方不做、需求真实（#1370 即为佐证）、技术接缝存在。

MVP 范围（建议）：

1. **Provider 选型**：Claude Code 或 Codex（stdio 最简单、文档最全）；dsh 也可（本身就是 stdio 协议）。
2. **wrapper**：Go 或 shell 单文件，作为 custom runtime profile 的 command-name；负责：透传 argv 给容器内工具、桥接 stdin/stdout、trap 信号 → `docker kill`、`--cidfile` 退出清理。
3. **容器镜像**：工具二进制 + 容器内完成工具登录（登录态放持久 volume，不随镜像销毁）；`--init`；`--network host`（规避 MCP broker 问题，Linux 服务器场景可接受）。
4. **挂载**：daemon env root（含 workdir/CODEX_HOME）bind-mount 到容器内同路径；UID 对齐。
5. **验证清单**（原型阶段必须过）：
   - [ ] custom profile 注册成功、daemon 探针通过、运行时在 Runtimes 页面在线
   - [ ] issue 触发任务：transcript 实时流出、产物落盘到宿主机 env root、issue 闭环
   - [ ] 任务失败后重试：会话恢复（ResumeSessionID）成功
   - [ ] 停止/超时任务：容器被正确清理，无孤儿容器
   - [ ] 并发任务：目录锁与多容器不冲突
   - [ ] 不使用 remote MCP 的回归路径

项目形态：独立仓库（wrapper + 镜像 + compose + 文档），与 SpecWire 解耦；跟踪 multica release，协议 flag/自定义 profile 行为变更时同步升级。
