package cli

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nficano/xpc/internal/profile"
)

// xpc snap list|create|restore|delete
//
// Talks to the Proxmox PVE HTTP API at https://<proxmox_host>:8006/api2/json/.
// Auth is by API token (Proxmox -> Datacenter -> API Tokens). The token id
// has the form "user@realm!tokenname"; the token secret is a UUID.
//
// Profile fields:
//
//	proxmox_host    Proxmox node hostname or IP
//	proxmox_user    Token id, e.g. "root@pam!xpc"
//	proxmox_node    Proxmox node name (for the API path)
//	proxmox_vmid    QEMU VM id (integer string)
//	proxmox_token   The token secret (only stored in ~/.xpc/credentials)
//
// All four are read from the active profile. Override with --proxmox-host,
// --proxmox-user, --proxmox-token, --proxmox-node, or set $XPC_PROXMOX_*.

func newSnapCmd(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snap",
		Short: "Proxmox VM snapshot operations.",
	}
	cmd.AddCommand(newSnapListCmd(g))
	cmd.AddCommand(newSnapCreateCmd(g))
	cmd.AddCommand(newSnapRestoreCmd(g))
	cmd.AddCommand(newSnapDeleteCmd(g))
	return cmd
}

type proxmoxConfig struct {
	Host     string
	Node     string
	VMID     string
	TokenID  string
	Secret   string
	Insecure bool
}

func resolveProxmox(g *Globals, cmd *cobra.Command) (*proxmoxConfig, error) {
	p, err := g.ResolveProfile()
	if err != nil {
		return nil, err
	}
	c := &proxmoxConfig{
		Host:    p.ProxmoxHost,
		TokenID: p.ProxmoxUser,
	}
	// Pull from env / flags. Flags take precedence.
	if v := os.Getenv("XPC_PROXMOX_HOST"); v != "" {
		c.Host = v
	}
	if v, _ := cmd.Flags().GetString("proxmox-host"); v != "" {
		c.Host = v
	}
	if v := os.Getenv("XPC_PROXMOX_USER"); v != "" {
		c.TokenID = v
	}
	if v, _ := cmd.Flags().GetString("proxmox-user"); v != "" {
		c.TokenID = v
	}
	c.Secret = os.Getenv("XPC_PROXMOX_TOKEN")
	if v, _ := cmd.Flags().GetString("proxmox-token"); v != "" {
		c.Secret = v
	}
	if c.Secret == "" {
		// Try profile credentials field (we'll add a generic free-form
		// "proxmox_token" key in ~/.xpc/credentials if the user opts to
		// store it there).
		if v := readCredsKey(p, "proxmox_token"); v != "" {
			c.Secret = v
		}
	}
	c.Node = os.Getenv("XPC_PROXMOX_NODE")
	if v, _ := cmd.Flags().GetString("proxmox-node"); v != "" {
		c.Node = v
	}
	c.VMID = os.Getenv("XPC_PROXMOX_VMID")
	if v, _ := cmd.Flags().GetString("proxmox-vmid"); v != "" {
		c.VMID = v
	}
	if v, _ := cmd.Flags().GetBool("proxmox-insecure"); v {
		c.Insecure = true
	}

	if c.Host == "" {
		return nil, wrapUsage(fmt.Errorf("proxmox host not set: pass --proxmox-host or set proxmox_host in the profile"))
	}
	if c.TokenID == "" {
		return nil, wrapUsage(fmt.Errorf("proxmox token id not set: pass --proxmox-user or set proxmox_user in the profile"))
	}
	if c.Secret == "" {
		return nil, wrapUsage(fmt.Errorf("proxmox token secret not set: pass --proxmox-token or set $XPC_PROXMOX_TOKEN"))
	}
	if c.Node == "" {
		return nil, wrapUsage(fmt.Errorf("proxmox node not set: pass --proxmox-node or set $XPC_PROXMOX_NODE"))
	}
	if c.VMID == "" {
		return nil, wrapUsage(fmt.Errorf("proxmox VM id not set: pass --proxmox-vmid or set $XPC_PROXMOX_VMID"))
	}
	return c, nil
}

// readCredsKey is a placeholder; the profile package only models known keys.
// For Phase 7 we just return "" and tell users to set $XPC_PROXMOX_TOKEN.
func readCredsKey(_ *profile.Profile, _ string) string {
	return ""
}

func addProxmoxFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().String("proxmox-host", "", "Proxmox node host (overrides profile.proxmox_host)")
	cmd.PersistentFlags().String("proxmox-user", "", "Proxmox token id, e.g. root@pam!xpc")
	cmd.PersistentFlags().String("proxmox-token", "", "Proxmox token secret (or set $XPC_PROXMOX_TOKEN)")
	cmd.PersistentFlags().String("proxmox-node", "", "Proxmox node name")
	cmd.PersistentFlags().String("proxmox-vmid", "", "QEMU VM id")
	cmd.PersistentFlags().Bool("proxmox-insecure", false, "Skip TLS verification of the Proxmox API endpoint")
}

