package kiro

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sorafujitani/ccsession/internal/grep"
	"github.com/sorafujitani/ccsession/internal/session"
)

func TestScanReadsClassicV2AndV3(t *testing.T) {
	f := newFixture(t)
	classicID := f.classic("classic-id", "classic prompt", "classic answer", 1000)
	v2ID := f.v2("v2-id", "V2 title", "v2 prompt", "v2 answer", "2026-06-02T00:00:00Z")
	v3ID := f.v3("v3-id", "V3 title", "v3 prompt", "v3 answer", "2026-06-03T00:00:00Z", true)
	checkoutID := f.v3InBucket("11fe14a563f7aed6", "checkout-id", "Checkout title",
		"checkout prompt", "checkout answer", "2026-06-04T00:00:00Z", true)

	sessions, err := OpenAt(f.home).Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(sessions) != 4 {
		t.Fatalf("Scan returned %d sessions, want 4", len(sessions))
	}
	got := sessionsByID(sessions)
	for _, id := range []string{classicID, v2ID, v3ID, checkoutID} {
		if got[id] == nil {
			t.Errorf("Scan missing %s", id)
		}
	}
	if got[classicID].Label != "classic prompt" || !isClassicPath(got[classicID].JSONLPath) {
		t.Errorf("classic session = %+v", got[classicID])
	}
	if got[v2ID].Label != "V2 title" || filepath.Ext(got[v2ID].JSONLPath) != ".jsonl" {
		t.Errorf("v2 session = %+v", got[v2ID])
	}
	if got[v3ID].Label != "V3 title" || filepath.Base(got[v3ID].JSONLPath) != "messages.jsonl" {
		t.Errorf("v3 session = %+v", got[v3ID])
	}
	if got[checkoutID].Label != "Checkout title" || !IsV3Path(got[checkoutID].JSONLPath) {
		t.Errorf("checkout v3 session = %+v", got[checkoutID])
	}
	for _, sess := range sessions {
		if sess.CWD != f.cwd || !sess.CWDExists || sess.CWDUnknown {
			t.Errorf("%s cwd fields = %q exists=%v unknown=%v", sess.ID, sess.CWD, sess.CWDExists, sess.CWDUnknown)
		}
	}
}

func TestMessagesRenderOnlyVisibleTurns(t *testing.T) {
	f := newFixture(t)
	ids := []string{
		f.classic("classic-id", "classic prompt", "classic answer", 1000),
		f.v2("v2-id", "V2 title", "v2 prompt", "v2 answer", "2026-06-02T00:00:00Z"),
		f.v3("v3-id", "V3 title", "v3 prompt", "v3 answer", "2026-06-03T00:00:00Z", true),
		f.v3InBucket("11fe14a563f7aed6", "checkout-id", "Checkout title",
			"checkout prompt", "checkout answer", "2026-06-04T00:00:00Z", true),
	}
	store := OpenAt(f.home)
	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			messages, startedAt, total, err := store.Messages(id, 1)
			if err != nil {
				t.Fatalf("Messages: %v", err)
			}
			if startedAt.IsZero() {
				t.Error("startedAt is zero")
			}
			if total != 2 {
				t.Fatalf("total = %d, want 2", total)
			}
			if len(messages) != 1 || messages[0].Role != "assistant" {
				t.Fatalf("limited messages = %#v, want final assistant turn", messages)
			}
		})
	}
}

