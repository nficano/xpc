// Package cli implements the xpc cobra command tree.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/nficano/xpc/internal/profile"
	"github.com/nficano/xpc/internal/version"
)

// Globals are populated from cobra flags at the root level and consumed by
// every subcommand.
type Globals struct {
	ProfileName string
	HostFlag    string
	PortFlag    int
	OutputMode  string // text | json | table
	Verbose     bool
	Timeout     time.Duration
	DryRun      bool

	// Resolved at first read (lazy).
	resolvedProfile *profile.Profile
}

// New returns the root cobra command with all subcommands registered.
func New() *cobra.Command {
	g := &Globals{}

	root := &cobra.Command{
		Use:           "xpc",
		Short:         "Modern remote management toolkit for Windows XP VMs.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.String(),
	}
	root.SetVersionTemplate("{{.Version}}\n")
	root.PersistentFlags().StringVar(&g.ProfileName, "profile", "", "Profile name (default: $XPC_PROFILE or active state)")
	root.PersistentFlags().StringVar(&g.HostFlag, "host", "", "Override profile host")
	root.PersistentFlags().IntVar(&g.PortFlag, "port", 0, "Override profile port (0 = use profile)")
	root.PersistentFlags().StringVar(&g.OutputMode, "output", "text", "Output format: text | json | table")
	root.PersistentFlags().BoolVarP(&g.Verbose, "verbose", "v", false, "Verbose output")
	root.PersistentFlags().DurationVar(&g.Timeout, "timeout", 0, "Per-invocation timeout (0 = none)")
	root.PersistentFlags().BoolVar(&g.DryRun, "dry-run", false, "Show planned actions without executing them")

	root.AddCommand(newVersionCmd(g))
	root.AddCommand(newConfigureCmd(g))
	root.AddCommand(newUseCmd(g))
	root.AddCommand(newProfileCmd(g))
	root.AddCommand(newCompletionCmd(g))
	root.AddCommand(newMigrateCmd(g))
	root.AddCommand(newExecCmd(g))
	root.AddCommand(newBootstrapCmd(g))
	root.AddCommand(newAgentCmd(g))
	// Phase 6 subcommands.
	root.AddCommand(newInfoCmd(g))
	root.AddCommand(newNetCmd(g))
	root.AddCommand(newPsCmd(g))
	root.AddCommand(newRegCmd(g))
	root.AddCommand(newSvcCmd(g))
	root.AddCommand(newEnvCmd(g))
	root.AddCommand(newBatCmd(g))
	root.AddCommand(newEvtCmd(g))
	root.AddCommand(newWatchCmd(g))
	root.AddCommand(newPyCmd(g))
	root.AddCommand(newDllCmd(g))
	root.AddCommand(newCpCmd(g))
	root.AddCommand(newShotCmd(g))
	root.AddCommand(newFetchCmd(g))
	root.AddCommand(newEditCmd(g))
	root.AddCommand(newBootCmd(g))
	root.AddCommand(newSendCmd(g))
	root.AddCommand(newInjCmd(g))
	root.AddCommand(newDumpCmd(g))
	// Filesystem helpers (xpctl extras renamed to top-level).
	root.AddCommand(newCatCmd(g))
	root.AddCommand(newHeadCmd(g))
	root.AddCommand(newTailCmd(g))
	root.AddCommand(newFindCmd(g))
	root.AddCommand(newSumCmd(g))

	return root
}

// Execute runs the CLI and returns a process exit code per the
// docs/ARCHITECTURE.md exit-code table.
func Execute(args []string, stdout, stderr io.Writer) int {
	cmd := New()
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)

	if err := cmd.Execute(); err != nil {
		return mapExitCode(err)
	}
	return 0
}

// ResolveProfile lazy-loads the profile this invocation should use, applying
// the --profile flag, $XPC_PROFILE env var, and active-state fallback.
func (g *Globals) ResolveProfile() (*profile.Profile, error) {
	if g.resolvedProfile != nil {
		return g.resolvedProfile, nil
	}
	name := g.ProfileName
	if name == "" {
		name = os.Getenv("XPC_PROFILE")
	}
	if name == "" {
		active, err := profile.Active()
		if err != nil {
			return nil, err
		}
		name = active
	}
	p, err := profile.Load(name)
	if err != nil {
		return nil, err
	}
	if g.HostFlag != "" {
		p.Host = g.HostFlag
	}
	if g.PortFlag != 0 {
		p.Port = g.PortFlag
	}
	g.resolvedProfile = p
	return p, nil
}

// ---- exit codes -----------------------------------------------------------

// UsageError is a sentinel for invalid CLI usage; mapExitCode returns 2.
type UsageError struct{ error }

// ConnectionError is a sentinel for transport-level failures; mapExitCode returns 3.
type ConnectionError struct{ error }

// AuthError is a sentinel for HMAC / fingerprint / cert failures; mapExitCode returns 4.
type AuthError struct{ error }

// RemoteError is a sentinel for failures reported by the remote agent;
// mapExitCode returns ExitCode (or 5 if zero).
type RemoteError struct {
	error
	ExitCode int
}

func (e *UsageError) Unwrap() error      { return e.error }
func (e *ConnectionError) Unwrap() error { return e.error }
func (e *AuthError) Unwrap() error       { return e.error }
func (e *RemoteError) Unwrap() error     { return e.error }

func wrapUsage(err error) error      { return &UsageError{err} }
func wrapConnection(err error) error { return &ConnectionError{err} }
func wrapAuth(err error) error       { return &AuthError{err} }

func mapExitCode(err error) int {
	if err == nil {
		return 0
	}
	var u *UsageError
	if errors.As(err, &u) {
		fmt.Fprintln(os.Stderr, "xpc:", u.Error())
		return 2
	}
	var c *ConnectionError
	if errors.As(err, &c) {
		fmt.Fprintln(os.Stderr, "xpc: connection error:", c.Error())
		return 3
	}
	var a *AuthError
	if errors.As(err, &a) {
		fmt.Fprintln(os.Stderr, "xpc: auth error:", a.Error())
		return 4
	}
	var r *RemoteError
	if errors.As(err, &r) {
		fmt.Fprintln(os.Stderr, "xpc: remote error:", r.Error())
		if r.ExitCode > 0 {
			return r.ExitCode
		}
		return 5
	}
	fmt.Fprintln(os.Stderr, "xpc:", err)
	return 1
}
