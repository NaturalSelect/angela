package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/NaturalSelect/angela/internal/profiling"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

const (
	defaultProfileSeconds = 30
	stdoutOutput          = "-"
)

// profileFormat is how a snapshot profile is encoded.
type profileFormat string

const (
	formatProto profileFormat = "proto"
	formatText  profileFormat = "text"
	formatFull  profileFormat = "full"
)

// debugLevel is the pprof "debug" query value that selects the format.
func (f profileFormat) debugLevel() int {
	switch f {
	case formatText:
		return 1
	case formatFull:
		return 2
	default:
		return 0
	}
}

func (f profileFormat) extension() string {
	if f == formatProto {
		return "pb.gz"
	}
	return "txt"
}

func parseProfileFormat(p profiling.Profile, raw string) (profileFormat, error) {
	f := profileFormat(raw)
	switch {
	case f == formatProto || f == formatText:
		return f, nil
	case f == formatFull && p.Name == "goroutine":
		return f, nil
	case f == formatFull:
		return "", fmt.Errorf("format %q is only available for the goroutine profile", f)
	default:
		return "", fmt.Errorf("unknown format %q (want %s, %s or %s)", raw, formatProto, formatText, formatFull)
	}
}

func init() {
	pprofCmd.AddCommand(pprofListCmd)
	for _, p := range profiling.Profiles {
		pprofCmd.AddCommand(newProfileCmd(p))
	}
	rootCmd.AddCommand(pprofCmd)
}

var pprofCmd = &cobra.Command{
	Use:   "pprof",
	Short: "Export runtime profiles of a running angela process",
	Long: `Export goroutine, CPU, memory and other runtime profiles of a running
angela process without opening a network port. Every interactive session,
"angela run" and "angela server" process serves its profiles on a private
endpoint that only the current user can reach.

Snapshot profiles are written in the binary format read by "go tool pprof".
Use --format text or --format full for something readable directly.`,
	Example: `
# List the processes whose profiles can be exported
angela pprof list

# Dump all goroutine stacks of a hung session as readable text
angela pprof goroutine --format full -o -

# Record 10 seconds of CPU and open it in the pprof web UI
angela pprof cpu --seconds 10 -o cpu.pb.gz && go tool pprof -http :6061 cpu.pb.gz

# Capture the heap of a specific process
angela pprof heap --pid 12345 -o heap.pb.gz
`,
}

var pprofListCmd = &cobra.Command{
	Use:   "list",
	Short: "List angela processes whose profiles can be exported",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		procs, err := profiling.Running(cmd.Context())
		if err != nil {
			return err
		}
		if len(procs) == 0 {
			return errNoProfileProcess
		}

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "PID\tCOMMAND")
		for _, proc := range procs {
			fmt.Fprintf(w, "%d\t%s\n", proc.PID, proc.Cmdline)
		}
		return w.Flush()
	},
}

var errNoProfileProcess = errors.New("no running angela process is serving profiles")

func newProfileCmd(p profiling.Profile) *cobra.Command {
	var (
		pid     int
		output  string
		seconds int
		format  string
	)

	c := &cobra.Command{
		Use:   p.Name,
		Short: p.Short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			req := profiling.Request{Profile: p}
			ext := "pb.gz"
			switch {
			case p.Name == "trace":
				ext = "trace"
				fallthrough
			case p.Timed:
				if seconds <= 0 {
					return fmt.Errorf("--seconds must be positive, got %d", seconds)
				}
				req.Seconds = seconds
			default:
				f, err := parseProfileFormat(p, format)
				if err != nil {
					return err
				}
				req.Debug = f.debugLevel()
				ext = f.extension()
			}
			binary := req.Debug == 0

			if output == stdoutOutput && binary && term.IsTerminal(os.Stdout.Fd()) {
				return errors.New("refusing to write a binary profile to the terminal; redirect stdout, use -o with a file name, or pick --format text")
			}

			var err error
			req.PID, err = resolveProfilePID(cmd.Context(), pid)
			if err != nil {
				return err
			}

			if p.Timed {
				fmt.Fprintf(cmd.ErrOrStderr(), "Recording %s profile of process %d for %ds...\n", p.Name, req.PID, req.Seconds)
			}
			body, err := profiling.Open(cmd.Context(), req)
			if err != nil {
				return fmt.Errorf("failed to export %s profile of process %d: %w", p.Name, req.PID, err)
			}
			defer body.Close()

			if output == stdoutOutput {
				_, err = io.Copy(cmd.OutOrStdout(), body)
				return err
			}
			if output == "" {
				output = fmt.Sprintf("%s-%d-%s.%s", p.Name, req.PID, time.Now().Format("20060102-150405"), ext)
			}
			if err := writeProfileFile(output, body); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), output)
			return nil
		},
	}

	c.Flags().IntVarP(&pid, "pid", "p", 0, "Process to export from; may be omitted when only one is running")
	c.Flags().StringVarP(&output, "output", "o", "", `File to write; "-" writes to stdout (default: <profile>-<pid>-<time> in the current directory)`)
	if p.Timed {
		c.Flags().IntVar(&seconds, "seconds", defaultProfileSeconds, "How long to record, in seconds")
	} else {
		c.Flags().StringVar(&format, "format", string(formatProto), "Output format: proto (for go tool pprof), text, or full (goroutine only: complete stack dump)")
	}
	return c
}

// resolveProfilePID picks the process to export from. An explicit pid must
// be serving profiles; without one, the choice has to be unambiguous.
func resolveProfilePID(ctx context.Context, pid int) (int, error) {
	procs, err := profiling.Running(ctx)
	if err != nil {
		return 0, err
	}

	if pid != 0 {
		for _, proc := range procs {
			if proc.PID == pid {
				return pid, nil
			}
		}
		return 0, fmt.Errorf("process %d is not serving profiles; run \"angela pprof list\" to see which are", pid)
	}

	switch len(procs) {
	case 0:
		return 0, errNoProfileProcess
	case 1:
		return procs[0].PID, nil
	default:
		var b strings.Builder
		for _, proc := range procs {
			fmt.Fprintf(&b, "\n  %d  %s", proc.PID, proc.Cmdline)
		}
		return 0, fmt.Errorf("several angela processes are running, choose one with --pid:%s", b.String())
	}
}

// writeProfileFile removes the file again when the copy fails, so an
// interrupted recording does not leave a truncated profile behind.
func writeProfileFile(path string, body io.Reader) (err error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer func() {
		err = errors.Join(err, f.Close())
		if err != nil {
			_ = os.Remove(path)
		}
	}()

	if _, err := io.Copy(f, body); err != nil {
		return fmt.Errorf("failed to write profile: %w", err)
	}
	return nil
}

// serveProfiles makes the profiles of this process exportable with
// "angela pprof" until the returned function is called. A failure only
// costs that ability, so it is logged rather than returned.
func serveProfiles() func() {
	stop, err := profiling.Start()
	if err != nil {
		slog.Warn("Failed to start profile endpoint", "error", err)
		return func() {}
	}
	return stop
}
