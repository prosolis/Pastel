package database

import (
	"path/filepath"
	"testing"
)

// saveTestDeal inserts a minimal deal row for reaction tests.
func saveTestDeal(t *testing.T, db *DB, dedupID, title string) {
	t.Helper()
	if err := db.SaveDeal(Deal{
		DedupID:   dedupID,
		Source:    "test",
		Category:  "games",
		Kind:      "game",
		Title:     title,
		TitleNorm: title,
	}); err != nil {
		t.Fatalf("save deal %q: %v", dedupID, err)
	}
}

// reactionCount reads the persisted reaction_count for a deal.
func reactionCount(t *testing.T, db *DB, dedupID string) int {
	t.Helper()
	var n int
	if err := db.db.Get(&n, "SELECT reaction_count FROM deals WHERE dedup_id = ?", dedupID); err != nil {
		t.Fatalf("read reaction_count: %v", err)
	}
	return n
}

func TestReactions(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	saveTestDeal(t, db, "deal-1", "hollow knight")
	if err := db.SetDealEventID("deal-1", "$evt1"); err != nil {
		t.Fatalf("set event id: %v", err)
	}

	// A reaction to an unknown event is ignored and reports not-a-deal.
	if isDeal, err := db.AddReaction("$nope", "@a:x", "$r0"); err != nil || isDeal {
		t.Fatalf("reaction to non-deal: isDeal=%v err=%v", isDeal, err)
	}

	// First reaction from user A counts once.
	if isDeal, err := db.AddReaction("$evt1", "@a:x", "$rA"); err != nil || !isDeal {
		t.Fatalf("add A: isDeal=%v err=%v", isDeal, err)
	}
	if got := reactionCount(t, db, "deal-1"); got != 1 {
		t.Fatalf("after A: count = %d, want 1", got)
	}

	// Re-delivering the SAME reaction event (sync replay) is idempotent.
	if _, err := db.AddReaction("$evt1", "@a:x", "$rA"); err != nil {
		t.Fatalf("replay A: %v", err)
	}
	if got := reactionCount(t, db, "deal-1"); got != 1 {
		t.Fatalf("after replay A: count = %d, want 1", got)
	}

	// A SECOND emoji from user A (different reaction event) still counts A once
	// — reaction_count is distinct users, not raw reactions.
	if _, err := db.AddReaction("$evt1", "@a:x", "$rA2"); err != nil {
		t.Fatalf("add A second emoji: %v", err)
	}
	if got := reactionCount(t, db, "deal-1"); got != 1 {
		t.Fatalf("after A second emoji: count = %d, want 1", got)
	}

	// User B reacts → two distinct users.
	if _, err := db.AddReaction("$evt1", "@b:x", "$rB"); err != nil {
		t.Fatalf("add B: %v", err)
	}
	if got := reactionCount(t, db, "deal-1"); got != 2 {
		t.Fatalf("after B: count = %d, want 2", got)
	}

	// B redacts one of their reactions → back to one distinct user.
	if err := db.RemoveReaction("$rB"); err != nil {
		t.Fatalf("remove B: %v", err)
	}
	if got := reactionCount(t, db, "deal-1"); got != 1 {
		t.Fatalf("after removing B: count = %d, want 1", got)
	}

	// Redacting an untracked event is a harmless no-op.
	if err := db.RemoveReaction("$unknown"); err != nil {
		t.Fatalf("remove unknown: %v", err)
	}
	if got := reactionCount(t, db, "deal-1"); got != 1 {
		t.Fatalf("after remove unknown: count = %d, want 1", got)
	}
}

func TestTopDealsSinceAndHotSort(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	// Three deals; give them differing reaction counts via the public path.
	for i, c := range []struct {
		id, title string
		users     []string
	}{
		{"d-cold", "cold deal", nil},
		{"d-warm", "warm deal", []string{"@a:x"}},
		{"d-hot", "hot deal", []string{"@a:x", "@b:x", "@c:x"}},
	} {
		saveTestDeal(t, db, c.id, c.title)
		ev := "$ev" + string(rune('0'+i))
		if err := db.SetDealEventID(c.id, ev); err != nil {
			t.Fatalf("set event id: %v", err)
		}
		for j, u := range c.users {
			if _, err := db.AddReaction(ev, u, ev+"-r"+string(rune('0'+j))); err != nil {
				t.Fatalf("add reaction: %v", err)
			}
		}
	}

	// TopDealsSince returns only reacted deals, hottest first.
	top, err := db.TopDealsSince(7, 5)
	if err != nil {
		t.Fatalf("top deals: %v", err)
	}
	if len(top) != 2 {
		t.Fatalf("top len = %d, want 2 (cold deal has no reactions)", len(top))
	}
	if top[0].DedupID != "d-hot" || top[1].DedupID != "d-warm" {
		t.Fatalf("top order = [%s, %s], want [d-hot, d-warm]", top[0].DedupID, top[1].DedupID)
	}
	if top[0].ReactionCount != 3 {
		t.Fatalf("hot reaction count = %d, want 3", top[0].ReactionCount)
	}

	// The "hot" sort ranks the reacted deals above the cold one.
	deals, _, err := db.QueryDeals(DealFilter{Sort: "hot"})
	if err != nil {
		t.Fatalf("query hot: %v", err)
	}
	if len(deals) != 3 || deals[0].DedupID != "d-hot" || deals[1].DedupID != "d-warm" {
		t.Fatalf("hot sort order wrong: got %d deals, first=%v", len(deals), deals)
	}
}
