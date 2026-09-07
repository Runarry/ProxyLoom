package xray

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestValidateAndRunSpecsUsePinnedArgv(t *testing.T) {
	id := ir.ID("42f06ab1-ac85-524b-87e5-6bc1e9a82409")
	directory := t.TempDir()
	core := Adapter{ExecutableID: id}
	workspace := adapter.JobWorkspace{Directory: directory, ConfigFilename: ConfigFilename}
	check, err := core.ValidateSpec(workspace)
	if err != nil {
		t.Fatal(err)
	}
	run, err := core.RunSpec(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(check.Args, []string{"run", "-test", "-c", "config.json"}) {
		t.Fatalf("validate argv %v", check.Args)
	}
	if !slices.Equal(run.Args, []string{"run", "-c", "config.json"}) {
		t.Fatalf("run argv %v", run.Args)
	}
	if check.WorkingDir != directory || check.ExecutableID != id {
		t.Fatal("spec escaped the runner allocation")
	}
	workspace.ConfigFilename = filepath.Join("..", "config.json")
	if _, err := core.ValidateSpec(workspace); err == nil {
		t.Fatal("parent path accepted")
	}
	if _, err := (Adapter{}).ValidateSpec(adapter.JobWorkspace{Directory: directory, ConfigFilename: ConfigFilename}); err == nil {
		t.Fatal("empty executable accepted")
	}
}
