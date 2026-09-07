package singbox

import (
	"slices"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestValidateAndRunSpecsUsePinnedArgv(t *testing.T) {
	id := ir.ID("ebdd7efd-a425-5eb7-a63b-88667909c4b0")
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
	if !slices.Equal(check.Args, []string{"check", "-c", "config.json"}) {
		t.Fatalf("validate argv %v", check.Args)
	}
	if !slices.Equal(run.Args, []string{"run", "-c", "config.json"}) {
		t.Fatalf("run argv %v", run.Args)
	}
}
