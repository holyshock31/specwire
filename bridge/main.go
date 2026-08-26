// SpecWire Bridge：GitLab Push Webhook → 判重 → Multica Backlog Issue。
// M2 骨架：配置 + SQLite + HTTP 服务框架；handler 为占位，M3 实现完整管线。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"
)

func main() {
	// 本地开发：从 ./.env 加载配置（已存在的环境变量优先；文件不存在则忽略）
	if err := loadDotEnv(".env"); err != nil {
		slog.Error("load .env failed", "error", err)
		os.Exit(1)
	}
	cfg, err := LoadConfig()
	if err != nil {
		slog.Error("config load failed", "error", err)
		os.Exit(1)
	}
	// SPECWIRE_PROJECT_MAP（可选）：GitLab path → Multica project 映射（D20）。
	// 配置了映射时必须覆盖 allowlist 全部项目；未配置时回退默认 project（旧行为）。
	if err := loadProjectMap(cfg); err != nil {
		slog.Error("project map load failed", "error", err)
		os.Exit(1)
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel})))

	legacyStore, err := OpenStore(cfg.DBPath)
	if err != nil {
		slog.Error("store open failed", "path", cfg.DBPath, "error", err)
		os.Exit(1)
	}
	defer legacyStore.Close()
	slog.Info("store ready", "path", cfg.DBPath)
	persistent, err := newPersistentApplication(cfg)
	if err != nil {
		slog.Error("persistent application load failed", "error", err)
		os.Exit(1)
	}
	defer persistent.store.Close()

	// 运行时配置指针：admin API copy-on-write 替换，webhook 侧原子读快照（无锁）。
	cfgPtr := &atomic.Pointer[Config]{}
	cfgPtr.Store(cfg)
	gitlab := newGitlabClient(cfg)
	admin := newAdminHandler(cfgPtr, gitlab, cfg.AdminToken,
		getenv("SPECWIRE_ADMIN_ENV_PATH", ".env"),
		filepath.Join(filepath.Dir(cfg.DBPath), "admin-state.json"))

	mux := http.NewServeMux()
	mux.Handle("/gitlab/specwire", persistent.ingress)
	mux.Handle("/api/v1/", persistent.api)
	mux.Handle("/admin/", admin)

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("bridge listening", "addr", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server failed", "error", err)
			stop()
		}
	}()
	go func() {
		if err := persistent.worker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("runtime worker stopped", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown failed", "error", err)
	}
}

// loadProjectMap 加载并解析 SPECWIRE_PROJECT_MAP（D20）：
//   - 未配置 → 使用默认 SPECWIRE_MULTICA_PROJECT_ID（旧行为），ProjectMap 为空。
//   - 已配置 → 调 multica project list 把 title 解析成 UUID，并校验 allowlist 全部项目都有映射。
func loadProjectMap(cfg *Config) error {
	raw := os.Getenv("SPECWIRE_PROJECT_MAP")
	if raw == "" {
		return nil
	}
	entries, err := parseProjectMap(raw)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.CLITimeout)
	defer cancel()
	projects, err := listProjects(ctx, cfg)
	if err != nil {
		return fmt.Errorf("list multica projects: %w", err)
	}
	mapped, err := resolveProjectMap(entries, projects)
	if err != nil {
		return err
	}
	for p := range cfg.AllowedProjects {
		if _, ok := mapped[p]; !ok {
			return fmt.Errorf("SPECWIRE_PROJECT_MAP: allowlist project %q has no mapping", p)
		}
	}
	cfg.ProjectMap = mapped
	slog.Info("project map loaded", "entries", len(mapped))
	return nil
}
