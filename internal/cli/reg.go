package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newRegCmd(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reg",
		Short: "Windows registry operations (reg.exe wrapper).",
	}
	cmd.AddCommand(newRegGetCmd(g))
	cmd.AddCommand(newRegSetCmd(g))
	cmd.AddCommand(newRegDeleteCmd(g))
	cmd.AddCommand(newRegExportCmd(g))
	return cmd
}

func newRegGetCmd(g *Globals) *cobra.Command {
	var (
		valueName string
		recurse   bool
	)
	c := &cobra.Command{
		Use:   "get <key>",
		Short: "Read a registry key (reg query).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			argv := []string{"reg", "query", args[0]}
			if valueName != "" {
				argv = append(argv, "/v", valueName)
			}
			if recurse {
				argv = append(argv, "/s")
			}
			return runRegPassthrough(cmd, g, argv, "reg query")
		},
	}
	c.Flags().StringVar(&valueName, "value", "", "Restrict output to a specific value name")
	c.Flags().BoolVar(&recurse, "recurse", false, "Recurse into subkeys (reg query /s)")
	return c
}

func newRegSetCmd(g *Globals) *cobra.Command {
	var (
		dataType string
		force    bool
	)
	c := &cobra.Command{
		Use:   "set <key> <name> <data>",
		Short: "Write a registry value (reg add).",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			if g.DryRun {
				cmd.Printf("(dry-run) reg add %s /v %s /t %s /d %s%s\n",
					args[0], args[1], dataType, args[2], forceFlag(force))
				return nil
			}
			argv := []string{"reg", "add", args[0], "/v", args[1], "/t", dataType, "/d", args[2]}
			if force {
				argv = append(argv, "/f")
			}
			return runRegPassthrough(cmd, g, argv, "reg add")
		},
	}
	c.Flags().StringVar(&dataType, "type", "REG_SZ", "Value type: REG_SZ | REG_DWORD | REG_BINARY | ...")
	c.Flags().BoolVar(&force, "force", true, "Overwrite without prompting (/f)")
	return c
}

func newRegDeleteCmd(g *Globals) *cobra.Command {
	var (
		valueName string
		force     bool
	)
	c := &cobra.Command{
		Use:   "delete <key>",
		Short: "Delete a registry key or value (reg delete).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if g.DryRun {
				suffix := ""
				if valueName != "" {
					suffix = " /v " + quoteRegArg(valueName)
				}
				cmd.Printf("(dry-run) reg delete %s%s%s\n", args[0], suffix, forceFlag(force))
				return nil
			}
			argv := []string{"reg", "delete", args[0]}
			if valueName != "" {
				argv = append(argv, "/v", valueName)
			}
			if force {
				argv = append(argv, "/f")
			}
			return runRegPassthrough(cmd, g, argv, "reg delete")
		},
	}
	c.Flags().StringVar(&valueName, "value", "", "Delete only this value (omit to delete the whole key)")
	c.Flags().BoolVar(&force, "force", false, "Skip confirmation (/f)")
	return c
}

func newRegExportCmd(g *Globals) *cobra.Command {
	c := &cobra.Command{
		Use:   "export <key> <vm-path>",
		Short: "Export a registry subtree to a .reg file on the VM (reg export).",
		Long: `The .reg file is written to <vm-path> on the VM. Use ` + "`xpc cp`" + ` to
pull it locally afterward.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if g.DryRun {
				cmd.Printf("(dry-run) reg export %s %s /y\n", args[0], args[1])
				return nil
			}
			argv := []string{"reg", "export", args[0], args[1], "/y"}
			return runRegPassthrough(cmd, g, argv, "reg export")
		},
	}
	return c
}

// runRegPassthrough runs reg.exe via python's subprocess (argv form, no
// cmd.exe wrapping). This sidesteps the Windows command-line quoting bug
// when registry paths contain spaces or backslashes (e.g. "Windows NT").
func runRegPassthrough(cmd *cobra.Command, g *Globals, argv []string, what string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	py := buildSubprocessPy(argv)
	stdout, stderr, rc, err := runRemoteCmd(ctx, g, py, "python")
	if err != nil {
		return err
	}
	if err := requireSuccess(stdout, stderr, rc, what); err != nil {
		return err
	}
	cmd.Print(strings.TrimRight(stdout, "\r\n") + "\n")
	return nil
}

// buildSubprocessPy emits a tiny python source that runs argv via
// subprocess.Popen with shell=False, prints stdout, and exits with the
// child's return code. Each argv element is quoted with Python's repr so
// backslashes survive untouched.
func buildSubprocessPy(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = pythonRepr(a)
	}
	return fmt.Sprintf(
		"import subprocess,sys\n"+
			"argv=[%s]\n"+
			"p=subprocess.Popen(argv,stdout=subprocess.PIPE,stderr=subprocess.STDOUT)\n"+
			"out,_=p.communicate()\n"+
			"sys.stdout.write(out.decode('utf-8','replace'))\n"+
			"sys.exit(p.returncode)",
		strings.Join(parts, ","))
}

// pythonRepr returns a Python string literal that decodes back to the input.
// We use double-quotes and explicit \\ for backslashes to keep things simple.
func pythonRepr(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString("\\\\")
		case '"':
			b.WriteString("\\\"")
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case '\t':
			b.WriteString("\\t")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// quoteRegArg wraps a registry path or value in quotes if it contains spaces
// or special chars. cmd.exe's reg.exe is happy to receive everything quoted.
func quoteRegArg(s string) string {
	if s == "" {
		return "\"\""
	}
	if !strings.ContainsAny(s, " \t\\\"") {
		return s
	}
	// Escape interior quotes per cmd.exe rules.
	return "\"" + strings.ReplaceAll(s, "\"", "\\\"") + "\""
}

func forceFlag(force bool) string {
	if force {
		return " /f"
	}
	return ""
}
