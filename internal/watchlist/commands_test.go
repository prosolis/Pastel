package watchlist

import (
	"strings"
	"testing"

	"maunium.net/go/mautrix/id"

	"github.com/prosolis/Pastel/internal/currency"
)

// fakeSender records DMs instead of sending them.
type fakeSender struct{ msgs []string }

func (f *fakeSender) SendDM(_ id.UserID, text string) error {
	f.msgs = append(f.msgs, text)
	return nil
}

func (f *fakeSender) last() string {
	if len(f.msgs) == 0 {
		return ""
	}
	return f.msgs[len(f.msgs)-1]
}

func newTestHandler(t *testing.T) (*CommandHandler, *fakeSender, *Store) {
	t.Helper()
	store := newTestStore(t)
	sender := &fakeSender{}
	h := NewCommandHandler(store, sender, currency.NewConverter())
	return h, sender, store
}

func TestHandleWatchParsesPredicates(t *testing.T) {
	h, sender, store := newTestHandler(t)

	h.HandleMessage("@u", "!watch laptop under 500 category:tech")
	if got := sender.last(); !strings.Contains(got, "Added") ||
		!strings.Contains(got, "tech") || !strings.Contains(got, "under $500") {
		t.Fatalf("watch reply missing constraints: %q", got)
	}

	entries, err := store.ListWatches("@u")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].MaxPrice != 500 || entries[0].Category != "tech" {
		t.Fatalf("stored watch wrong: %+v", entries)
	}

	// Re-watch refines and reports "Updated".
	h.HandleMessage("@u", "!watch laptop under 400 category:tech")
	if got := sender.last(); !strings.Contains(got, "Updated") {
		t.Fatalf("re-watch should say Updated: %q", got)
	}
}

func TestHandleWatchRejectsEmptyLabel(t *testing.T) {
	h, sender, _ := newTestHandler(t)
	h.HandleMessage("@u", "!watch under 30")
	if got := sender.last(); !strings.Contains(got, "include something to watch") {
		t.Fatalf("empty label should be rejected: %q", got)
	}
}

func TestHandleDigestToggle(t *testing.T) {
	h, sender, store := newTestHandler(t)

	h.HandleMessage("@u", "!digest")
	if got := sender.last(); !strings.Contains(got, "off") {
		t.Fatalf("default digest status should be off: %q", got)
	}

	h.HandleMessage("@u", "!digest on")
	if mode, _ := store.NotifyMode("@u"); mode != "daily" {
		t.Fatalf("digest on should set daily, got %q", mode)
	}

	h.HandleMessage("@u", "!digest off")
	if mode, _ := store.NotifyMode("@u"); mode != "instant" {
		t.Fatalf("digest off should set instant, got %q", mode)
	}
}