func proxmoxRequest(ctx context.Context, c *proxmoxConfig, method, path string, body url.Values) (map[string]any, error) {
	endpoint := fmt.Sprintf("https://%s:8006%s", c.Host, path)
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(body.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "PVEAPIToken="+c.TokenID+"="+c.Secret)
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: c.Insecure}, //nolint:gosec
		},
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, wrapConnection(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("proxmox %s %s: HTTP %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var parsed map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return nil, fmt.Errorf("proxmox json decode: %w", err)
		}
	}
	return parsed, nil
}

func newSnapListCmd(g *Globals) *cobra.Command {
	c := &cobra.Command{
		Use:   "list",
		Short: "List snapshots for the configured VM.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			pc, err := resolveProxmox(g, cmd)
			if err != nil {
				return err
			}
			path := fmt.Sprintf("/api2/json/nodes/%s/qemu/%s/snapshot",
				url.PathEscape(pc.Node), url.PathEscape(pc.VMID))
			out, err := proxmoxRequest(ctx, pc, "GET", path, nil)
			if err != nil {
				return err
			}
			data, _ := out["data"].([]any)
			if g.OutputMode == "json" {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(data)
			}
			if len(data) == 0 {
				cmd.Println("(no snapshots)")
				return nil
			}
			cmd.Printf("%-25s %-25s %s\n", "NAME", "PARENT", "DESCRIPTION")
			for _, raw := range data {
				e, _ := raw.(map[string]any)
				name, _ := e["name"].(string)
				parent, _ := e["parent"].(string)
				desc, _ := e["description"].(string)
				cmd.Printf("%-25s %-25s %s\n", name, parent, desc)
			}
			return nil
		},
	}
	addProxmoxFlags(c)
	return c
}

func newSnapCreateCmd(g *Globals) *cobra.Command {
	var includeVMState bool
	c := &cobra.Command{
		Use:   "create <name> [description]",
		Short: "Create a snapshot on the configured VM.",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			pc, err := resolveProxmox(g, cmd)
			if err != nil {
				return err
			}
			if g.DryRun {
				cmd.Printf("(dry-run) POST /nodes/%s/qemu/%s/snapshot snapname=%s vmstate=%v\n",
					pc.Node, pc.VMID, args[0], includeVMState)
				return nil
			}
			path := fmt.Sprintf("/api2/json/nodes/%s/qemu/%s/snapshot",
				url.PathEscape(pc.Node), url.PathEscape(pc.VMID))
			form := url.Values{}
			form.Set("snapname", args[0])
			if includeVMState {
				form.Set("vmstate", "1")
			}
			if len(args) > 1 {
				form.Set("description", args[1])
			}
			out, err := proxmoxRequest(ctx, pc, "POST", path, form)
			if err != nil {
				return err
			}
			cmd.Printf("snapshot create: %v\n", out["data"])
			return nil
		},
	}
	c.Flags().BoolVar(&includeVMState, "vmstate", false, "Include the VM state (memory) in the snapshot")
	addProxmoxFlags(c)
	return c
}

func newSnapRestoreCmd(g *Globals) *cobra.Command {
	var startAfter bool
	c := &cobra.Command{
		Use:   "restore <name>",
		Short: "Roll back the configured VM to <name>.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			pc, err := resolveProxmox(g, cmd)
			if err != nil {
				return err
			}
			if g.DryRun {
				cmd.Printf("(dry-run) POST /nodes/%s/qemu/%s/snapshot/%s/rollback start=%v\n",
					pc.Node, pc.VMID, args[0], startAfter)
				return nil
			}
			path := fmt.Sprintf("/api2/json/nodes/%s/qemu/%s/snapshot/%s/rollback",
				url.PathEscape(pc.Node), url.PathEscape(pc.VMID), url.PathEscape(args[0]))
			form := url.Values{}
			if startAfter {
				form.Set("start", "1")
			}
			out, err := proxmoxRequest(ctx, pc, "POST", path, form)
			if err != nil {
				return err
			}
			cmd.Printf("rollback: %v\n", out["data"])
			return nil
		},
	}
	c.Flags().BoolVar(&startAfter, "start", false, "Start the VM after rollback")
	addProxmoxFlags(c)
	return c
}

func newSnapDeleteCmd(g *Globals) *cobra.Command {
	c := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a snapshot.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			pc, err := resolveProxmox(g, cmd)
			if err != nil {
				return err
			}
			if g.DryRun {
				cmd.Printf("(dry-run) DELETE /nodes/%s/qemu/%s/snapshot/%s\n",
					pc.Node, pc.VMID, args[0])
				return nil
			}
			path := fmt.Sprintf("/api2/json/nodes/%s/qemu/%s/snapshot/%s",
				url.PathEscape(pc.Node), url.PathEscape(pc.VMID), url.PathEscape(args[0]))
			out, err := proxmoxRequest(ctx, pc, "DELETE", path, nil)
			if err != nil {
				return err
			}
			cmd.Printf("delete: %v\n", out["data"])
			return nil
		},
	}
	addProxmoxFlags(c)
	return c
}
