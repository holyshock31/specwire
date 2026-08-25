package main

import (
	"fmt"
	"sync"
	"testing"
)

const (
	tkProject = "specwire/specwire-poc"
	tkChange  = "add-user-login"
	tkSHA     = "494dd55ad996a61a486c206041a25d039e137966"
)

func testKey(sha string) string { return tkProject + ":" + tkChange + ":" + sha }

// newTestStore 在临时目录创建独立数据库。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestClaimFirstWins(t *testing.T) {
	s := newTestStore(t)

	r, err := s.Claim(testKey(tkSHA), "delivery-1", tkProject, tkChange, tkSHA)
	if err != nil {
		t.Fatalf("first Claim: %v", err)
	}
	if r != Claimed {
		t.Fatalf("first Claim: want Claimed, got %v", r)
	}

	r, err = s.Claim(testKey(tkSHA), "delivery-2", tkProject, tkChange, tkSHA)
	if err != nil {
		t.Fatalf("second Claim: %v", err)
	}
	if r != Duplicate {
		t.Fatalf("second Claim: want Duplicate, got %v", r)
	}
}

func TestClaimAfterCreatedIsDuplicate(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.Claim(testKey(tkSHA), "d1", tkProject, tkChange, tkSHA); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkCreated(testKey(tkSHA), "WW1-9", "proj-a"); err != nil {
		t.Fatalf("MarkCreated: %v", err)
	}

	r, err := s.Claim(testKey(tkSHA), "d2", tkProject, tkChange, tkSHA)
	if err != nil {
		t.Fatalf("Claim after created: %v", err)
	}
	if r != Duplicate {
		t.Fatalf("Claim after created: want Duplicate, got %v", r)
	}
}

func TestClaimAfterErrorRetries(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.Claim(testKey(tkSHA), "d1", tkProject, tkChange, tkSHA); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkError(testKey(tkSHA), "multica CLI exit 1"); err != nil {
		t.Fatalf("MarkError: %v", err)
	}

	// error 状态允许重试：重新认领成功
	r, err := s.Claim(testKey(tkSHA), "d2", tkProject, tkChange, tkSHA)
	if err != nil {
		t.Fatalf("Claim after error: %v", err)
	}
	if r != Claimed {
		t.Fatalf("Claim after error: want Claimed, got %v", r)
	}

	// 重试成功后再次投递 → Duplicate
	if err := s.MarkCreated(testKey(tkSHA), "WW1-9", "proj-a"); err != nil {
		t.Fatal(err)
	}
	r, err = s.Claim(testKey(tkSHA), "d3", tkProject, tkChange, tkSHA)
	if err != nil {
		t.Fatal(err)
	}
	if r != Duplicate {
		t.Fatalf("Claim after retry+created: want Duplicate, got %v", r)
	}
}

func TestClaimDifferentKeys(t *testing.T) {
	s := newTestStore(t)

	for _, sha := range []string{"sha-aaa", "sha-bbb", "sha-ccc"} {
		r, err := s.Claim(testKey(sha), "d-"+sha, tkProject, tkChange, sha)
		if err != nil {
			t.Fatalf("Claim %s: %v", sha, err)
		}
		if r != Claimed {
			t.Fatalf("Claim %s: want Claimed, got %v", sha, r)
		}
	}
}

func TestMarkErrorRecordsMessage(t *testing.T) {
	s := newTestStore(t)
	key := testKey(tkSHA)

	if _, err := s.Claim(key, "d1", tkProject, tkChange, tkSHA); err != nil {
		t.Fatal(err)
	}
	longErr := fmt.Sprintf("boom: %s", string(make([]byte, 0)))
	if err := s.MarkError(key, longErr+"1234567890"); err != nil {
		t.Fatalf("MarkError: %v", err)
	}

	var state, lastErr string
	if err := s.db.QueryRow(`SELECT state, last_error FROM events WHERE stable_key = ?`, key).Scan(&state, &lastErr); err != nil {
		t.Fatalf("query: %v", err)
	}
	if state != StateError {
		t.Errorf("state = %q, want %q", state, StateError)
	}
	if lastErr != "boom: 1234567890" {
		t.Errorf("last_error = %q, want %q", lastErr, "boom: 1234567890")
	}
}

func TestMarkErrorTruncatesMessage(t *testing.T) {
	s := newTestStore(t)
	key := testKey(tkSHA)

	if _, err := s.Claim(key, "d1", tkProject, tkChange, tkSHA); err != nil {
		t.Fatal(err)
	}
	big := string(make([]byte, 2000))
	for i := range big {
		big = big[:i] + "x" + big[i+1:]
	}
	if err := s.MarkError(key, big); err != nil {
		t.Fatalf("MarkError: %v", err)
	}

	var lastErr string
	if err := s.db.QueryRow(`SELECT last_error FROM events WHERE stable_key = ?`, key).Scan(&lastErr); err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(lastErr) != 500 {
		t.Errorf("last_error length = %d, want 500", len(lastErr))
	}
}

