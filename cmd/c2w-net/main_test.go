package main

import (
	"reflect"
	"testing"
)

func TestWasmtimeArgs(t *testing.T) {
	tests := []struct {
		name  string
		cli13 bool
		env   []string
		args  []string
		want  []string
	}{
		{
			name: "new CLI",
			env:  []string{"APP_MODE=production", "EMPTY="},
			args: []string{"image.wasm", "--net=socket", "sh"},
			want: []string{"run", "-S", "preview2=n", "-S", "tcplisten=127.0.0.1:1234", "--env='LISTEN_FDS=1'", "--env=APP_MODE=production", "--env=EMPTY=", "--", "image.wasm", "--net=socket", "sh"},
		},
		{
			name:  "old CLI",
			cli13: true,
			env:   []string{"APP_MODE=production"},
			args:  []string{"image.wasm"},
			want:  []string{"run", "--tcplisten=127.0.0.1:1234", "--env='LISTEN_FDS=1'", "--env=APP_MODE=production", "--", "image.wasm"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := wasmtimeArgs(tt.cli13, "127.0.0.1:1234", tt.env, tt.args); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("wasmtimeArgs() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestEnvFlagSet(t *testing.T) {
	for _, tt := range []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "key and value", value: "APP_MODE=production"},
		{name: "empty value", value: "EMPTY="},
		{name: "value contains equals", value: "URL=https://example.test?a=b"},
		{name: "missing equals", value: "APP_MODE", wantErr: true},
		{name: "empty key", value: "=production", wantErr: true},
		{name: "NUL", value: "APP_MODE=prod\x00uction", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var flags envFlag
			err := flags.Set(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Set(%q) error = %v, wantErr %v", tt.value, err, tt.wantErr)
			}
			if tt.wantErr && len(flags) != 0 {
				t.Fatalf("Set(%q) appended invalid value: %#v", tt.value, flags)
			}
		})
	}
}
