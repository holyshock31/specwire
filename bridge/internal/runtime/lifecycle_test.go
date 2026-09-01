package runtime

import (
	"strings"
	"testing"
)

func TestAcceptedLifecycleTrailersRespectRefAndReason(t *testing.T) {
	tests := []struct {
		name      string
		ref       string
		after     string
		message   string
		event     string
		reason    string
		wantCount int
	}{
		{
			name:      "archive on main",
			ref:       "refs/heads/main",
			after:     "abc123",
			message:   "SpecWire-Event: archived\nSpecWire-Change: CHG-1",
			event:     "archived",
			wantCount: 1,
		},
		{
			name:      "legacy abandoned push is ignored",
			ref:       "refs/heads/change/feat-CHG-2",
			after:     "def456",
			message:   "SpecWire-Event: abandoned\nSpecWire-Status: abandoned\nSpecWire-Change: CHG-2\nSpecWire-Reason: change has no actual content",
			wantCount: 0,
		},
		{
			name:      "legacy abandoned fix push is ignored",
			ref:       "refs/heads/change/fix-CHG-3",
			after:     "ghi789",
			message:   "SpecWire-Event: abandoned\nSpecWire-Status: abandoned\nSpecWire-Change: CHG-3\nSpecWire-Reason: obsolete",
			wantCount: 0,
		},
		{
			name:      "archive cannot come from change branch",
			ref:       "refs/heads/change/feat-CHG-4",
			after:     "jkl012",
			message:   "SpecWire-Event: archived\nSpecWire-Change: CHG-4",
			wantCount: 0,
		},
		{
			name:      "abandon requires a reason",
			ref:       "refs/heads/change/feat-CHG-5",
			after:     "mno345",
			message:   "SpecWire-Event: abandoned\nSpecWire-Change: CHG-5",
			wantCount: 0,
		},
		{
			name:      "abandon requires abandoned status",
			ref:       "refs/heads/change/feat-CHG-STATUS",
			after:     "status123",
			message:   "SpecWire-Event: abandoned\nSpecWire-Change: CHG-STATUS\nSpecWire-Reason: obsolete",
			wantCount: 0,
		},
		{
			name:      "abandon requires exact change branch",
			ref:       "refs/heads/feature/CHG-6",
			after:     "pqr678",
			message:   "SpecWire-Event: abandoned\nSpecWire-Change: CHG-6\nSpecWire-Reason: obsolete",
			wantCount: 0,
		},
		{
			name:      "zero commit is ignored",
			ref:       "refs/heads/main",
			after:     "0000000000000000000000000000000000000000",
			message:   "SpecWire-Event: archived\nSpecWire-Change: CHG-7",
			wantCount: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := map[string]any{
				"ref":   tt.ref,
				"after": tt.after,
				"head_commit": map[string]any{
					"message": tt.message,
				},
			}
			trailers := acceptedLifecycleTrailers(payload)
			if len(trailers) != tt.wantCount {
				t.Fatalf("accepted trailers = %+v, want %d", trailers, tt.wantCount)
			}
			if tt.wantCount == 1 {
				if trailers[0].Event != tt.event || trailers[0].Reason != tt.reason {
					t.Fatalf("trailer = %+v, want event=%q reason=%q", trailers[0], tt.event, tt.reason)
				}
			}
		})
	}
}

func TestAbandonedReasonIsBounded(t *testing.T) {
	tooLong := strings.Repeat("x", maxLifecycleReasonRunes+1)
	trailer := "SpecWire-Event: abandoned\nSpecWire-Change: CHG-LONG\nSpecWire-Reason: " + tooLong
	if _, ok := parseLifecycleTrailer(trailer); ok {
		t.Fatal("an abandoned reason over the limit must be rejected")
	}
	if _, ok := parseLifecycleTrailer("SpecWire-Event: abandoned\nSpecWire-Change: CHG-NEWLINE\nSpecWire-Reason: line\rnext"); ok {
		t.Fatal("an abandoned reason containing a carriage return must be rejected")
	}
	if _, ok := parseLifecycleTrailer("SpecWire-Event: abandoned\nSpecWire-Status: cancelled\nSpecWire-Change: CHG-STATUS\nSpecWire-Reason: obsolete"); ok {
		t.Fatal("an abandoned event with a conflicting status must be rejected")
	}
}

func TestIssueAbandonActionIdentity(t *testing.T) {
	abandon := map[string]any{"change_id": "CHG-1", "lifecycle_event": "abandoned"}
	if got := actionIdentity("Issue Hook", "delivery", abandon); got != "lifecycle:abandoned:CHG-1" {
		t.Fatalf("action identity = %q", got)
	}
	if got := actionDeliveryID(GitLabEnvelope{EventName: "Issue Hook", DeliveryID: "delivery"}, abandon); got != "delivery:abandoned:CHG-1" {
		t.Fatalf("action delivery ID = %q", got)
	}
}

func TestAbandonIssueRequiresControlledLabelTransition(t *testing.T) {
	base := func(action string, labels any, changes any) GitLabEnvelope {
		payload := map[string]any{
			"object_kind": "issue",
			"object_attributes": map[string]any{
				"action":      action,
				"description": "change_id: CHG-LABEL\n",
				"labels":      labels,
			},
			"project": map[string]any{"id": 101},
		}
		if changes != nil {
			payload["changes"] = changes
		}
		return GitLabEnvelope{EventName: "Issue Hook", Payload: payload}
	}
	transition := map[string]any{"labels": map[string]any{
		"previous": []any{map[string]any{"title": "change"}},
		"current":  []any{map[string]any{"title": "change"}, map[string]any{"title": abandonLabel}},
	}}
	for _, test := range []struct {
		name     string
		envelope GitLabEnvelope
		want     bool
	}{
		{"new controlled label", base("update", []any{map[string]any{"title": "change"}, map[string]any{"title": abandonLabel}}, transition), true},
		{"label already present", base("update", []any{map[string]any{"title": abandonLabel}}, map[string]any{"labels": map[string]any{
			"previous": []any{map[string]any{"title": abandonLabel}},
			"current":  []any{map[string]any{"title": abandonLabel}},
		}}), false},
		{"label present without label diff", base("update", []any{map[string]any{"title": abandonLabel}}, nil), false},
		{"Bridge description follow-up", base("update", []any{map[string]any{"title": abandonLabel}}, map[string]any{
			"description": map[string]any{"previous": "old", "current": "new"},
		}), false},
		{"unrelated update", base("update", []any{map[string]any{"title": "change"}}, map[string]any{"description": map[string]any{}}), false},
		{"wrong action", base("open", []any{map[string]any{"title": abandonLabel}}, transition), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := matchesAbandonIssue(test.envelope); got != test.want {
				t.Fatalf("matchesAbandonIssue = %v, want %v", got, test.want)
			}
		})
	}
}