func TestGrepKeysFeedScanFilteredAcrossStores(t *testing.T) {
	t.Setenv(grep.EnvCacheDir, t.TempDir())
	f := newFixture(t)
	f.classic("classic-id", "classic prompt", "classic needle", 1000)
	v2ID := f.v2("v2-id", "V2 title", "v2 prompt", "v2 needle", "2026-06-02T00:00:00Z")
	f.v3("v3-id", "V3 title", "v3 prompt", "v3 answer", "2026-06-03T00:00:00Z", true)
	checkoutID := f.v3InBucket("11fe14a563f7aed6", "checkout-id", "Checkout title",
		"checkout prompt", "checkout needle", "2026-06-04T00:00:00Z", true)
	store := OpenAt(f.home)

	keys, err := store.GrepKeys("v2 needle", false)
	if err != nil {
		t.Fatalf("GrepKeys: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("GrepKeys = %#v, want one match", keys)
	}
	if _, ok := keys[v2ID]; !ok {
		t.Fatalf("GrepKeys missing %s", v2ID)
	}
	sessions, err := store.ScanFiltered(keys)
	if err != nil {
		t.Fatalf("ScanFiltered: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != v2ID {
		t.Fatalf("ScanFiltered = %#v, want only %s", sessions, v2ID)
	}

	keys, err = store.GrepKeys("classic needle", false)
	if err != nil || len(keys) != 1 {
		t.Fatalf("classic GrepKeys = %#v, err=%v", keys, err)
	}

	keys, err = store.GrepKeys("checkout needle", false)
	if err != nil || len(keys) != 1 {
		t.Fatalf("checkout GrepKeys = %#v, err=%v", keys, err)
	}
	if _, ok := keys[checkoutID]; !ok {
		t.Fatalf("GrepKeys missing checkout session %s", checkoutID)
	}
}

func TestDuplicateIDKeepsNewestSession(t *testing.T) {
	f := newFixture(t)
	id := f.classic("duplicate-id", "old classic", "old answer", 1000)
	f.v2(id, "new v2", "new prompt", "new answer", "2026-06-02T00:00:00Z")

	sessions, err := OpenAt(f.home).Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("Scan returned %d sessions, want one representative", len(sessions))
	}
	if sessions[0].Label != "new v2" || sessions[0].JSONLPath == "" {
		t.Fatalf("representative = %+v, want newer v2 session", sessions[0])
	}
}

func TestDuplicateClassicIDTieUsesSortedCWD(t *testing.T) {
	f := newFixture(t)
	f.classicAt(f.cwd, "duplicate-id", "later cwd", "answer", 1000)
	f.classicAt("/aaa", "duplicate-id", "sorted cwd", "answer", 1000)

	sessions, err := OpenAt(f.home).Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(sessions) != 1 || sessions[0].CWD != "/aaa" {
		t.Fatalf("sessions = %#v, want lexicographically first cwd", sessions)
	}
}

func TestFindByLocatorUsesExactFile(t *testing.T) {
	f := newFixture(t)
	classicID := f.classic("classic-id", "classic prompt", "classic answer", 1000)
	v2ID := f.v2("v2-id", "V2 title", "v2 prompt", "v2 answer", "2026-06-02T00:00:00Z")
	v3ID := f.v3("v3-id", "V3 title", "v3 prompt", "v3 answer", "2026-06-03T00:00:00Z", true)
	checkoutID := f.v3InBucket("11fe14a563f7aed6", "checkout-id", "Checkout title",
		"checkout prompt", "checkout answer", "2026-06-04T00:00:00Z", true)
	store := OpenAt(f.home)
	sessions, err := store.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	got := sessionsByID(sessions)
	for _, id := range []string{classicID, v2ID, v3ID, checkoutID} {
		found, err := store.FindByLocator(id, got[id].JSONLPath)
		if err != nil {
			t.Fatalf("FindByLocator(%s): %v", id, err)
		}
		if found.ID != id || found.JSONLPath != got[id].JSONLPath {
			t.Errorf("FindByLocator(%s) = %+v", id, found)
		}
	}
	if _, err := store.FindByLocator("wrong-id", got[v2ID].JSONLPath); !errors.Is(err, session.ErrSessionFileMissing) {
		t.Fatalf("mismatched locator err = %v, want ErrSessionFileMissing", err)
	}
}

func TestV3WithoutWorkspaceMarksCWDUnknown(t *testing.T) {
	f := newFixture(t)
	id := f.v3("v3-id", "V3 title", "prompt", "answer", "2026-06-03T00:00:00Z", false)

	sess, err := OpenAt(f.home).FindByID(id)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if !sess.CWDUnknown || sess.CWDExists || sess.CWD != "" {
		t.Fatalf("cwd fields = %q exists=%v unknown=%v", sess.CWD, sess.CWDExists, sess.CWDUnknown)
	}
}

func TestInvalidMetadataTimeFallsBackToModTime(t *testing.T) {
	f := newFixture(t)
	id := f.v2("v2-id", "V2 title", "prompt", "answer", "invalid")
	path := filepath.Join(f.home, "sessions", "cli", id+".json")
	writeJSON(t, path, v2Metadata{ID: id, CWD: f.cwd, Title: "V2 title", CreatedAt: "invalid", UpdatedAt: "invalid"})
	want := time.Date(2025, 1, 2, 3, 4, 5, 0, time.Local)
	if err := os.Chtimes(path, want, want); err != nil {
		t.Fatal(err)
	}

	sess, err := OpenAt(f.home).FindByID(id)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if sess.LastEpoch != want.Unix() {
		t.Fatalf("LastEpoch = %d, want modtime %d", sess.LastEpoch, want.Unix())
	}
}

type fixture struct {
	t    *testing.T
	home string
	cwd  string
	db   *sql.DB
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	home := t.TempDir()
	cwd := t.TempDir()
	path := filepath.Join(home, "data.sqlite3")
	u := url.URL{Scheme: "file", Path: path}
	db, err := sql.Open("sqlite3", u.String())
	if err != nil {
		t.Fatalf("open sqlite fixture: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE conversations_v2 (
		key TEXT NOT NULL,
		conversation_id TEXT NOT NULL,
		value TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (key, conversation_id)
	)`); err != nil {
		t.Fatalf("create classic schema: %v", err)
	}
	return &fixture{t: t, home: home, cwd: cwd, db: db}
}

func (f *fixture) classic(id, prompt, answer string, updatedAt int64) string {
	return f.classicAt(f.cwd, id, prompt, answer, updatedAt)
}

func (f *fixture) classicAt(cwd, id, prompt, answer string, updatedAt int64) string {
	f.t.Helper()
	value := map[string]any{
		"conversation_id": id,
		"history": []any{
			map[string]any{
				"user": map[string]any{
					"content":   map[string]any{"Prompt": map[string]any{"prompt": prompt}},
					"timestamp": "2026-06-01T00:00:00Z",
				},
				"assistant": map[string]any{"Response": map[string]any{"content": answer}},
			},
			map[string]any{
				"user":      map[string]any{"content": map[string]any{"ToolUseResults": map[string]any{"tool_use_results": []any{"hidden result"}}}},
				"assistant": map[string]any{"ToolUse": map[string]any{"content": "", "tool_uses": []any{"hidden tool"}}},
			},
		},
	}
	raw, err := json.Marshal(value)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO conversations_v2
		(key, conversation_id, value, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		cwd, id, string(raw), updatedAt-100, updatedAt); err != nil {
		f.t.Fatalf("insert classic: %v", err)
	}
	return id
}

func (f *fixture) v2(id, title, prompt, answer, updatedAt string) string {
	f.t.Helper()
	dir := filepath.Join(f.home, "sessions", "cli")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	meta := v2Metadata{
		ID:        id,
		CWD:       f.cwd,
		Title:     title,
		CreatedAt: "2026-06-01T00:00:00Z",
		UpdatedAt: updatedAt,
	}
	writeJSON(f.t, filepath.Join(dir, id+".json"), meta)
	lines := []any{
		map[string]any{"kind": "Prompt", "data": map[string]any{
			"content": []any{map[string]any{"kind": "text", "data": prompt}},
			"meta":    map[string]any{"timestamp": int64(1780272000)},
		}},
		map[string]any{"kind": "ToolResults", "data": map[string]any{
			"content": []any{map[string]any{"kind": "toolResult", "data": "hidden result"}},
		}},
		map[string]any{"kind": "AssistantMessage", "data": map[string]any{
			"content": []any{
				map[string]any{"kind": "text", "data": answer},
				map[string]any{"kind": "toolUse", "data": map[string]any{"name": "hidden tool"}},
			},
		}},
	}
	writeJSONL(f.t, filepath.Join(dir, id+".jsonl"), lines)
	return id
}

func (f *fixture) v3(id, title, prompt, answer, updatedAt string, withCWD bool) string {
	return f.v3InBucket("_global", id, title, prompt, answer, updatedAt, withCWD)
}

func (f *fixture) v3InBucket(bucket, id, title, prompt, answer, updatedAt string, withCWD bool) string {
	f.t.Helper()
	dir := filepath.Join(f.home, "sessions", bucket, "sess_"+id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	meta := v3Metadata{
		ID:             id,
		Title:          title,
		CreatedAt:      "2026-06-01T00:00:00Z",
		LastModifiedAt: updatedAt,
	}
	if withCWD {
		meta.WorkspacePaths = []string{f.cwd}
	}
	writeJSON(f.t, filepath.Join(dir, "session.json"), meta)
	lines := []any{
		map[string]any{"timestamp": "2026-06-01T00:00:00Z", "payload": map[string]any{"type": "user", "content": prompt}},
		map[string]any{"timestamp": "2026-06-01T00:00:01Z", "payload": map[string]any{"type": "turn_start"}},
		map[string]any{"timestamp": "2026-06-01T00:00:02Z", "payload": map[string]any{"type": "assistant", "content": answer}},
		map[string]any{"timestamp": "2026-06-01T00:00:03Z", "payload": map[string]any{"type": "session_event", "content": "hidden event"}},
	}
	writeJSONL(f.t, filepath.Join(dir, "messages.jsonl"), lines)
	return id
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeJSONL(t *testing.T, path string, values []any) {
	t.Helper()
	var raw []byte
	for _, value := range values {
		line, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		raw = append(raw, line...)
		raw = append(raw, '\n')
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func sessionsByID(sessions []*session.Session) map[string]*session.Session {
	out := make(map[string]*session.Session, len(sessions))
	for _, sess := range sessions {
		out[sess.ID] = sess
	}
	return out
}

func TestClassicMessageTimestamp(t *testing.T) {
	raw := []byte(`{"history":[{"user":{"content":{"Prompt":{"prompt":"hello"}},"timestamp":"2026-06-01T00:00:00Z"},"assistant":{"Response":{"content":"answer"}}}]}`)
	messages, err := parseClassicMessages(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || !messages[0].Timestamp.Equal(messages[1].Timestamp) ||
		!messages[0].Timestamp.Equal(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestReadJSONLLineSkipsOversizeLine(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader(strings.Repeat("x", 10) + "\n" + `{"ok":true}` + "\n"))
	line, err := readJSONLLine(reader, 4)
	if err != nil || line != nil {
		t.Fatalf("oversize line = %q, err=%v; want nil, nil", line, err)
	}
	line, err = readJSONLLine(reader, 1024)
	if err != nil || string(line) != `{"ok":true}` {
		t.Fatalf("next line = %q, err=%v", line, err)
	}
}

func TestIsV3Path(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: filepath.Join("home", ".kiro", "sessions", "_global", "sess_id", "messages.jsonl"), want: true},
		{path: filepath.Join("home", ".kiro", "sessions", "11fe14a563f7aed6", "sess_id", "messages.jsonl"), want: true},
		{path: filepath.Join("home", ".kiro", "sessions", "cli", "sess_id", "messages.jsonl"), want: false},
		{path: filepath.Join("home", "other", "bucket", "sess_id", "messages.jsonl"), want: false},
		{path: filepath.Join("home", ".kiro", "sessions", "cli", "id.jsonl"), want: false},
	}
	for _, tt := range tests {
		if got := IsV3Path(tt.path); got != tt.want {
			t.Errorf("IsV3Path(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestOpenHonorsKiroHome(t *testing.T) {
	want := t.TempDir()
	t.Setenv(EnvHome, want)

	store, err := Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if store.home != want {
		t.Errorf("Open home = %q, want %q", store.home, want)
	}
}
