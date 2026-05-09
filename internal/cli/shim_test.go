package cli

import (
	"reflect"
	"testing"
)

func TestShimArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		argv []string
		want []string
	}{
		{"empty", nil, nil},
		{"xpc passes through", []string{"xpc", "exec", "ver"}, []string{"exec", "ver"}},
		{"xpcexec prepends exec", []string{"/usr/local/bin/xpcexec", "ver"}, []string{"exec", "ver"}},
		{"xpcreg prepends reg", []string{"xpcreg", "get", `HKLM\Foo`}, []string{"reg", "get", `HKLM\Foo`}},
		{"case-insensitive on Windows", []string{"XPCPS.EXE", "--filter", "xpc"}, []string{"ps", "--filter", "xpc"}},
		{"unknown basename passes through", []string{"weird", "args"}, []string{"args"}},
		{"shim with no extra args", []string{"xpcinfo"}, []string{"info"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ShimArgs(tt.argv)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ShimArgs(%v) = %v, want %v", tt.argv, got, tt.want)
			}
		})
	}
}
