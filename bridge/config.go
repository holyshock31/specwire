// SpecWire Bridge 配置加载：环境变量 + 可选的 ./.env 文件，见 SPECWIRE_BRIDGE_DESIGN.md §4。
package main

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"
)

// Config 是 Bridge 的运行时配置。
type Config struct {
	AllowedProjects  map[string]bool   // SPECWIRE_ALLOWED_PROJECTS：逗号分隔的 GitLab path_with_namespace 白名单
	WebhookSecrets   []string          // SPECWIRE_WEBHOOK_SECRETS：Standard Webhooks signing tokens（whsec_，逗号分隔）；SPECWIRE_WEBHOOK_SECRET 兼容并入
	MulticaProfile   string            // SPECWIRE_MULTICA_PROFILE：multica CLI profile
	MulticaProjectID string            // SPECWIRE_MULTICA_PROJECT_ID：默认 Multica project ID（未配置 PROJECT_MAP 时使用）
	ProjectMap       map[string]string // SPECWIRE_PROJECT_MAP resolve 后：GitLab path → Multica project ID（D20）
	ListenAddr       string            // SPECWIRE_LISTEN_ADDR：监听地址（GitLab 容器需经 host.docker.internal 访问，勿绑 127.0.0.1）
	DBPath           string            // SPECWIRE_DB_PATH：SQLite 文件路径
	RefFilter        string            // SPECWIRE_REF_FILTER：只处理的分支 ref
	CLITimeout       time.Duration     // SPECWIRE_CLI_TIMEOUT：multica CLI 调用超时
	LogLevel         slog.Level        // SPECWIRE_LOG_LEVEL：debug|info|warn|error
	GitLabToken      string            // SPECWIRE_GITLAB_TOKEN：GitLab API token（scope 至少 issues；v2 归档关 Issue）
	GitLabURL        string            // SPECWIRE_GITLAB_URL：GitLab API 基址（容器网络内可达）
	WebhookURL       string            // SPECWIRE_WEBHOOK_URL：GitLab webhook 回调地址（hook 自动编排用，admin API）
	AdminToken       string            // SPECWIRE_ADMIN_TOKEN：admin API 访问 token（未配置时仅回环可访问）
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// LoadConfig 从环境变量加载并校验配置。必填项缺失时返回错误，不静默运行。
func LoadConfig() (*Config, error) {
	cfg := &Config{
		MulticaProfile: getenv("SPECWIRE_MULTICA_PROFILE", "specwire-local"),
		ListenAddr:     getenv("SPECWIRE_LISTEN_ADDR", "0.0.0.0:8787"),
		DBPath:         getenv("SPECWIRE_DB_PATH", "./specwire-bridge.db"),
		RefFilter:      getenv("SPECWIRE_REF_FILTER", "refs/heads/main"),
	}

	cfg.AllowedProjects = parseList(os.Getenv("SPECWIRE_ALLOWED_PROJECTS"))
	if len(cfg.AllowedProjects) == 0 {
		return nil, fmt.Errorf("SPECWIRE_ALLOWED_PROJECTS is required (comma-separated GitLab path_with_namespace list)")
	}

	var err error
	cfg.WebhookSecrets, err = loadWebhookSecrets()
	if err != nil {
		return nil, err
	}

	cfg.MulticaProjectID = os.Getenv("SPECWIRE_MULTICA_PROJECT_ID")
	if cfg.MulticaProjectID == "" {
		return nil, fmt.Errorf("SPECWIRE_MULTICA_PROJECT_ID is required")
	}

	timeout := getenv("SPECWIRE_CLI_TIMEOUT", "30s")
	d, err := time.ParseDuration(timeout)
	if err != nil {
		return nil, fmt.Errorf("SPECWIRE_CLI_TIMEOUT %q is not a valid duration: %w", timeout, err)
	}
	cfg.CLITimeout = d

	level, err := parseLogLevel(getenv("SPECWIRE_LOG_LEVEL", "info"))
	if err != nil {
		return nil, err
	}
	cfg.LogLevel = level

	cfg.GitLabToken = os.Getenv("SPECWIRE_GITLAB_TOKEN")
	cfg.GitLabURL = getenv("SPECWIRE_GITLAB_URL", "http://gitlab.specwire.test:8929")
	cfg.WebhookURL = getenv("SPECWIRE_WEBHOOK_URL", "http://host.docker.internal:8787/gitlab/specwire")
	cfg.AdminToken = os.Getenv("SPECWIRE_ADMIN_TOKEN")

	return cfg, nil
}

// loadWebhookSecrets 加载 signing tokens 列表：
//   - 新配置 SPECWIRE_WEBHOOK_SECRETS（逗号分隔）优先；
//   - 旧配置 SPECWIRE_WEBHOOK_SECRET 兼容并入（SECRETS 已配置时追加，优先级低于 SECRETS）；
//   - 空列表（两个配置都缺失/全空）→ 启动失败。
func loadWebhookSecrets() ([]string, error) {
	secretsRaw := os.Getenv("SPECWIRE_WEBHOOK_SECRETS")
	legacy := os.Getenv("SPECWIRE_WEBHOOK_SECRET")
	raw := secretsRaw
	if raw == "" {
		raw = legacy
	}
	secrets, err := parseSecrets(raw)
	if err != nil {
		return nil, err
	}
	if len(secrets) == 0 {
		return nil, fmt.Errorf("SPECWIRE_WEBHOOK_SECRETS is required (comma-separated whsec_ signing tokens; legacy SPECWIRE_WEBHOOK_SECRET also accepted)")
	}
	if legacy != "" && secretsRaw != "" && !slices.Contains(secrets, legacy) {
		secrets = append(secrets, legacy)
	}
	return secrets, nil
}

// parseSecrets 解析逗号分隔的 signing token 列表：
// 每项 trim 后必须非空、以 whsec_ 开头且其后为合法 base64，否则报错。
func parseSecrets(s string) ([]string, error) {
	var out []string
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if !strings.HasPrefix(item, "whsec_") {
			return nil, fmt.Errorf("webhook secret %q: must be a Standard Webhooks signing token starting with whsec_", maskSecret(item))
		}
		if _, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(item, "whsec_")); err != nil {
			return nil, fmt.Errorf("webhook secret: payload after whsec_ must be base64: %w", err)
		}
		out = append(out, item)
	}
	return out, nil
}

