package kiro

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sorafujitani/ccsession/internal/session"
)

// The optional home is produced by scripts/benchmark-startup.py. Without it,
// benchmarks use 200 conversations with 41 turns and about 100 MB of JSON.
func benchmarkClassicStore(b *testing.B) (*Store, *sql.DB, int) {
	b.Helper()
	b.Setenv(session.EnvCacheDir, b.TempDir())
	if home := os.Getenv("CCSESSION_KIRO_BENCH_HOME"); home != "" {
		path := classicDBPath(home)
		db, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(path))
		if err != nil {
			b.Fatal(err)
		}
		b.Cleanup(func() { db.Close() })
		var count int
		if err := db.QueryRow(`SELECT count(*) FROM conversations_v2`).Scan(&count); err != nil {
			b.Fatal(err)
		}
		return &Store{home: filepath.Join(home, ".kiro"), dbPath: path}, db, count
	}
	f := newFixture(b)
	turn := `{"user":{"content":{"Prompt":{"prompt":"latest prompt"}}},"assistant":{"Response":{"content":"` + strings.Repeat("x", 12000) + `"}}}`
	last := `{"user":{"content":{"ToolUseResults":{}}},"assistant":{"Response":{"content":"` + strings.Repeat("x", 12000) + `"}}}`
	raw := `{"history":[` + strings.Repeat(turn+",", 40) + last + `]}`
	tx, err := f.db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	defer tx.Rollback()
	for i := range 200 {
		if _, err := tx.Exec(`INSERT INTO conversations_v2 VALUES (?, ?, ?, ?, ?)`,
			f.cwd, fmt.Sprintf("classic-synthetic-%05d", i), raw, 1000, 10000+i); err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	return OpenAt(f.home), f.db, 200
}

func BenchmarkClassicStartup(b *testing.B) {
	for _, name := range []string{"cold", "warm", "updated"} {
		b.Run(name, func(b *testing.B) {
			store, db, count := benchmarkClassicStore(b)
			if _, err := store.Scan(); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := range b.N {
				b.StopTimer()
				if name == "cold" {
					if err := os.RemoveAll(os.Getenv(session.EnvCacheDir)); err != nil {
						b.Fatal(err)
					}
				}
				if name == "updated" {
					if _, err := db.Exec(`UPDATE conversations_v2
						SET value=json_set(value, '$.history[39].user.content.Prompt.prompt', ?), updated_at=updated_at+1
						WHERE conversation_id='classic-synthetic-00000'`, fmt.Sprintf("updated prompt %d", i)); err != nil {
						b.Fatal(err)
					}
				}
				b.StartTimer()
				ss, err := store.Scan()
				if err != nil || len(ss) != count {
					b.Fatalf("Scan: count=%d, want=%d, error=%v", len(ss), count, err)
				}
			}
		})
	}
}

func BenchmarkClassicLabelQueries(b *testing.B) {
	_, db, _ := benchmarkClassicStore(b)
	const labels = `SELECT c.key, c.conversation_id, COALESCE((
		SELECT json_extract(h.value, '$.user.content.Prompt.prompt')
		FROM json_each(c.value, '$.history') h
		WHERE json_type(h.value, '$.user.content.Prompt.prompt') = 'text'
		ORDER BY CAST(h.key AS INTEGER) DESC LIMIT 1
	), ''), c.updated_at FROM conversations_v2 c`
	for _, tc := range []struct {
		name, query string
	}{
		{"labels", labels + ` ORDER BY c.key`},
		{"labels_jsonb", strings.Replace(labels, "json_each(", "jsonb_each(", 1) + ` ORDER BY c.key`},
		{"metadata", `SELECT key, conversation_id, '', updated_at FROM conversations_v2 ORDER BY key`},
		{"one_label", labels + ` WHERE c.conversation_id='classic-synthetic-00000'`},
		{"fingerprints", `SELECT key, conversation_id, value, updated_at FROM conversations_v2 ORDER BY key`},
	} {
		b.Run(tc.name, func(b *testing.B) {
			for range b.N {
				rows, err := db.Query(tc.query)
				if err != nil {
					b.Fatal(err)
				}
				for rows.Next() {
					var key, id string
					var raw []byte
					var updatedAt int64
					if err := rows.Scan(&key, &id, &raw, &updatedAt); err != nil {
						rows.Close()
						b.Fatal(err)
					}
					if tc.name == "fingerprints" {
						_ = sha256.Sum256(raw)
					}
				}
				if err := rows.Err(); err != nil {
					b.Fatal(err)
				}
				rows.Close()
			}
		})
	}
}
