package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime/debug"

	"github.com/sorafujitani/ccsession/internal/list"
	"github.com/sorafujitani/ccsession/internal/preview"
	"github.com/sorafujitani/ccsession/internal/resume"
)

// These are filled in by `go build -ldflags "-X main.version=... -X main.commit=... -X main.date=..."`.
// goreleaser and the nix flake set them explicitly. For `go install` builds
// without ldflags we recover them from runtime/debug.ReadBuildInfo at init time.
var (
	version = "dev"
	commit  = ""
	date    = ""
)

func init() {
	if version != "dev" {
		return
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		version = v
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) >= 7 && commit == "" {
				commit = s.Value[:7]
			}
		case "vcs.time":
			if date == "" {
				date = s.Value
			}
		}
	}
}

const usage = `ccsession - fzf frontend for "claude --resume"

USAGE:
  ccsession                       # list -> fzf -> resume
  ccsession list  [--grep Q]      # TSV rows for fzf
  ccsession preview <sessionId>   # preview pane content
  ccsession resume  <sessionId>   # chdir to original cwd, exec claude --resume
  ccsession --help | --version

LIST FLAGS:
  --grep <query>   filter sessions by content (requires ripgrep)
  --no-color       disable ANSI color output

REQUIRES: fzf, claude. ripgrep only when --grep / grep mode is used.
`

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		if err := runDefault(); err != nil {
			fmt.Fprintln(os.Stderr, "ccsession:", err)
			os.Exit(1)
		}
		return
	}
	switch args[0] {
	case "list":
		cmdList(args[1:])
	case "preview":
		cmdPreview(args[1:])
	case "resume":
		cmdResume(args[1:])
	case "-h", "--help", "help":
		fmt.Print(usage)
	case "-v", "--version", "version":
		fmt.Printf("ccsession %s\n", version)
		if commit != "" {
			fmt.Printf("  commit: %s\n", commit)
		}
		if date != "" {
			fmt.Printf("  built : %s\n", date)
		}
	default:
		fmt.Fprintln(os.Stderr, "ccsession: unknown subcommand:", args[0])
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}

func cmdList(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	grepFlag := fs.String("grep", "", "filter sessions by content (ripgrep)")
	noColor := fs.Bool("no-color", false, "disable ANSI colors")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if err := list.Run(list.Options{Grep: *grepFlag, NoColor: *noColor}); err != nil {
		fmt.Fprintln(os.Stderr, "ccsession list:", err)
		os.Exit(1)
	}
}

func cmdPreview(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "ccsession preview: session id required")
		os.Exit(2)
	}
	if err := preview.Run(args[0]); err != nil {
		fmt.Fprintln(os.Stderr, "ccsession preview:", err)
		os.Exit(1)
	}
}

func cmdResume(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "ccsession resume: session id required")
		os.Exit(2)
	}
	if err := resume.Run(args[0]); err != nil {
		fmt.Fprintln(os.Stderr, "ccsession resume:", err)
		os.Exit(1)
	}
}

func runDefault() error {
	if _, err := exec.LookPath("fzf"); err != nil {
		return fmt.Errorf("fzf is required but not found in PATH")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command("bash", "-c", defaultScript)
	cmd.Env = append(os.Environ(), "CCSESSION_BIN="+self)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// defaultScript wires `ccsession list` into fzf with three input modes.
// fuzzy mode: fzf's own matcher filters across time/dir/label (columns 3,4,5).
// dir   mode: fzf's matcher narrowed to the directory column only (column 4).
// grep  mode: every keystroke reloads the list through `ccsession list --grep`.
// ctrl-g: grep, ctrl-o: dir, ctrl-f: fuzzy (default).
const defaultScript = `set -u
id=$("$CCSESSION_BIN" list | fzf \
  --ansi \
  --delimiter=$'\t' \
  --with-nth=3,4,5 \
  --nth=1,2,3 \
  --no-sort \
  --preview "$CCSESSION_BIN preview {1}" \
  --preview-window=right,60%,wrap \
  --header='[fuzzy] ctrl-g: grep / ctrl-o: dir / ctrl-f: fuzzy / enter: resume' \
  --bind 'start:unbind(change)' \
  --bind "change:reload(sleep 0.05; $CCSESSION_BIN list --grep {q})" \
  --bind "ctrl-g:transform:echo \"change-prompt(grep> )+disable-search+reload(sleep 0.05; $CCSESSION_BIN list --grep {q})+rebind(change)\"" \
  --bind "ctrl-o:transform:echo \"change-prompt(dir> )+enable-search+change-nth(2)+reload($CCSESSION_BIN list)+unbind(change)\"" \
  --bind "ctrl-f:transform:echo \"change-prompt(> )+enable-search+change-nth(1,2,3)+reload($CCSESSION_BIN list)+unbind(change)\"" \
  | awk -F'\t' '{print $1}') || true
if [ -n "$id" ]; then
  exec "$CCSESSION_BIN" resume "$id"
fi
`
