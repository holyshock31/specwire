package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// clearEnv 清空全部 Bridge 相关环境变量，保证用例隔离。
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"SPECWIRE_ALLOWED_PROJECTS",
		"SPECWIRE_WEBHOOK_SECRET",
		"SPECWIRE_WEBHOOK_SECRETS",
		"SPECWIRE_WEBHOOK_URL",
		"SPECWIRE_ADMIN_TOKEN",
		"SPECWIRE_MULTICA_PROFILE",
		"SPECWIRE_MULTICA_PROJECT_ID",
		"SPECWIRE_LISTEN_ADDR",
		"SPECWIRE_DB_PATH",
		"SPECWIRE_REF_FILTER",
		"SPECWIRE_CLI_TIMEOUT",
		"SPECWIRE_LOG_LEVEL",
		"SPECWIRE_PERSISTENT_ONLY",
		"SPECWIRE_LEGACY_IMPORT",
		"SPECWIRE_RETENTION_DAYS",
	} {
		t.Setenv(k, "")
	}
}

// setRequired 设置三个必填项。
func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("SPECWIRE_ALLOWED_PROJECTS", "specwire/specwire-poc")
	t.Setenv("SPECWIRE_WEBHOOK_SECRET", tkSigningSecret)
	t.Setenv("SPECWIRE_MULTICA_PROJECT_ID", "3e7d61cd-900b-41a8-85f4-c97019e2020f")
}

func TestLoadConfigDefaults(t *testing.T) {
	clearEnv(t)
	setRequired(t)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.AllowedProjects["specwire/specwire-poc"] {
		t.Errorf("allowlist missing specwire/specwire-poc: %v", cfg.AllowedProjects)
	}
	if len(cfg.WebhookSecrets) != 1 || cfg.WebhookSecrets[0] != tkSigningSecret {
		t.Errorf("secrets = %v, want [%s]", cfg.WebhookSecrets, tkSigningSecret)
	}
	if cfg.WebhookURL != "http://host.docker.internal:8787/gitlab/specwire" {
		t.Errorf("webhook url = %q, want default", cfg.WebhookURL)
	}
	if cfg.MulticaProfile != "specwire-local" {
		t.Errorf("profile = %q, want specwire-local", cfg.MulticaProfile)
	}
	if cfg.ListenAddr != "0.0.0.0:8787" {
		t.Errorf("listen = %q, want 0.0.0.0:8787", cfg.ListenAddr)
	}
	if cfg.DBPath != "./specwire-bridge.db" {
		t.Errorf("dbpath = %q, want ./specwire-bridge.db", cfg.DBPath)
	}
	if cfg.RefFilter != "refs/heads/main" {
		t.Errorf("ref filter = %q, want refs/heads/main", cfg.RefFilter)
	}
	if cfg.CLITimeout != 30*time.Second {
		t.Errorf("cli timeout = %v, want 30s", cfg.CLITimeout)
	}
	if cfg.LogLevel.String() != "INFO" {
		t.Errorf("log level = %v, want INFO", cfg.LogLevel)
	}
	if cfg.PersistentOnly {
		t.Error("persistent_only = true, want false")
	}
	if cfg.RetentionDays != 30 {
		t.Errorf("retention days = %d, want 30", cfg.RetentionDays)
	}
}

func TestLoadConfigPersistentOnlyDoesNotRequireLegacyEnvironment(t *testing.T) {
	clearEnv(t)
	t.Setenv("SPECWIRE_PERSISTENT_ONLY", "true")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.PersistentOnly {
		t.Fatal("persistent_only = false, want true")
	}
	if len(cfg.AllowedProjects) != 0 || len(cfg.WebhookSecrets) != 0 || cfg.MulticaProjectID != "" {
		t.Fatalf("legacy settings should be optional: %+v", cfg)
	}
	if cfg.LegacyImport {
		t.Fatal("persistent-only must not import legacy configuration by default")
	}
}

func TestLoadConfigLegacyImportDefaultsToCompatibilityModeOnly(t *testing.T) {
	clearEnv(t)
	setRequired(t)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.LegacyImport {
		t.Fatal("legacy mode should keep compatibility import enabled by default")
	}
}

func TestLoadConfigPersistentOnlyLegacyImportRequiresOptIn(t *testing.T) {
	clearEnv(t)
	t.Setenv("SPECWIRE_PERSISTENT_ONLY", "true")
	t.Setenv("SPECWIRE_LEGACY_IMPORT", "true")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.LegacyImport {
		t.Fatal("explicit legacy import opt-in was ignored")
	}
}

