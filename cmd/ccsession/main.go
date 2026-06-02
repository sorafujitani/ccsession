package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"

	"github.com/sorafujitani/ccsession/internal/list"
	"github.com/sorafujitani/ccsession/internal/preview"
	"github.com/sorafujitani/ccsession/internal/resume"
)

// excludeDirEnv is the env var that pipes --exclude-dir down to subcommands
// and to the fzf bash script. Set by main() when the global --exclude-dir
// flag is parsed; read by cmdList's flag default and by defaultScript.
const excludeDirEnv = "CCSESSION_EXCLUDE_DIR"

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
  ccsession [--exclude-dir <s>]                       # list -> fzf -> resume
  ccsession list [--grep Q] [--exclude-dir S]         # TSV rows for fzf
  ccsession preview <sessionId>   # preview pane content
  ccsession resume  <sessionId>   # chdir to original cwd, exec claude --resume
  ccsession --help | --version

GLOBAL FLAGS:
  --exclude-dir <s>   hide sessions whose cwd contains <s> (case-insensitive).
                      Applied to every list call, including grep/dir/fuzzy
                      reloads, so the matching directories never appear in
                      the picker.

LIST FLAGS:
  --grep <query>      filter sessions by user/assistant content (fixed-string)
  --regex             treat --grep query as a regular expression
  --exclude-dir <s>   hide sessions whose cwd contains <s> (case-insensitive)
  --color <mode>      color output: auto (default) | always | never
  --no-color          shorthand for --color=never

REQUIRES: fzf, claude.
`

const listUsage = `ccsession list - emit TSV rows for fzf

USAGE:
  ccsession list [--grep <query>] [--regex] [--exclude-dir <s>] [--color <mode>] [--no-color]

FLAGS:
  --grep <query>      filter sessions by user/assistant content (fixed-string)
  --regex             treat --grep query as a regular expression
  --exclude-dir <s>   hide sessions whose cwd contains <s> (case-insensitive)
  --color <mode>      color output: auto (default) | always | never
                      "auto" emits ANSI only when stdout is a terminal
  --no-color          shorthand for --color=never
`

const previewUsage = `ccsession preview - render the preview pane for a session id

USAGE:
  ccsession preview [--query <query>] [--regex] <sessionId>

FLAGS:
  --query <query>     highlight matches of <query> in the preview (fixed-string)
  --regex             treat --query as a regular expression
`

const resumeUsage = `ccsession resume - chdir to the session's cwd and exec "claude --resume"

USAGE:
  ccsession resume <sessionId>
