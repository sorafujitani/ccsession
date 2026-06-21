package opencode

import (
	"slices"
	"testing"

	"github.com/sorafujitani/ccsession/internal/session"
)

func bodies(ms []session.Message) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Role + ":" + m.Body
	}
	return out
}

func TestMessages_FromPartsChronologicalAndCounts(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.session("ses_a", "/p", "t", 100)
	f.partsTurn("ses_a", "user", 10, "hi")
	f.partsTurn("ses_a", "assistant", 20, "hello")
	d := f.open()

	msgs, started, total, err := d.Messages("ses_a", 30)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if started.UnixMilli() != 10 {
		t.Errorf("startedAt = %d ms, want 10", started.UnixMilli())
	}
	if got, want := bodies(msgs), []string{"user:hi", "assistant:hello"}; !slices.Equal(got, want) {
		t.Errorf("messages = %v, want %v", got, want)
	}
}

func TestMessages_JoinsMultipleTextParts(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.session("ses_a", "/p", "t", 100)
	f.partsTurn("ses_a", "assistant", 10, "part one", "part two")
	d := f.open()

	msgs, _, _, err := d.Messages("ses_a", 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if msgs[0].Body != "part one\npart two" {
		t.Errorf("joined body = %q, want two parts joined by newline", msgs[0].Body)
	}
}

func TestMessages_ProjectionPreferredWhenRenderable(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.session("ses_a", "/p", "t", 100)
	// The parts store has content, but the projection has renderable turns; the projection wins.
	f.partsTurn("ses_a", "user", 10, "from parts")
	f.projectionRow("ses_a", "user", 1, `{"text":"from projection first","time":{"created":10}}`)
	f.projectionRow("ses_a", "assistant", 2, `{"text":"from projection second","time":{"created":20}}`)
	d := f.open()

	msgs, _, _, _ := d.Messages("ses_a", 30)
	if got, want := bodies(msgs), []string{"user:from projection first", "assistant:from projection second"}; !slices.Equal(got, want) {
		t.Errorf("messages = %v, want projection turns reversed to chronological", got)
	}
}

func TestMessages_ProjectionStartedAtAndTotal(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.session("ses_a", "/p", "t", 100)
	f.projectionRow("ses_a", "user", 1, `{"text":"first","time":{"created":111}}`)
	f.projectionRow("ses_a", "assistant", 2, `{"text":"second","time":{"created":222}}`)

	_, started, total, err := f.open().Messages("ses_a", 30)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	// After reversal the oldest turn is first, so startedAt is its time.
	if started.UnixMilli() != 111 {
		t.Errorf("startedAt = %d ms, want 111 (oldest after reversal)", started.UnixMilli())
	}
}

func TestMessages_ProjectionLimitReturnsNewestN(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.session("ses_a", "/p", "t", 100)
	for i := 1; i <= 4; i++ {
		f.projectionRow("ses_a", "user", int64(i),
			`{"text":"`+itoa(int64(i))+`","time":{"created":`+itoa(int64(i*10))+`}}`)
	}

	msgs, _, total, _ := f.open().Messages("ses_a", 2)
	if total != 4 {
		t.Errorf("total = %d, want 4", total)
	}
	if got, want := bodies(msgs), []string{"user:3", "user:4"}; !slices.Equal(got, want) {
		t.Errorf("limited projection = %v, want newest 2 in chronological order", got)
	}
}

// A DB predating the session_message table must fall back to the parts store
// rather than erroring (the absent-projection case).
func TestMessages_NoProjectionTableFallsBackToParts(t *testing.T) {
	f := newFixture(t, fixtureOpts{dropTable: "session_message"})
	f.session("ses_a", "/p", "t", 100)
	f.partsTurn("ses_a", "user", 10, "only the parts store exists")

	msgs, _, total, err := f.open().Messages("ses_a", 30)
	if err != nil {
		t.Fatalf("Messages should fall back, not error: %v", err)
	}
	if total != 1 || len(msgs) != 1 || msgs[0].Body != "only the parts store exists" {
		t.Errorf("got %v (total %d), want the parts turn", bodies(msgs), total)
	}
}

func TestMessages_SwitchEventsFallBackToParts(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.session("ses_a", "/p", "t", 100)
	// The projection holds only non-renderable switch events; fall back to the parts store.
	f.projectionRow("ses_a", "model-switched", 1, `{"model":{"id":"x"}}`)
	f.projectionRow("ses_a", "agent-switched", 2, `{"agent":"build"}`)
	f.partsTurn("ses_a", "user", 10, "live parts content")
	d := f.open()

	msgs, _, total, _ := d.Messages("ses_a", 30)
	if total != 1 || len(msgs) != 1 || msgs[0].Body != "live parts content" {
		t.Errorf("got %v (total %d), want parts fallback", bodies(msgs), total)
	}
}

func TestMessages_EmptySession(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.session("ses_a", "/p", "t", 100)
	d := f.open()

	msgs, _, total, err := d.Messages("ses_a", 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 || total != 0 {
		t.Errorf("empty session: msgs=%d total=%d, want 0/0", len(msgs), total)
	}
}

func TestMessages_LimitReturnsNewestN(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.session("ses_a", "/p", "t", 100)
	for i := 1; i <= 5; i++ {
		f.partsTurn("ses_a", "user", int64(i*10), itoa(int64(i)))
	}
	d := f.open()

	msgs, _, total, _ := d.Messages("ses_a", 2)
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	// Newest 2, still chronological: "4","5".
	if got, want := bodies(msgs), []string{"user:4", "user:5"}; !slices.Equal(got, want) {
		t.Errorf("limited messages = %v, want newest 2 in order", got)
	}
}

func TestMessages_NonTextPartCountsAsEmptyTurn(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.session("ses_a", "/p", "t", 100)
	f.reasoningTurn("ses_a", "assistant", 10)
	d := f.open()

	msgs, _, total, _ := d.Messages("ses_a", 30)
	if total != 1 || len(msgs) != 1 || msgs[0].Body != "" {
		t.Errorf("got %v (total %d), want one empty-body turn", bodies(msgs), total)
	}
}

func BenchmarkMessagesLargeSession(b *testing.B) {
	f := newFixture(b, fixtureOpts{})
	f.session("ses_large", "/tmp/large", "large", 100)
	for i := range 1000 {
		f.partsTurn("ses_large", "user", int64(i+1)*1000, "message "+itoa(int64(i)))
	}
	d := f.open()

	b.ResetTimer()
	for range b.N {
		msgs, _, total, err := d.Messages("ses_large", 30)
		if err != nil {
			b.Fatalf("Messages: %v", err)
		}
		if total != 1000 || len(msgs) != 30 {
			b.Fatalf("Messages returned total=%d len=%d, want 1000/30", total, len(msgs))
		}
	}
}