func TestLoadConfigRetentionDays(t *testing.T) {
	clearEnv(t)
	setRequired(t)
	t.Setenv("SPECWIRE_RETENTION_DAYS", "45")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.RetentionDays != 45 {
		t.Errorf("retention days = %d, want 45", cfg.RetentionDays)
	}
}

func TestLoadConfigRejectsInvalidRetentionDays(t *testing.T) {
	for _, value := range []string{"0", "3651", "not-a-number"} {
		t.Run(value, func(t *testing.T) {
			clearEnv(t)
			setRequired(t)
			t.Setenv("SPECWIRE_RETENTION_DAYS", value)
			if _, err := LoadConfig(); err == nil {
				t.Fatal("LoadConfig: want invalid retention error, got nil")
			}
		})
	}
}

func TestLoadConfigMissingRequired(t *testing.T) {
	cases := []struct {
		name     string
		unsetEnv func(t *testing.T)
	}{
		{"allowlist", func(t *testing.T) { t.Setenv("SPECWIRE_ALLOWED_PROJECTS", "") }},
		{"secret", func(t *testing.T) { t.Setenv("SPECWIRE_WEBHOOK_SECRET", "") }},
		{"project id", func(t *testing.T) { t.Setenv("SPECWIRE_MULTICA_PROJECT_ID", "") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			setRequired(t)
			tc.unsetEnv(t)
			if _, err := LoadConfig(); err == nil {
				t.Fatalf("LoadConfig: want error for missing %s, got nil", tc.name)
			}
		})
	}
}

func TestLoadConfigSecretMustBeWhsec(t *testing.T) {
	clearEnv(t)
	setRequired(t)
	t.Setenv("SPECWIRE_WEBHOOK_SECRET", "plain-token-without-prefix")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig: want error for non-whsec secret, got nil")
	}
}

func TestLoadConfigSecretBadBase64(t *testing.T) {
	clearEnv(t)
	setRequired(t)
	t.Setenv("SPECWIRE_WEBHOOK_SECRET", "whsec_!!!not-base64!!!")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig: want error for bad base64 secret, got nil")
	}
}

// TestLoadDotEnv 验证 .env 解析：KV、注释、空行、引号、环境变量优先、缺失文件静默。
func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	content := "# comment\n\nKEY_A=value-a\nKEY_B=\"quoted b\"\nKEY_C='single c'\n\n"
	if err := os.WriteFile(envFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// KEY_A 已存在于环境 → .env 不应覆盖
	t.Setenv("KEY_A", "from-env")
	// 其余键测试前清空
	t.Setenv("KEY_B", "")
	t.Setenv("KEY_C", "")

	if err := loadDotEnv(envFile); err != nil {
		t.Fatalf("loadDotEnv: %v", err)
	}
	if got := os.Getenv("KEY_A"); got != "from-env" {
		t.Errorf("KEY_A = %q, want from-env (env wins)", got)
	}
	if got := os.Getenv("KEY_B"); got != "quoted b" {
		t.Errorf("KEY_B = %q, want %q", got, "quoted b")
	}
	if got := os.Getenv("KEY_C"); got != "single c" {
		t.Errorf("KEY_C = %q, want %q", got, "single c")
	}
}

func TestLoadDotEnvMissingFile(t *testing.T) {
	if err := loadDotEnv(filepath.Join(t.TempDir(), "no-such-env")); err != nil {
		t.Fatalf("loadDotEnv on missing file: want nil, got %v", err)
	}
}

func TestLoadConfigAllowlistParsing(t *testing.T) {
	clearEnv(t)
	setRequired(t)
	t.Setenv("SPECWIRE_ALLOWED_PROJECTS", " specwire/specwire-poc , other/repo,specwire/another ,,")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	for _, want := range []string{"specwire/specwire-poc", "other/repo", "specwire/another"} {
		if !cfg.AllowedProjects[want] {
			t.Errorf("allowlist missing %q: %v", want, cfg.AllowedProjects)
		}
	}
	if len(cfg.AllowedProjects) != 3 {
		t.Errorf("allowlist size = %d, want 3: %v", len(cfg.AllowedProjects), cfg.AllowedProjects)
	}
}

func TestLoadConfigAllowlistEmpty(t *testing.T) {
	clearEnv(t)
	setRequired(t)
	t.Setenv("SPECWIRE_ALLOWED_PROJECTS", " , , ")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig: want error for empty allowlist, got nil")
	}
}

