package resume

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sorafujitani/ccsession/internal/codex"
	"github.com/sorafujitani/ccsession/internal/list"
	"github.com/sorafujitani/ccsession/internal/source"
)

func TestRunSpec_PrintsResumeTargetWithoutLookingUpBinary(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	id := "11111111-1111-1111-1111-111111111111"
	writeResumeSession(t, home, cwd, id)
	t.Setenv("HOME", home)
	t.Setenv(source.EnvVar, "")
	t.Setenv("PATH", "")

	var buf bytes.Buffer
	if err := RunSpec(id, Options{Out: &buf}); err != nil {
		t.Fatalf("RunSpec: %v", err)
	}

	var spec Spec
	if err := json.Unmarshal(buf.Bytes(), &spec); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, buf.String())
	}
	if spec.Source != "claude" || spec.ID != id {
		t.Errorf("session = source:%q id:%q", spec.Source, spec.ID)
	}
	if spec.CWD != cwd || !spec.CWDExists || spec.CWDUnknown {
		t.Errorf("cwd fields = cwd:%q exists:%v unknown:%v", spec.CWD, spec.CWDExists, spec.CWDUnknown)
	}
	if spec.Bin != "claude" {
		t.Errorf("bin = %q, want claude", spec.Bin)
	}
	wantArgs := []string{"claude", "--resume", id}
	if len(spec.Args) != len(wantArgs) {
		t.Fatalf("args = %v, want %v", spec.Args, wantArgs)
	}
	for i := range wantArgs {
		if spec.Args[i] != wantArgs[i] {
			t.Errorf("args[%d] = %q, want %q", i, spec.Args[i], wantArgs[i])
		}
	}
	if spec.Locator == "" {
		t.Error("locator is empty")
	}
}

func TestRunSpec_PreservesInputIDAndLocator(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	id := "11111111-1111-1111-1111-111111111111"
	path := writeResumeSession(t, home, cwd, id)
	locator := base64.RawURLEncoding.EncodeToString([]byte(path))
	t.Setenv("HOME", home)
	t.Setenv(source.EnvVar, "")

	var buf bytes.Buffer
	if err := RunSpec(id, Options{Locator: locator, Out: &buf}); err != nil {
		t.Fatalf("RunSpec: %v", err)
	}

	var spec Spec
	if err := json.Unmarshal(buf.Bytes(), &spec); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, buf.String())
	}
	if spec.ID != id {
		t.Errorf("id = %q, want input id %q", spec.ID, id)
	}
	if spec.Locator != locator {
		t.Errorf("locator = %q, want input locator", spec.Locator)
	}
}

func TestRunSpec_SourceAllKeepsCompositeIDAndLocator(t *testing.T) {
	home := t.TempDir()
	codexHome := t.TempDir()
	cwd := t.TempDir()
	id := "11111111-1111-1111-1111-111111111111"
	writeResumeSession(t, home, cwd, id)
	writeCodexResumeSession(t, codexHome, cwd, id)
	t.Setenv("HOME", home)
	t.Setenv(codex.EnvHome, codexHome)
	t.Setenv(source.EnvVar, "all")

	var listBuf bytes.Buffer
	if err := list.Run(list.Options{JSON: true, Out: &listBuf}); err != nil {
		t.Fatalf("list.Run: %v", err)
	}
	var rows []list.JSONSession
	if err := json.Unmarshal(listBuf.Bytes(), &rows); err != nil {
		t.Fatalf("json.Unmarshal list: %v\n%s", err, listBuf.String())
	}
	var codexRow list.JSONSession
	for _, row := range rows {
		if strings.HasPrefix(row.ID, "codex:") {
			codexRow = row
			break
		}
	}
	if codexRow.ID == "" || !strings.HasPrefix(codexRow.Locator, "codex:") {
		t.Fatalf("missing composite codex row: %#v", rows)
	}

	var specBuf bytes.Buffer
	if err := RunSpec(codexRow.ID, Options{Locator: codexRow.Locator, Out: &specBuf}); err != nil {
		t.Fatalf("RunSpec: %v", err)
	}
	var spec Spec
	if err := json.Unmarshal(specBuf.Bytes(), &spec); err != nil {
		t.Fatalf("json.Unmarshal spec: %v\n%s", err, specBuf.String())
	}
	if spec.ID != codexRow.ID {
		t.Errorf("id = %q, want list id %q", spec.ID, codexRow.ID)
	}
	if spec.Locator != codexRow.Locator {
		t.Errorf("locator = %q, want list locator %q", spec.Locator, codexRow.Locator)
	}
	if spec.Bin != "codex" || len(spec.Args) < 3 || spec.Args[2] != id {
		t.Errorf("resume target = %q %v, want codex resume local id", spec.Bin, spec.Args)
	}
}

func writeResumeSession(t *testing.T, home, cwd, id string) string {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", "-proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `{"type":"user","timestamp":"2026-05-26T10:00:00Z","cwd":"` + cwd + `","message":{"role":"user","content":"resume me"}}` + "\n"
	path := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
	return path
}

func writeCodexResumeSession(t *testing.T, home, cwd, id string) {
	t.Helper()
	body := `{"timestamp":"2026-06-14T00:00:00Z","type":"session_meta","payload":{"id":"` + id + `","timestamp":"2026-06-14T00:00:00Z","cwd":"` + cwd + `"}}` + "\n" +
		`{"timestamp":"2026-06-14T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"codex resume me"}]}}` + "\n"
	path := filepath.Join(home, "sessions", "2026", "06", "14", "rollout-2026-06-14T00-00-00-"+id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir codex: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write codex session: %v", err)
	}
}
