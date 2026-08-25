// SpecWire Bridge 判重存储：SQLite 唯一索引保证业务稳定键幂等。
// 状态机：processing（已认领、创建中）→ created（成功）| error（失败，可重试）。
package main

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

const (
	// StateProcessing 已认领但尚未创建成功；并发重复投递对此状态返回 Duplicate，
	// 由认领请求自身的响应（502 触发 GitLab 重试）保证最终一致。
	StateProcessing = "processing"
	// StateCreated 创建成功；重复投递返回 Duplicate。
	StateCreated = "created"
	// StateError 创建失败；允许同稳定键重试（覆盖旧记录）。
	StateError = "error"
)

// ClaimResult 是 Claim 的认领结果。
type ClaimResult int

const (
	// Claimed 本次调用负责创建 Multica Issue。
	Claimed ClaimResult = iota
	// Duplicate 稳定键已被成功处理（created）或正在处理中（processing），不得再创建。
	Duplicate
)

const schema = `
CREATE TABLE IF NOT EXISTS events (
  stable_key          TEXT PRIMARY KEY,
  delivery_key        TEXT NOT NULL,
  state               TEXT NOT NULL,
  multica_issue_id    TEXT,
  multica_project_id  TEXT,
  project_path        TEXT NOT NULL,
  change_id           TEXT NOT NULL,
  after_sha           TEXT NOT NULL,
  created_at          TEXT NOT NULL,
  last_error          TEXT
);

CREATE TABLE IF NOT EXISTS issue_links (
  gitlab_project   TEXT NOT NULL,
  issue_iid        INTEGER NOT NULL,
  change_id        TEXT NOT NULL,
  branch           TEXT NOT NULL,
  branch_head_sha  TEXT,
  created_at       TEXT NOT NULL,
  PRIMARY KEY (gitlab_project, issue_iid)
);`

// IssueLink 是 GitLab 发布 Issue 与 change 的关联记录（v2 发布模型）。
type IssueLink struct {
	GitlabProject string
	IssueIID      int
	ChangeID      string
	Branch        string
	BranchHeadSHA string
}