// maskSecret 把 token 缩略成前 6 位 + 省略号，避免错误信息泄露完整 token。
func maskSecret(s string) string {
	if len(s) <= 6 {
		return "***"
	}
	return s[:6] + "..."
}

// parseList 解析逗号分隔列表，去除空白和空项。
func parseList(s string) map[string]bool {
	out := map[string]bool{}
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			out[item] = true
		}
	}
	return out
}

func parseLogLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("SPECWIRE_LOG_LEVEL %q invalid: want debug|info|warn|error", s)
	}
}

// parseProjectMap 解析 SPECWIRE_PROJECT_MAP：逗号分隔的 "GitLab path:Multica title/ID" 条目。
// 例如：specwire/specwire-poc:SpecWire PoC,personal/webdeck:WebDeck
// 值可以是 Multica project title（可读）或 UUID（直通）。
func parseProjectMap(s string) (map[string]string, error) {
	out := map[string]string{}
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		k, v, ok := strings.Cut(item, ":")
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if !ok || k == "" || v == "" {
			return nil, fmt.Errorf("SPECWIRE_PROJECT_MAP entry %q invalid: want <gitlab path>:<multica title or id>", item)
		}
		if _, dup := out[k]; dup {
			return nil, fmt.Errorf("SPECWIRE_PROJECT_MAP: duplicate gitlab path %q", k)
		}
		out[k] = v
	}
	return out, nil
}

// resolveProjectMap 把映射值解析为 Multica project UUID：
//   - 值本身是 UUID → 直通；
//   - 值是 title → 在 projects 中查找（title 重复 → 报错，要求改用 UUID）。
func resolveProjectMap(entries map[string]string, projects []multicaProject) (map[string]string, error) {
	byTitle := map[string][]string{}
	for _, p := range projects {
		byTitle[p.Title] = append(byTitle[p.Title], p.ID)
	}
	out := map[string]string{}
	for gitlabPath, val := range entries {
		switch {
		case isUUIDish(val):
			out[gitlabPath] = val
		default:
			ids := byTitle[val]
			if len(ids) == 0 {
				return nil, fmt.Errorf("SPECWIRE_PROJECT_MAP: multica project %q (for %s) not found", val, gitlabPath)
			}
			if len(ids) > 1 {
				return nil, fmt.Errorf("SPECWIRE_PROJECT_MAP: multica project title %q ambiguous (%d matches); use its UUID instead", val, len(ids))
			}
			out[gitlabPath] = ids[0]
		}
	}
	return out, nil
}

// isUUIDish 判断字符串是否为 UUID 形态（36 字符，十六进制 + 连字符）。
func isUUIDish(s string) bool {
	if len(s) != 36 {
		return false
	}
	for _, c := range s {
		if c == '-' {
			continue
		}
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// loadDotEnv 加载 KEY=VALUE 格式的 .env 文件（本地开发用）：
//   - 忽略空行与 # 注释；支持单/双引号包裹的值（去除引号）。
//   - 已存在的环境变量优先，.env 不覆盖（真实部署用 env 注入时行为一致）。
//   - 文件不存在时静默返回 nil。
func loadDotEnv(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if os.Getenv(k) != "" {
			continue // 环境变量优先
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		if err := os.Setenv(k, v); err != nil {
			return fmt.Errorf("setenv %s: %w", k, err)
		}
	}
	return nil
}
