package mihomo

import (
	"slices"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestValidateAndRunSpecsUsePinnedArgv(t *testing.T) {
	id := ir.ID("81407b9e-61c5-5473-b359-29fa2730567e")
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
	if !slices.Equal(check.Args, []string{"-t", "-d", ".", "-f", "config.yaml"}) {
		t.Fatalf("validate argv %v", check.Args)
	}
	if !slices.Equal(run.Args, []string{"-d", ".", "-f", "config.yaml"}) {
		t.Fatalf("run argv %v", run.Args)
	}
	for _, arg := range append(check.Args, run.Args...) {
		if strings.Contains(arg, "post-up") || strings.Contains(arg, "post-down") || strings.Contains(arg, "/") {
			t.Fatalf("unsafe argument %q", arg)
		}
	}
}
