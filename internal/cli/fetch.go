package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// xpc fetch <url> [<vm-path>]
//
// Downloads <url> on the host, then ships the bytes to the VM via the same
// inline-base64 path xpc cp uses. Sidesteps the VM having to talk to the
// internet directly. If <vm-path> is omitted, the file lands at
// C:\xpc\downloads\<basename>.

func newFetchCmd(g *Globals) *cobra.Command {
	var fetchTimeout time.Duration
	c := &cobra.Command{
		Use:   "fetch <url> [<vm-path>]",
		Short: "Download a URL on the host and upload it to the VM.",
		Long: `Streams the URL to a local temp file, then sends it to the VM via the
same inline base64 path as xpc cp. Useful when the VM has no working internet
or HTTPS stack but the host does. The default destination is
C:\\xpc\\downloads\\<basename derived from the URL>.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			rawURL := args[0]
			parsed, err := url.Parse(rawURL)
			if err != nil {
				return wrapUsage(fmt.Errorf("invalid url: %w", err))
			}

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			if fetchTimeout == 0 {
				fetchTimeout = 5 * time.Minute
			}
			fetchCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
			defer cancel()

			req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, rawURL, nil)
			if err != nil {
				return err
			}
			req.Header.Set("User-Agent", "xpc/0.0.0-dev")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return wrapConnection(fmt.Errorf("fetch %s: %w", rawURL, err))
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode/100 != 2 {
				return fmt.Errorf("fetch %s: HTTP %d", rawURL, resp.StatusCode)
			}

			tmp, err := os.CreateTemp("", "xpc-fetch-*")
			if err != nil {
				return err
			}
			tmpPath := tmp.Name()
			defer func() {
				_ = tmp.Close()
				_ = os.Remove(tmpPath)
			}()
			n, err := io.Copy(tmp, resp.Body)
			if err != nil {
				return fmt.Errorf("write %s: %w", tmpPath, err)
			}
			if err := tmp.Close(); err != nil {
				return err
			}
			cmd.Printf("downloaded %d bytes from %s\n", n, rawURL)

			vmPath := ""
			if len(args) >= 2 {
				vmPath = stripVMPrefix(args[1])
			}
			if vmPath == "" {
				name := path.Base(parsed.Path)
				if name == "" || name == "/" || name == "." {
					name = "download.bin"
				}
				vmPath = `C:\xpc\downloads\` + name
			} else if !strings.ContainsAny(vmPath, `:`) {
				// User passed a bare relative path; assume it's relative to C:\xpc\downloads.
				vmPath = `C:\xpc\downloads\` + vmPath
			}

			return cpUpload(ctx, cmd, g, tmpPath, vmPath)
		},
	}
	c.Flags().DurationVar(&fetchTimeout, "fetch-timeout", 0, "HTTP fetch timeout (default 5m)")
	return c
}
