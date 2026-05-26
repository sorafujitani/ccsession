package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
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
  --grep <query>   filter sessions by user/assistant content (fixed-string)
  --regex          treat --grep query as a regular expression
  --color <mode>   color output: auto (default) | always | never
  --no-color       shorthand for --color=never

REQUIRES: fzf, claude.
`

const listUsage = `ccsession list - emit TSV rows for fzf

USAGE:
  ccsession list [--grep <query>] [--regex] [--color <mode>] [--no-color]

FLAGS:
  --grep <query>   filter sessions by user/assistant content (fixed-string)
  --regex          treat --grep query as a regular expression
  --color <mode>   color output: auto (default) | always | never
                   "auto" emits ANSI only when stdout is a terminal
  --no-color       shorthand for --color=never
`

const previewUsage = `ccsession preview - render the preview pane for a session id

USAGE:
  ccsession preview <sessionId>
`

const resumeUsage = `ccsession resume - chdir to the session's cwd and exec "claude --resume"

USAGE:
  ccsession resume <sessionId>
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
		// Error line goes to stderr; the usage block itself is informational
		// and goes to stdout so it can be piped / grepped.
		fmt.Fprintln(os.Stderr, "ccsession: unknown subcommand:", args[0])
		fmt.Fprint(os.Stdout, usage)
		os.Exit(2)
	}
}

// newFlagSet builds a FlagSet with project-style usage handling: errors and
// stray flag output go to stderr via our own messages, and the help block
// goes to stdout in our format (not Go's stock "Usage of list:" block).
func newFlagSet(name, helpText string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() { fmt.Fprint(os.Stdout, helpText) }
	return fs
}

func handleFlagError(name string, _ *flag.FlagSet, err error) {
	// flag.FlagSet.Parse already invoked fs.Usage() on its own when it hit
	// ErrHelp or a parsing error, so we only need to add the stderr error
	// line for the error case (the help case prints nothing extra).
	if errors.Is(err, flag.ErrHelp) {
		os.Exit(0)
	}
	fmt.Fprintf(os.Stderr, "ccsession %s: %s\n", name, err)
	os.Exit(2)
}

func cmdList(args []string) {
	fs := newFlagSet("list", listUsage)
	grepFlag := fs.String("grep", "", "filter sessions by user/assistant content (fixed-string)")
	regexFlag := fs.Bool("regex", false, "treat --grep query as a regular expression")
	colorFlag := fs.String("color", "auto", "color output: auto|always|never")
	noColor := fs.Bool("no-color", false, "shorthand for --color=never")
	if err := fs.Parse(args); err != nil {
		handleFlagError("list", fs, err)
	}
	if err := list.Run(list.Options{
		Grep:    *grepFlag,
		Regex:   *regexFlag,
		Color:   *colorFlag,
		NoColor: *noColor,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "ccsession list:", err)
		os.Exit(1)
	}
}

func cmdPreview(args []string) {
	fs := newFlagSet("preview", previewUsage)
	if err := fs.Parse(args); err != nil {
		handleFlagError("preview", fs, err)
	}
	rest := fs.Args()
	if len(rest) < 1 {
		fmt.Fprintln(os.Stderr, "ccsession preview: session id required")
		fs.Usage()
		os.Exit(2)
	}
	if err := preview.Run(rest[0]); err != nil {
		fmt.Fprintln(os.Stderr, "ccsession preview:", err)
		os.Exit(1)
	}
}

func cmdResume(args []string) {
	fs := newFlagSet("resume", resumeUsage)
	if err := fs.Parse(args); err != nil {
		handleFlagError("resume", fs, err)
	}
	rest := fs.Args()
	if len(rest) < 1 {
		fmt.Fprintln(os.Stderr, "ccsession resume: session id required")
		fs.Usage()
		os.Exit(2)
	}
	if err := resume.Run(rest[0]); err != nil {
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
id=$("$CCSESSION_BIN" list --color=always | fzf \
  --ansi \
  --delimiter=$'\t' \
  --with-nth=3,4,5 \
  --nth=1,2,3 \
  --no-sort \
  --preview "$CCSESSION_BIN preview {1}" \
  --preview-window=right,60%,wrap \
  --header='[fuzzy] ctrl-g: grep / ctrl-o: dir / ctrl-f: fuzzy / enter: resume' \
  --bind 'start:unbind(change)' \
  --bind "change:reload(sleep 0.05; $CCSESSION_BIN list --color=always --grep {q})" \
  --bind "ctrl-g:transform:echo \"change-prompt(grep> )+disable-search+reload(sleep 0.05; $CCSESSION_BIN list --color=always --grep {q})+rebind(change)\"" \
  --bind "ctrl-o:transform:echo \"change-prompt(dir> )+enable-search+change-nth(2)+reload($CCSESSION_BIN list --color=always)+unbind(change)\"" \
  --bind "ctrl-f:transform:echo \"change-prompt(> )+enable-search+change-nth(1,2,3)+reload($CCSESSION_BIN list --color=always)+unbind(change)\"" \
  | awk -F'\t' '{print $1}') || true
if [ -n "$id" ]; then
  exec "$CCSESSION_BIN" resume "$id"
fi
`