func TestLoadConfigCLITimeoutCustom(t *testing.T) {
	clearEnv(t)
	setRequired(t)
	t.Setenv("SPECWIRE_CLI_TIMEOUT", "5s")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.CLITimeout != 5*time.Second {
		t.Errorf("cli timeout = %v, want 5s", cfg.CLITimeout)
	}
}

func TestLoadConfigCLITimeoutInvalid(t *testing.T) {
	clearEnv(t)
	setRequired(t)
	t.Setenv("SPECWIRE_CLI_TIMEOUT", "abc")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig: want error for invalid duration, got nil")
	}
}

func TestLoadConfigLogLevelInvalid(t *testing.T) {
	clearEnv(t)
	setRequired(t)
	t.Setenv("SPECWIRE_LOG_LEVEL", "verbose")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig: want error for invalid log level, got nil")
	}
}

// ---------- D20：SPECWIRE_PROJECT_MAP ----------

func TestParseProjectMap(t *testing.T) {
	m, err := parseProjectMap("specwire/specwire-poc:SpecWire PoC, personal/webdeck:WebDeck")
	if err != nil {
		t.Fatalf("parseProjectMap: %v", err)
	}
	if m["specwire/specwire-poc"] != "SpecWire PoC" {
		t.Errorf("specwire map = %q, want %q", m["specwire/specwire-poc"], "SpecWire PoC")
	}
	if m["personal/webdeck"] != "WebDeck" {
		t.Errorf("webdeck map = %q, want WebDeck", m["personal/webdeck"])
	}
}

func TestParseProjectMapInvalid(t *testing.T) {
	for _, s := range []string{"nocolon", "a:", ":b", "a:b,c"} {
		if _, err := parseProjectMap(s); err == nil {
			t.Errorf("parseProjectMap(%q): want error, got nil", s)
		}
	}
}

func TestParseProjectMapDuplicateKey(t *testing.T) {
	if _, err := parseProjectMap("a:x,a:y"); err == nil {
		t.Fatal("parseProjectMap: want error for duplicate key, got nil")
	}
}

func TestResolveProjectMapByTitle(t *testing.T) {
	projects := []multicaProject{
		{ID: tkProjectID, Title: "SpecWire PoC"},
		{ID: "59f6006a-4b60-4219-b736-e7c3d4befd19", Title: "WebDeck"},
	}
	m, err := resolveProjectMap(map[string]string{
		"specwire/specwire-poc": "SpecWire PoC",
		"personal/webdeck":      "WebDeck",
	}, projects)
	if err != nil {
		t.Fatalf("resolveProjectMap: %v", err)
	}
	if m["specwire/specwire-poc"] != tkProjectID {
		t.Errorf("specwire resolve = %q, want %q", m["specwire/specwire-poc"], tkProjectID)
	}
	if m["personal/webdeck"] != "59f6006a-4b60-4219-b736-e7c3d4befd19" {
		t.Errorf("webdeck resolve = %q", m["personal/webdeck"])
	}
}

func TestResolveProjectMapUUIDPassthrough(t *testing.T) {
	m, err := resolveProjectMap(map[string]string{"x": tkProjectID}, []multicaProject{{ID: "other", Title: "other"}})
	if err != nil {
		t.Fatalf("resolveProjectMap: %v", err)
	}
	if m["x"] != tkProjectID {
		t.Errorf("uuid passthrough = %q, want %q", m["x"], tkProjectID)
	}
}

func TestResolveProjectMapNotFound(t *testing.T) {
	if _, err := resolveProjectMap(map[string]string{"x": "不存在的项目"}, []multicaProject{{ID: "a", Title: "A"}}); err == nil {
		t.Fatal("resolveProjectMap: want error for missing title, got nil")
	}
}

func TestResolveProjectMapAmbiguousTitle(t *testing.T) {
	projects := []multicaProject{
		{ID: "id-1", Title: "WebDeck"},
		{ID: "id-2", Title: "WebDeck"},
	}
	if _, err := resolveProjectMap(map[string]string{"x": "WebDeck"}, projects); err == nil {
		t.Fatal("resolveProjectMap: want error for ambiguous title, got nil")
	}
}

// ---------- bridge-admin-ui：多 token 验签配置 ----------

// tkSigningSecretB / tkSigningSecretC 是额外测试 token（base64("test-secret-b"/"test-secret-c")）。
var (
	tkSigningSecretB = "whsec_" + base64.StdEncoding.EncodeToString([]byte("test-secret-b"))
	tkSigningSecretC = "whsec_" + base64.StdEncoding.EncodeToString([]byte("test-secret-c"))
)

