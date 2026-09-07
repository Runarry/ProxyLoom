package singbox

import (
	"errors"
	"slices"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

const ConfigFilename = "config.json"

type Adapter struct {
	ExecutableID ir.ID
}

func (Adapter) Family() ir.CoreFamily { return ir.SingBox }

func (a Adapter) ValidateSpec(workspace adapter.JobWorkspace) (adapter.CommandSpec, error) {
	return a.spec(workspace, []string{"check", "-c", ConfigFilename})
}

func (a Adapter) RunSpec(workspace adapter.JobWorkspace) (adapter.CommandSpec, error) {
	return a.spec(workspace, []string{"run", "-c", ConfigFilename})
}

func (Adapter) RedactLog(line []byte) []byte { return adapter.RedactLogLine(line) }

func (a Adapter) spec(workspace adapter.JobWorkspace, args []string) (adapter.CommandSpec, error) {
	if err := workspace.Validate(); err != nil {
		return adapter.CommandSpec{}, err
	}
	if workspace.ConfigFilename != ConfigFilename {
		return adapter.CommandSpec{}, errors.New("sing-box config filename must be config.json")
	}
	if err := a.ExecutableID.Validate(); err != nil {
		return adapter.CommandSpec{}, errors.New("missing pinned executable identity")
	}
	spec := adapter.CommandSpec{ExecutableID: a.ExecutableID, Args: slices.Clone(args), WorkingDir: workspace.Directory}
	if err := spec.Validate(a.ExecutableID, workspace.Directory); err != nil {
		return adapter.CommandSpec{}, err
	}
	return spec, nil
}