`

func main() {
	excludeDir, args := parseGlobalFlags(os.Args[1:])
	if excludeDir != "" {
		// Propagate to subcommands (cmdList reads it as flag default) and
		// to the fzf bash script (defaultScript reads it directly).
		os.Setenv(excludeDirEnv, excludeDir)
	}
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

// parseGlobalFlags peels off ccsession-level flags that appear before any
// subcommand (or before the bare list/preview/resume token). Stops at the
// first non-flag argument so per-subcommand flag parsing keeps working.
// Currently only --exclude-dir is recognized; everything else is passed
// through unchanged.
func parseGlobalFlags(args []string) (excludeDir string, rest []string) {
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--exclude-dir":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "ccsession: --exclude-dir requires a value")
				os.Exit(2)
			}
			excludeDir = args[i+1]
			i += 2
		case strings.HasPrefix(a, "--exclude-dir="):
			excludeDir = strings.TrimPrefix(a, "--exclude-dir=")
			i++
		default:
			rest = append(rest, args[i:]...)
			return
		}
	}
	return
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
	// Default from env so `ccsession --exclude-dir foo list` works the same
	// as `ccsession list --exclude-dir foo`. An explicit flag still wins.
	excludeDirFlag := fs.String("exclude-dir", os.Getenv(excludeDirEnv), "hide sessions whose cwd contains <s> (case-insensitive)")
	colorFlag := fs.String("color", "auto", "color output: auto|always|never")
	noColor := fs.Bool("no-color", false, "shorthand for --color=never")
	if err := fs.Parse(args); err != nil {
		handleFlagError("list", fs, err)
	}
	if err := list.Run(list.Options{
		Grep:       *grepFlag,
		Regex:      *regexFlag,
		ExcludeDir: *excludeDirFlag,
		Color:      *colorFlag,
		NoColor:    *noColor,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "ccsession list:", err)
		os.Exit(1)
	}
}

func cmdPreview(args []string) {
	fs := newFlagSet("preview", previewUsage)
	queryFlag := fs.String("query", "", "highlight matches of <query> in the preview")
	regexFlag := fs.Bool("regex", false, "treat --query as a regular expression")
	if err := fs.Parse(args); err != nil {
		handleFlagError("preview", fs, err)
	}
	rest := fs.Args()
	if len(rest) < 1 {
		fmt.Fprintln(os.Stderr, "ccsession preview: session id required")
		fs.Usage()
		os.Exit(2)
	}
	if err := preview.Run(rest[0], preview.Options{Query: *queryFlag, Regex: *regexFlag}); err != nil {
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
//
// --no-hscroll anchors every row at its left edge. Without it, fzf scrolls a
// row sideways to reveal the matched substring, so a query that hits the label
// (the rightmost column) pushes the time+dir prefix off-screen behind a "··"
// ellipsis while a query that hits the dir keeps it visible. That left every
// row looking different depending on where the match landed; anchoring left
// keeps the time+dir prefix as the start of every row (the match may instead
// be truncated on the right, but the preview pane shows the full content).
//
// --color hl/hl+ are set to reverse video so fzf's own match highlight in
// the list (fuzzy/dir modes) matches the reverse-video highlight the preview
// applies to the query, instead of fzf's default colored background.
//
// If CCSESSION_EXCLUDE_DIR is set at startup, every list invocation
// (initial + every reload) is prefixed with --exclude-dir VALUE so the
// matching sessions never appear in the picker. Two forms are kept:
// exclude_args[] feeds the direct bash subshell with proper word splitting;
// $exclude_arg (printf %q) feeds the fzf --bind strings, which sh -c will
// later re-parse. The value is never typed inside the TUI, so it can't
// leak into screenshots. The ${arr[@]+...} idiom keeps macOS bash 3.2
// happy under set -u when the array is empty.
const defaultScript = `set -u
exclude_args=()
exclude_arg=""
if [ -n "${CCSESSION_EXCLUDE_DIR:-}" ]; then
  exclude_args=(--exclude-dir "$CCSESSION_EXCLUDE_DIR")
  exclude_arg=$(printf -- '--exclude-dir %q' "$CCSESSION_EXCLUDE_DIR")
fi
id=$("$CCSESSION_BIN" list --color=always "${exclude_args[@]+"${exclude_args[@]}"}" | fzf \
  --ansi \
  --delimiter=$'\t' \
  --with-nth=3,4,5 \
  --nth=1,2,3 \
  --no-sort \
  --no-hscroll \
  --color='hl:-1:reverse,hl+:-1:reverse' \
  --preview "$CCSESSION_BIN preview --query {q} {1}" \
  --preview-window=right,60%,wrap \
  --header='ctrl-g: grep / ctrl-o: dir / ctrl-f: fuzzy / enter: resume' \
  --bind 'start:unbind(change)' \
  --bind "change:reload(sleep 0.05; $CCSESSION_BIN list --color=always $exclude_arg --grep {q})" \
  --bind "ctrl-g:transform:echo \"change-prompt(grep> )+disable-search+reload(sleep 0.05; $CCSESSION_BIN list --color=always $exclude_arg --grep {q})+rebind(change)\"" \
  --bind "ctrl-o:transform:echo \"change-prompt(dir> )+enable-search+change-nth(2)+reload($CCSESSION_BIN list --color=always $exclude_arg)+unbind(change)\"" \
  --bind "ctrl-f:transform:echo \"change-prompt(> )+enable-search+change-nth(1,2,3)+reload($CCSESSION_BIN list --color=always $exclude_arg)+unbind(change)\"" \
  | awk -F'\t' '{print $1}') || true
if [ -n "$id" ]; then
  exec "$CCSESSION_BIN" resume "$id"
fi
`