func TestParseSecretsMulti(t *testing.T) {
	got, err := parseSecrets(" " + tkSigningSecret + " , " + tkSigningSecretB + ",, " + tkSigningSecretC + " ")
	if err != nil {
		t.Fatalf("parseSecrets: %v", err)
	}
	want := []string{tkSigningSecret, tkSigningSecretB, tkSigningSecretC}
	if len(got) != len(want) {
		t.Fatalf("parseSecrets = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("secrets[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestParseSecretsEmpty(t *testing.T) {
	if got, err := parseSecrets(" , "); err != nil || len(got) != 0 {
		t.Fatalf("parseSecrets(empty) = %v, %v; want [], nil", got, err)
	}
}

func TestParseSecretsBadPrefix(t *testing.T) {
	if _, err := parseSecrets("whsec_AAA,plain-token"); err == nil {
		t.Fatal("parseSecrets: want error for token without whsec_ prefix, got nil")
	}
}

func TestParseSecretsBadBase64(t *testing.T) {
	if _, err := parseSecrets("whsec_!!!not-base64!!!"); err == nil {
		t.Fatal("parseSecrets: want error for bad base64 payload, got nil")
	}
}

// SECRETS 多值 → 全部解析。
func TestLoadConfigSecretsList(t *testing.T) {
	clearEnv(t)
	setRequired(t)
	t.Setenv("SPECWIRE_WEBHOOK_SECRETS", tkSigningSecret+","+tkSigningSecretB)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	want := []string{tkSigningSecret, tkSigningSecretB}
	if len(cfg.WebhookSecrets) != 2 || cfg.WebhookSecrets[0] != want[0] || cfg.WebhookSecrets[1] != want[1] {
		t.Errorf("secrets = %v, want %v", cfg.WebhookSecrets, want)
	}
}

// 旧单值 SECRET 兼容 → 单元素列表。
func TestLoadConfigSecretsLegacyCompat(t *testing.T) {
	clearEnv(t)
	setRequired(t)
	t.Setenv("SPECWIRE_WEBHOOK_SECRETS", "")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.WebhookSecrets) != 1 || cfg.WebhookSecrets[0] != tkSigningSecret {
		t.Errorf("secrets = %v, want [%s]", cfg.WebhookSecrets, tkSigningSecret)
	}
}

// SECRETS 与 SECRET 同时配置 → 并入（去重）。
func TestLoadConfigSecretsMergeLegacy(t *testing.T) {
	clearEnv(t)
	setRequired(t)
	t.Setenv("SPECWIRE_WEBHOOK_SECRETS", tkSigningSecretB)
	t.Setenv("SPECWIRE_WEBHOOK_SECRET", tkSigningSecret)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	want := []string{tkSigningSecretB, tkSigningSecret}
	if len(cfg.WebhookSecrets) != 2 || cfg.WebhookSecrets[0] != want[0] || cfg.WebhookSecrets[1] != want[1] {
		t.Errorf("secrets = %v, want %v", cfg.WebhookSecrets, want)
	}
}

// SECRETS 中已含 SECRET 值 → 不重复并入。
func TestLoadConfigSecretsMergeDedupe(t *testing.T) {
	clearEnv(t)
	setRequired(t)
	t.Setenv("SPECWIRE_WEBHOOK_SECRETS", tkSigningSecret+","+tkSigningSecretB)
	t.Setenv("SPECWIRE_WEBHOOK_SECRET", tkSigningSecret)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.WebhookSecrets) != 2 {
		t.Errorf("secrets = %v, want 2 entries (dedupe)", cfg.WebhookSecrets)
	}
}

// 两个配置都缺失 → 启动失败。
func TestLoadConfigSecretsEmptyFails(t *testing.T) {
	clearEnv(t)
	setRequired(t)
	t.Setenv("SPECWIRE_WEBHOOK_SECRET", "")
	t.Setenv("SPECWIRE_WEBHOOK_SECRETS", "")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig: want error for empty secrets, got nil")
	}
}

// 列表中存在非法项 → 启动失败。
func TestLoadConfigSecretsInvalidListFails(t *testing.T) {
	clearEnv(t)
	setRequired(t)
	t.Setenv("SPECWIRE_WEBHOOK_SECRETS", tkSigningSecret+",not-a-secret")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig: want error for invalid list entry, got nil")
	}
}
