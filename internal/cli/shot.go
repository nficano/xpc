package cli

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// xpc shot [<host:path>]
//
// Captures the desktop on the VM via Win32 BitBlt + GetDIBits (ctypes), saves
// a BMP locally to <host:path> (or ~/.xpc/shots/<timestamp>.bmp by default).

func newShotCmd(g *Globals) *cobra.Command {
	var outPath string
	c := &cobra.Command{
		Use:   "shot [<host-path>]",
		Short: "Capture a desktop screenshot from the VM and save it locally as a BMP.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			dst := outPath
			if dst == "" && len(args) > 0 {
				dst = args[0]
			}
			if dst == "" {
				home, _ := os.UserHomeDir()
				dst = filepath.Join(home, ".xpc", "shots", fmt.Sprintf("shot-%s.bmp",
					time.Now().UTC().Format("20060102T150405Z")))
			}

			py := shotPython()
			stdout, stderr, rc, err := runRemoteCmd(ctx, g, py, "python")
			if err != nil {
				return err
			}
			if err := requireSuccess(stdout, stderr, rc, "shot"); err != nil {
				return err
			}

			raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(stdout))
			if err != nil {
				return fmt.Errorf("decode bmp: %w", err)
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
				return fmt.Errorf("mkdir %s: %w", filepath.Dir(dst), err)
			}
			if err := os.WriteFile(dst, raw, 0o644); err != nil {
				return fmt.Errorf("write %s: %w", dst, err)
			}
			cmd.Printf("wrote %d bytes -> %s\n", len(raw), dst)
			return nil
		},
	}
	c.Flags().StringVarP(&outPath, "output", "o", "", "Local path to save the .bmp (default: ~/.xpc/shots/shot-<utc>.bmp)")
	return c
}

// shotPython returns a Python 3.4-compatible source that captures the
// virtual screen via Win32 BitBlt, packs a 24-bit BMP in memory, and prints
// the bytes base64-encoded to stdout. Adapted from xpctl's gui_screenshot.py.
func shotPython() string {
	return `
import base64, ctypes, struct, sys
from ctypes import wintypes

user32   = ctypes.windll.user32
gdi32    = ctypes.windll.gdi32

SM_XVIRTUALSCREEN  = 76
SM_YVIRTUALSCREEN  = 77
SM_CXVIRTUALSCREEN = 78
SM_CYVIRTUALSCREEN = 79
SRCCOPY  = 0x00CC0020
DIB_RGB_COLORS = 0
BI_RGB = 0

class BITMAPINFOHEADER(ctypes.Structure):
    _fields_ = [
        ("biSize", ctypes.c_uint32),
        ("biWidth", ctypes.c_int32),
        ("biHeight", ctypes.c_int32),
        ("biPlanes", ctypes.c_uint16),
        ("biBitCount", ctypes.c_uint16),
        ("biCompression", ctypes.c_uint32),
        ("biSizeImage", ctypes.c_uint32),
        ("biXPelsPerMeter", ctypes.c_int32),
        ("biYPelsPerMeter", ctypes.c_int32),
        ("biClrUsed", ctypes.c_uint32),
        ("biClrImportant", ctypes.c_uint32),
    ]

class BITMAPINFO(ctypes.Structure):
    _fields_ = [("bmiHeader", BITMAPINFOHEADER), ("bmiColors", ctypes.c_uint32 * 3)]

x  = user32.GetSystemMetrics(SM_XVIRTUALSCREEN)
y  = user32.GetSystemMetrics(SM_YVIRTUALSCREEN)
cx = user32.GetSystemMetrics(SM_CXVIRTUALSCREEN)
cy = user32.GetSystemMetrics(SM_CYVIRTUALSCREEN)

screen_dc = user32.GetDC(0)
mem_dc    = gdi32.CreateCompatibleDC(screen_dc)
bitmap    = gdi32.CreateCompatibleBitmap(screen_dc, cx, cy)
gdi32.SelectObject(mem_dc, bitmap)
gdi32.BitBlt(mem_dc, 0, 0, cx, cy, screen_dc, x, y, SRCCOPY)

stride = ((cx * 3 + 3) // 4) * 4
size   = stride * cy
buf    = (ctypes.c_ubyte * size)()

bmi = BITMAPINFO()
bmi.bmiHeader.biSize        = ctypes.sizeof(BITMAPINFOHEADER)
bmi.bmiHeader.biWidth       = cx
bmi.bmiHeader.biHeight      = cy
bmi.bmiHeader.biPlanes      = 1
bmi.bmiHeader.biBitCount    = 24
bmi.bmiHeader.biCompression = BI_RGB
bmi.bmiHeader.biSizeImage   = size

gdi32.GetDIBits(mem_dc, bitmap, 0, cy, ctypes.byref(buf),
                ctypes.byref(bmi), DIB_RGB_COLORS)

gdi32.DeleteObject(bitmap)
gdi32.DeleteDC(mem_dc)
user32.ReleaseDC(0, screen_dc)

bmp_header = struct.pack(
    '<2sIHHI', b'BM', 14 + 40 + size, 0, 0, 14 + 40,
)
bmp_dib = struct.pack(
    '<IiiHHIIIIII',
    40, cx, cy, 1, 24, 0, size, 2835, 2835, 0, 0,
)
data = bmp_header + bmp_dib + bytes(buf)
sys.stdout.write(base64.b64encode(data).decode('ascii'))
`
}
