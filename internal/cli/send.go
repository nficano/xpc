package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// xpc send {keys, click, move}
//
// Synthetic input via Win32 SendInput. Adapted from xpctl/assets/scripts/
// gui_sendkeys.py. Runs through --shell python so the host doesn't need
// agent-side handlers.

func newSendCmd(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "send",
		Short: "Synthetic input on the VM (keys, mouse).",
	}
	cmd.AddCommand(newSendKeysCmd(g))
	cmd.AddCommand(newSendClickCmd(g))
	cmd.AddCommand(newSendMoveCmd(g))
	return cmd
}

func newSendKeysCmd(g *Globals) *cobra.Command {
	var (
		title string
		delay int
	)
	c := &cobra.Command{
		Use:   "keys -- <text>",
		Short: "Type a string of characters into the foreground window (or one matched by --title).",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			text := strings.Join(args, " ")
			py := fmt.Sprintf(
				`import ctypes,time
user32=ctypes.windll.user32
title=%[2]q
text=%[1]q
delay=%[3]d/1000.0
if title:
    h=user32.FindWindowW(None, title)
    if h: user32.SetForegroundWindow(h)
for ch in text:
    vk=user32.VkKeyScanW(ord(ch))
    if vk==-1: continue
    sc=user32.MapVirtualKeyW(vk & 0xff, 0)
    user32.keybd_event(vk & 0xff, sc, 0, 0)
    user32.keybd_event(vk & 0xff, sc, 2, 0)
    if delay: time.sleep(delay)
print('sent', len(text), 'chars')`,
				text, title, delay)
			return runFsPython(cmd, g, py, "send keys")
		},
	}
	c.Flags().StringVar(&title, "title", "", "Focus this exact window title before typing")
	c.Flags().IntVar(&delay, "delay-ms", 0, "Sleep between keystrokes")
	return c
}

func newSendClickCmd(g *Globals) *cobra.Command {
	var (
		x, y    int
		button  string
		doubleC bool
	)
	c := &cobra.Command{
		Use:   "click",
		Short: "Synthesize a mouse click at (x, y) on the VM.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			down, up := mouseFlagsFor(button)
			py := fmt.Sprintf(
				`import ctypes,time
user32=ctypes.windll.user32
user32.SetCursorPos(%[1]d, %[2]d)
time.sleep(0.02)
user32.mouse_event(%[3]d, 0, 0, 0, 0)
time.sleep(0.02)
user32.mouse_event(%[4]d, 0, 0, 0, 0)
time.sleep(0.02)
if %[5]s:
    user32.mouse_event(%[3]d, 0, 0, 0, 0)
    time.sleep(0.02)
    user32.mouse_event(%[4]d, 0, 0, 0, 0)
print('clicked %[1]d,%[2]d')`,
				x, y, down, up, pyBool(doubleC))
			return runFsPython(cmd, g, py, "send click")
		},
	}
	c.Flags().IntVar(&x, "x", 0, "Cursor X coordinate")
	c.Flags().IntVar(&y, "y", 0, "Cursor Y coordinate")
	c.Flags().StringVar(&button, "button", "left", "Button: left | right | middle")
	c.Flags().BoolVar(&doubleC, "double", false, "Double-click")
	return c
}

func newSendMoveCmd(g *Globals) *cobra.Command {
	var x, y int
	c := &cobra.Command{
		Use:   "move",
		Short: "Move the cursor to (x, y) on the VM.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			py := fmt.Sprintf(
				`import ctypes
ctypes.windll.user32.SetCursorPos(%d, %d)
print('moved %d,%d')`, x, y, x, y)
			return runFsPython(cmd, g, py, "send move")
		},
	}
	c.Flags().IntVar(&x, "x", 0, "Cursor X coordinate")
	c.Flags().IntVar(&y, "y", 0, "Cursor Y coordinate")
	return c
}

func mouseFlagsFor(button string) (int, int) {
	switch button {
	case "right":
		return 0x0008, 0x0010 // RIGHTDOWN, RIGHTUP
	case "middle":
		return 0x0020, 0x0040 // MIDDLEDOWN, MIDDLEUP
	default:
		return 0x0002, 0x0004 // LEFTDOWN, LEFTUP
	}
}

func pyBool(b bool) string {
	if b {
		return "True"
	}
	return "False"
}