// InsertIssueLink 记录发布 Issue 关联（幂等：主键冲突忽略）。
func (s *Store) InsertIssueLink(gitlabProject string, issueIID int, changeID, branch, branchHeadSHA string) error {
	if _, err := s.db.Exec(
		`INSERT OR IGNORE INTO issue_links
		   (gitlab_project, issue_iid, change_id, branch, branch_head_sha, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		gitlabProject, issueIID, changeID, branch, branchHeadSHA,
		time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return fmt.Errorf("insert issue link: %w", err)
	}
	return nil
}

// ListIssueLinks 返回指定项目+change 的全部关联记录（v2 归档关闭 Issue 用）。
func (s *Store) ListIssueLinks(gitlabProject, changeID string) ([]IssueLink, error) {
	rows, err := s.db.Query(
		`SELECT gitlab_project, issue_iid, change_id, branch, branch_head_sha
		 FROM issue_links WHERE gitlab_project = ? AND change_id = ?`,
		gitlabProject, changeID,
	)
	if err != nil {
		return nil, fmt.Errorf("query issue links: %w", err)
	}
	defer rows.Close()
	var out []IssueLink
	for rows.Next() {
		var l IssueLink
		if err := rows.Scan(&l.GitlabProject, &l.IssueIID, &l.ChangeID, &l.Branch, &l.BranchHeadSHA); err != nil {
			return nil, fmt.Errorf("scan issue link: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Store 封装 SQLite 判重存储。
type Store struct {
	db *sql.DB
}

// OpenStore 打开（必要时创建）SQLite 数据库并初始化 schema，
// 并对旧库执行迁移（multica_project_id 列，D21 归属漂移检测需要）。
func OpenStore(path string) (*Store, error) {
	// DSN pragma 对连接池中每个新连接生效（Exec 方式只作用于单个连接，并发写会 SQLITE_BUSY）。
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	// 迁移：旧库没有 multica_project_id 列（D21 之前建的表）
	rows, err := db.Query(`PRAGMA table_info(events)`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("inspect schema: %w", err)
	}
	hasProjectCol := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			db.Close()
			return nil, fmt.Errorf("scan schema: %w", err)
		}
		if name == "multica_project_id" {
			hasProjectCol = true
		}
	}
	rows.Close()
	if !hasProjectCol {
		if _, err := db.Exec(`ALTER TABLE events ADD COLUMN multica_project_id TEXT`); err != nil {
			db.Close()
			return nil, fmt.Errorf("migrate multica_project_id: %w", err)
		}
	}
	return &Store{db: db}, nil
}

// Close 关闭数据库。
func (s *Store) Close() error { return s.db.Close() }

// Claim 尝试认领业务稳定键。并发安全：INSERT OR IGNORE + PRIMARY KEY 唯一约束，
// 多个并发请求中恰好一个成功插入（返回 Claimed），其余落入状态分支。
//
// 状态分支：
//   - created / processing → Duplicate（不创建）；
//   - error → 允许重试，重置为 processing 并返回 Claimed。
func (s *Store) Claim(stableKey, deliveryKey, projectPath, changeID, afterSHA string) (ClaimResult, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`INSERT OR IGNORE INTO events
		   (stable_key, delivery_key, state, project_path, change_id, after_sha, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		stableKey, deliveryKey, StateProcessing, projectPath, changeID, afterSHA,
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return 0, fmt.Errorf("insert: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 1 {
		if err := tx.Commit(); err != nil {
			return 0, fmt.Errorf("commit: %w", err)
		}
		return Claimed, nil
	}

	var state string
	if err := tx.QueryRow(`SELECT state FROM events WHERE stable_key = ?`, stableKey).Scan(&state); err != nil {
		return 0, fmt.Errorf("query state: %w", err)
	}

	switch state {
	case StateCreated, StateProcessing:
		if err := tx.Commit(); err != nil {
			return 0, fmt.Errorf("commit: %w", err)
		}
		return Duplicate, nil
	case StateError:
		if _, err := tx.Exec(
			`UPDATE events SET state = ?, delivery_key = ?, last_error = NULL WHERE stable_key = ?`,
			StateProcessing, deliveryKey, stableKey,
		); err != nil {
			return 0, fmt.Errorf("reset for retry: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return 0, fmt.Errorf("commit: %w", err)
		}
		return Claimed, nil
	default:
		return 0, fmt.Errorf("unexpected state %q for %s", state, stableKey)
	}
}

// MarkCreated 记录创建成功后的 Multica Issue 及其归属 project（D21 审计用）。
func (s *Store) MarkCreated(stableKey, issueID, projectID string) error {
	if _, err := s.db.Exec(
		`UPDATE events SET state = ?, multica_issue_id = ?, multica_project_id = ? WHERE stable_key = ?`,
		StateCreated, issueID, projectID, stableKey,
	); err != nil {
		return fmt.Errorf("mark created %s: %w", stableKey, err)
	}
	return nil
}

// MarkError 记录创建失败（可重试）。
func (s *Store) MarkError(stableKey, errMsg string) error {
	if len(errMsg) > 500 {
		errMsg = errMsg[:500]
	}
	if _, err := s.db.Exec(
		`UPDATE events SET state = ?, last_error = ? WHERE stable_key = ?`,
		StateError, errMsg, stableKey,
	); err != nil {
		return fmt.Errorf("mark error %s: %w", stableKey, err)
	}
	return nil
}

// LatestCreatedIssue 返回指定项目+change 下最新创建且 state=created 的 Multica Issue ID；
// 无匹配返回空字符串。
// 用于 archived 事件按 project+change_id（稳定键去掉 SHA 部分）匹配实现卡（D17）：
// 归档 push 的 after_sha 与建卡时不同，稳定键无法精确匹配，只能按 project+change_id 模糊匹配。
// ORDER BY rowid：SQLite rowid 单调递增，语义等同插入顺序（created_at 为秒级精度，同秒插入时不稳定）。
func (s *Store) LatestCreatedIssue(projectPath, changeID string) (string, error) {
	var id string
	err := s.db.QueryRow(
		`SELECT multica_issue_id FROM events
		 WHERE project_path = ? AND change_id = ? AND state = ?
		 ORDER BY rowid DESC LIMIT 1`,
		projectPath, changeID, StateCreated,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("query latest created issue: %w", err)
	}
	return id, nil
}
