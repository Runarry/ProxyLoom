package adapter_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestCommandSpecBindsRegistryIdentityAndRunnerDirectory(t *testing.T) {
	id := ir.ID("44444444-4444-4444-8444-444444444444")
	directory := t.TempDir()
	valid := adapter.CommandSpec{ExecutableID: id, Args: []string{"check", "config.json"}, WorkingDir: directory}
	if err := valid.Validate(id, directory); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		edit func(*adapter.CommandSpec)
	}{
		{"unregistered executable", func(spec *adapter.CommandSpec) { spec.ExecutableID = "55555555-5555-4555-8555-555555555555" }},
		{"executable path", func(spec *adapter.CommandSpec) { spec.ExecutableID = "/bin/sh" }},
		{"other directory", func(spec *adapter.CommandSpec) { spec.WorkingDir = filepath.Join(directory, "other") }},
		{"relative directory", func(spec *adapter.CommandSpec) { spec.WorkingDir = "." }},
		{"NUL argument", func(spec *adapter.CommandSpec) { spec.Args[0] = "check\x00run" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := valid.Clone()
			test.edit(&spec)
			if spec.Validate(id, directory) == nil {
				t.Fatal("unsafe command identity or directory accepted")
			}
		})
	}
	if valid.Args[0] != "check" {
		t.Fatal("command clone changed original arguments")
	}
}

func TestArtifactCloneAndDefaultLoggingProtectBytes(t *testing.T) {
	artifact := adapter.Artifact{SnapshotID: "ffffffff-ffff-4fff-8fff-ffffffffffff", TargetKey: "xray-default", ContentType: "application/json", Bytes: []byte("NEVER_PRINT_ARTIFACT"), ContentHMAC: bytes.Repeat([]byte{1}, 32)}
	clone := artifact.Clone()
	clone.Bytes[0] = 'x'
	clone.ContentHMAC[0] = 2
	if artifact.Bytes[0] == 'x' || artifact.ContentHMAC[0] == 2 {
		t.Fatal("artifact clone exposes mutable aliases")
	}
	for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
		if strings.Contains(fmt.Sprintf(format, artifact), "NEVER_PRINT") {
			t.Fatal("artifact formatting leaked bytes")
		}
	}
	var output bytes.Buffer
	slog.New(slog.NewJSONHandler(&output, nil)).Info("test", "artifact", artifact)
	if strings.Contains(output.String(), "NEVER_PRINT") || strings.Contains(output.String(), "TkVWRVJfUFJJTlRfQVJUSUZBQ1Q") {
		t.Fatal("artifact structured logging leaked bytes")
	}
}
