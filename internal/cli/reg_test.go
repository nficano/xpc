package cli

import (
	"reflect"
	"testing"
)

func TestParseRegQueryOutput(t *testing.T) {
	t.Parallel()
	in := "\r\nHKEY_LOCAL_MACHINE\\Software\\Microsoft\\Windows NT\\CurrentVersion\r\n" +
		"    ProductName    REG_SZ    Microsoft Windows XP\r\n" +
		"    CSDVersion    REG_SZ    Service Pack 3\r\n" +
		"    CurrentBuildNumber    REG_SZ    2600\r\n" +
		"    CurrentVersion    REG_SZ    5.1\r\n" +
		"    InstallDate    REG_DWORD    0x40e3a8d8\r\n" +
		"\r\n"
	got := parseRegQueryOutput(in)
	want := []RegEntry{
		{Key: `HKEY_LOCAL_MACHINE\Software\Microsoft\Windows NT\CurrentVersion`, Name: "ProductName", Type: "REG_SZ", Data: "Microsoft Windows XP"},
		{Key: `HKEY_LOCAL_MACHINE\Software\Microsoft\Windows NT\CurrentVersion`, Name: "CSDVersion", Type: "REG_SZ", Data: "Service Pack 3"},
		{Key: `HKEY_LOCAL_MACHINE\Software\Microsoft\Windows NT\CurrentVersion`, Name: "CurrentBuildNumber", Type: "REG_SZ", Data: "2600"},
		{Key: `HKEY_LOCAL_MACHINE\Software\Microsoft\Windows NT\CurrentVersion`, Name: "CurrentVersion", Type: "REG_SZ", Data: "5.1"},
		{Key: `HKEY_LOCAL_MACHINE\Software\Microsoft\Windows NT\CurrentVersion`, Name: "InstallDate", Type: "REG_DWORD", Data: "0x40e3a8d8"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestParseRegQueryOutput_MultipleKeys(t *testing.T) {
	t.Parallel()
	in := "HKLM\\A\r\n    foo    REG_SZ    1\r\n\r\nHKLM\\B\r\n    bar    REG_DWORD    0x2\r\n"
	got := parseRegQueryOutput(in)
	if len(got) != 2 || got[0].Key != "HKLM\\A" || got[1].Key != "HKLM\\B" {
		t.Fatalf("got %+v", got)
	}
}

func TestParseRegQueryOutput_Empty(t *testing.T) {
	t.Parallel()
	if got := parseRegQueryOutput(""); len(got) != 0 {
		t.Fatalf("expected zero entries, got %+v", got)
	}
}