// TestConcurrentClaimSameKey 验证并发下同一个稳定键恰好被认领一次。
func TestConcurrentClaimSameKey(t *testing.T) {
	s := newTestStore(t)

	const n = 10
	results := make([]ClaimResult, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = s.Claim(testKey(tkSHA), fmt.Sprintf("d%d", i), tkProject, tkChange, tkSHA)
		}(i)
	}
	wg.Wait()

	claimed := 0
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("Claim #%d: %v", i, errs[i])
		}
		if results[i] == Claimed {
			claimed++
		}
	}
	if claimed != 1 {
		t.Fatalf("concurrent Claims: want exactly 1 Claimed, got %d", claimed)
	}
}

// TestConcurrentDistinctKeys 验证不同稳定键并发认领全部成功。
func TestConcurrentDistinctKeys(t *testing.T) {
	s := newTestStore(t)

	const n = 10
	results := make([]ClaimResult, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sha := fmt.Sprintf("sha-%d", i)
			results[i], errs[i] = s.Claim(testKey(sha), fmt.Sprintf("d%d", i), tkProject, tkChange, sha)
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("Claim #%d: %v", i, errs[i])
		}
		if results[i] != Claimed {
			t.Fatalf("Claim #%d: want Claimed, got %v", i, results[i])
		}
	}
}

// TestLatestCreatedIssue 验证 archived 的匹配规则（D17）：
// 按 project+change_id（不含 SHA）取最新 created 卡；error 状态不算；无匹配返回空。
func TestLatestCreatedIssue(t *testing.T) {
	s := newTestStore(t)

	// 旧修订版本（先建）
	if _, err := s.Claim(testKey("sha-revision-1"), "d1", tkProject, tkChange, "sha-revision-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkCreated(testKey("sha-revision-1"), "issue-revision-1", "proj-a"); err != nil {
		t.Fatal(err)
	}
	// 新修订版本（后建，应被匹配）
	if _, err := s.Claim(testKey("sha-revision-2"), "d2", tkProject, tkChange, "sha-revision-2"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkCreated(testKey("sha-revision-2"), "issue-revision-2", "proj-a"); err != nil {
		t.Fatal(err)
	}
	// error 状态不参与匹配
	if _, err := s.Claim(testKey("sha-revision-3"), "d3", tkProject, tkChange, "sha-revision-3"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkError(testKey("sha-revision-3"), "boom"); err != nil {
		t.Fatal(err)
	}

	id, err := s.LatestCreatedIssue(tkProject, tkChange)
	if err != nil {
		t.Fatalf("LatestCreatedIssue: %v", err)
	}
	if id != "issue-revision-2" {
		t.Errorf("latest = %q, want issue-revision-2 (latest created)", id)
	}

	// 无匹配 change
	id, err = s.LatestCreatedIssue(tkProject, "never-existed")
	if err != nil {
		t.Fatalf("LatestCreatedIssue(no match): %v", err)
	}
	if id != "" {
		t.Errorf("no-match latest = %q, want empty", id)
	}

	// 不同项目隔离
	id, err = s.LatestCreatedIssue("other/repo", tkChange)
	if err != nil {
		t.Fatalf("LatestCreatedIssue(other project): %v", err)
	}
	if id != "" {
		t.Errorf("other-project latest = %q, want empty", id)
	}
}

// ---------- v2：issue_links 关联表 ----------

func TestInsertAndListIssueLinks(t *testing.T) {
	s := newTestStore(t)

	if err := s.InsertIssueLink("personal/webdeck", 7, "change-a", "feat/change-a", "sha-a"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// 幂等：同主键重复插入不报错不新增
	if err := s.InsertIssueLink("personal/webdeck", 7, "change-a", "feat/change-a", "sha-a"); err != nil {
		t.Fatalf("insert dup: %v", err)
	}
	if err := s.InsertIssueLink("personal/webdeck", 8, "change-b", "feat/change-b", "sha-b"); err != nil {
		t.Fatalf("insert b: %v", err)
	}

	links, err := s.ListIssueLinks("personal/webdeck", "change-a")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(links) != 1 || links[0].IssueIID != 7 || links[0].Branch != "feat/change-a" || links[0].BranchHeadSHA != "sha-a" {
		t.Errorf("links = %+v, want single change-a link", links)
	}

	// 按 change 过滤
	links, err = s.ListIssueLinks("personal/webdeck", "change-b")
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].IssueIID != 8 {
		t.Errorf("links = %+v, want single change-b link", links)
	}

	// 跨项目隔离
	links, err = s.ListIssueLinks("specwire/specwire-poc", "change-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 0 {
		t.Errorf("cross-project links = %+v, want none", links)
	}
}
