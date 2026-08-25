#!/bin/bash
# SpecWire 客户端发布 skill（Issue 事件驱动）：分支 + propose + push + 创建 label=change Issue
#
# 流程（对应 v2 发布模型，见 openspec/changes/v2-issue-publish-model）：
#   1. 基于最新 main 创建 feat/<change-id> 分支
#   2. 生成 change 骨架（openspec new change，若不存在）
#   3. 精确暂存 change 目录 → commit → push 分支
#   4. 创建 GitLab Issue（labels=[change]），描述含 change_id/branch/branch_head_sha
#      （可选 --todo / --assignee 写入 SpecWire-Status / SpecWire-Assignee）
#
# 用法：publish-v2.sh [--todo] [--assignee <name>] <change-id>
# 前置：SPECWIRE_GITLAB_TOKEN 已配置（宿主视角 GitLab API）；
#       目标仓库 main 与 upstream 同步。
#
# 依赖：openspec CLI、GitLab API（不需要 glab）。

set -euo pipefail

STATUS="backlog"
ASSIGNEE=""
CHANGE_IDS=()
while [ "$#" -gt 0 ]; do
  case "$1" in
    --todo) STATUS="todo"; shift ;;
    --assignee) ASSIGNEE="${2:-}"; if [ -z "$ASSIGNEE" ]; then echo "✗ --assignee 需要参数" >&2; exit 1; fi; shift 2 ;;
    -h|--help) sed -n '1,16p' "$0" | grep '^#' | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) CHANGE_IDS+=("$1"); shift ;;
  esac
done
if [ "${#CHANGE_IDS[@]}" -ne 1 ]; then
  echo "usage: publish-v2.sh [--todo] [--assignee <name>] <change-id>" >&2
  exit 1
fi
CHANGE_ID="${CHANGE_IDS[0]}"
BRANCH="feat/$CHANGE_ID"

GITLAB_TOKEN="${SPECWIRE_GITLAB_TOKEN:-}"
if [ -z "$GITLAB_TOKEN" ]; then
  echo "✗ SPECWIRE_GITLAB_TOKEN 未设置（创建 GitLab Access Token，scope: issues）" >&2
  exit 1
fi
# 宿主视角 GitLab API 地址（统一使用 GitLab canonical host）
GITLAB_URL="${SPECWIRE_GITLAB_URL:-http://gitlab.specwire.test:8929}"

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"
UPSTREAM="$(git rev-parse --abbrev-ref --symbolic-full-name @{u} 2>/dev/null || true)"
UPSTREAM_REMOTE="${UPSTREAM%%/*}"
if [ -z "$UPSTREAM_REMOTE" ] || [ "$UPSTREAM_REMOTE" = "$UPSTREAM" ]; then
  echo "✗ 当前分支没有 upstream 跟踪分支" >&2
  exit 1
fi

# ---------- 1. change 骨架检查（在 main 上，先于建分支） ----------
if [ ! -d "openspec/changes/$CHANGE_ID" ]; then
  echo "→ change 不存在，生成骨架（请填写 proposal/spec/design/tasks 后重新运行本脚本）"
  openspec new change "$CHANGE_ID"
  echo "已生成：openspec/changes/$CHANGE_ID —— 填写完成后重新运行 publish-v2.sh"
  exit 0
fi

# ---------- 2. 前置：同步 main + 建分支 ----------
git fetch "$UPSTREAM_REMOTE" main -q
if git rev-parse --verify "refs/heads/$BRANCH" >/dev/null 2>&1; then
  echo "✗ 分支 $BRANCH 已存在（重复发布？先处理或删除）" >&2
  exit 1
fi
git checkout -q -b "$BRANCH" "$UPSTREAM_REMOTE/main"

# ---------- 3. 校验 + 暂存 + 提交 + 推送 ----------
echo "→ openspec validate $CHANGE_ID"
openspec validate "$CHANGE_ID"
if ! git diff --quiet; then
  echo "✗ 存在未提交改动（分支创建后不应有；确认后 git stash 或提交）" >&2
  git status --short >&2
  exit 1
fi
git add -A -- "openspec/changes/$CHANGE_ID"
git commit -m "spec($CHANGE_ID): publish proposal (v2 branch)" -q
BRANCH_HEAD_SHA="$(git rev-parse HEAD)"
git push -q -u "$UPSTREAM_REMOTE" "$BRANCH"
echo "→ 分支 $BRANCH 已推送（$BRANCH_HEAD_SHA）"

# ---------- 4. 创建 GitLab Issue ----------
DESC="change_id: $CHANGE_ID
branch: $BRANCH
branch_head_sha: $BRANCH_HEAD_SHA"
if [ "$STATUS" = "todo" ]; then
  DESC="$DESC
SpecWire-Status: todo"
fi
if [ -n "$ASSIGNEE" ]; then
  DESC="$DESC
SpecWire-Assignee: $ASSIGNEE"
fi

# 项目路径：从 remote URL 推断 GitLab 项目路径（支持 ssh/scp/http(s) 格式）
REMOTE_URL="$(git remote get-url "$UPSTREAM_REMOTE")"
PROJECT_PATH="$(python3 - "$REMOTE_URL" <<'PYEOF'
import re, sys
url = sys.argv[1].strip()
# ssh: git@host:group/proj.git | ssh://git@host:port/group/proj.git | http(s)://host/group/proj[.git]
m = re.search(r'[:/]([^/:]+/[^/:]+?)(?:\.git)?/?$', url)
print(m.group(1) if m else '')
PYEOF
)"
if [ -z "$PROJECT_PATH" ]; then
  echo "✗ 无法从 remote URL 推断 GitLab 项目路径: $REMOTE_URL" >&2
  echo "  可设置 SPECWIRE_GITLAB_PROJECT 覆盖" >&2
  exit 1
fi
PROJECT_PATH="${SPECWIRE_GITLAB_PROJECT:-$PROJECT_PATH}"
echo "→ 目标项目: $PROJECT_PATH"

TITLE="[change] $CHANGE_ID"
BODY_JSON=$(python3 - "$TITLE" "$DESC" <<'PYEOF'
import json, sys
print(json.dumps({"title": sys.argv[1], "labels": "change", "description": sys.argv[2]}))
PYEOF
)
RESP=$(curl -fsS -X POST "$GITLAB_URL/api/v4/projects/$(python3 -c "import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1],safe=''))" "$PROJECT_PATH")/issues" \
  -H "PRIVATE-TOKEN: $GITLAB_TOKEN" -H "Content-Type: application/json" \
  -d "$BODY_JSON" 2>&1) || { echo "✗ 创建 Issue 失败: $RESP" >&2; exit 1; }

IID=$(echo "$RESP" | python3 -c "import json,sys;print(json.load(sys.stdin)['iid'])")
echo "✓ 已发布 $CHANGE_ID：分支 $BRANCH（$BRANCH_HEAD_SHA）→ Issue #$IID（label: change）"
echo "  Bridge 收到 Issue Hook 后将创建 Multica 卡（status=$STATUS${ASSIGNEE:+ assignee=$ASSIGNEE}）"
