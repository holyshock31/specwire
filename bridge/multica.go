// SpecWire Bridge → Multica CLI 桥接：os/exec 参数数组，禁止 shell 字符串拼接（设计 D8 / §5.7）。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// multicaCreateOutput 是 `multica issue create --output json` 输出的最小字段。
// 注意：真实 CLI 输出结构尚未联调验证（SPECWIRE_BRIDGE_TEST_CASES.md §4），
// M4 联调时如字段名不同需在此调整。
type multicaCreateOutput struct {
	ID string `json:"id"`
}

// multicaProject 是 `multica project list --output json` 的最小字段。
type multicaProject struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// listProjects 调用 multica CLI 列出 workspace 下的项目（只读），用于 PROJECT_MAP 的 title→id 解析（D20）。
func listProjects(ctx context.Context, cfg *Config) ([]multicaProject, error) {
	args := []string{
		"--profile", cfg.MulticaProfile,
		"project", "list",
		"--output", "json",
	}
	// The legacy map resolver runs before the persistent Multica endpoint is
	// loaded. Honor the explicitly configured server when present so a
	// one-time import uses the same endpoint inside or outside Docker; keep
	// profile resolution as the fallback for older local deployments.
	if serverURL := strings.TrimSpace(os.Getenv("MULTICA_SERVER_URL")); serverURL != "" {
		args = []string{
			"--profile", cfg.MulticaProfile,
			"--server-url", serverURL,
			"project", "list",
			"--output", "json",
		}
	}
	cmd := exec.Command("multica", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start multica cli: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			return nil, fmt.Errorf("multica cli failed: %w; stderr: %s", err, truncateStr(stderr.String(), 500))
		}
	case <-ctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
		return nil, fmt.Errorf("multica cli timed out after %s", cfg.CLITimeout)
	}

	var out []multicaProject
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return nil, fmt.Errorf("parse multica project list: %w; stdout: %s", err, truncateStr(stdout.String(), 500))
	}
	return out, nil
}

// createBacklogIssue 调用 multica CLI 在指定 project 创建 Issue，返回 issue ID。
// status 默认 backlog；assignee 非空时预分配（D23：发布者按次指定）。
// projectID 由 ProjectMap 解析或默认配置决定（D20）。
// 参数全部通过 argv 数组传递，webhook 字段绝不拼进 shell 字符串。
//
// 超时处理：Setpgid 使 CLI 成为独立进程组组长，超时时 kill 整个进程组。
// 不能只 kill 直接子进程——若 CLI 派生的孙进程持有 stdout/stderr 管道写端，
// cmd.Wait 会一直被 copy goroutine 阻塞到孙进程退出（Go exec 经典陷阱，
// 见 M3 调试记录：fake 脚本的 sleep 把超时从 1s 拖到 5s）。
func createBacklogIssue(ctx context.Context, cfg *Config, projectID, changeID, description, status, assignee string) (string, error) {
	if status == "" {
		status = "backlog" // 发布者未指定时默认评审模式
	}
	args := []string{
		"--profile", cfg.MulticaProfile,
		"issue", "create",
		"--project", projectID,
		"--status", status,
		"--title", "[SpecWire] " + changeID,
		"--description-stdin",
		"--output", "json",
	}
	if assignee != "" {
		args = append(args, "--assignee", assignee)
	}
	cmd := exec.Command("multica", args...)
	cmd.Stdin = strings.NewReader(description)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start multica cli: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var err error
	select {
	case err = <-done:
		// CLI 正常结束
	case <-ctx.Done():
		// 超时：杀整个进程组（含全部后代），再等待 Wait 收尾，避免 goroutine 泄漏
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
		return "", fmt.Errorf("multica cli timed out after %s", cfg.CLITimeout)
	}

	if err != nil {
		return "", fmt.Errorf("multica cli failed: %w; stderr: %s", err, truncateStr(stderr.String(), 500))
	}

	var out multicaCreateOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return "", fmt.Errorf("parse multica output: %w; stdout: %s", err, truncateStr(stdout.String(), 500))
	}
	if out.ID == "" {
		return "", fmt.Errorf("multica output missing issue id; stdout: %s", truncateStr(stdout.String(), 500))
	}
	return out.ID, nil
}

// updateIssueStatus 调用 multica CLI 更新 Issue 状态（如 done）。
// 复用 createBacklogIssue 的进程组超时处理；不解析 stdout（status 命令输出非结构化）。
func updateIssueStatus(ctx context.Context, cfg *Config, issueID, status string) error {
	args := []string{
		"--profile", cfg.MulticaProfile,
		"issue", "status",
		issueID,
		status,
	}
	cmd := exec.Command("multica", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start multica cli: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("multica cli failed: %w; stderr: %s", err, truncateStr(stderr.String(), 500))
		}
		return nil
	case <-ctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
		return fmt.Errorf("multica cli timed out after %s", cfg.CLITimeout)
	}
}

// truncateStr 截断错误输出，防止把大段输出写进日志/数据库。
func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
