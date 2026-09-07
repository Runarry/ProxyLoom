package exec

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/Runarry/ProxyLoom/internal/adapter"
)

type Workspace struct {
	Directory      string
	ConfigFilename string
}

func Allocate(parent, filename string, config []byte) (Workspace, error) {
	if filename == "" || filepath.Base(filename) != filename || filepath.IsAbs(filename) {
		return Workspace{}, ErrForbiddenPath
	}
	dir, err := os.MkdirTemp(parent, "proxyloom-job-")
	if err != nil {
		return Workspace{}, err
	}
	if err := os.Chmod(dir, 0o700); err != nil && runtime.GOOS != "windows" {
		_ = os.RemoveAll(dir)
		return Workspace{}, err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		return Workspace{}, err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	path := filepath.Join(abs, filename)
	if err := os.WriteFile(path, config, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return Workspace{}, err
	}
	tmp := jobTmpDir(abs)
	if err := os.Mkdir(tmp, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return Workspace{}, err
	}
	if err := os.Chmod(tmp, 0o700); err != nil && runtime.GOOS != "windows" {
		_ = os.RemoveAll(dir)
		return Workspace{}, err
	}
	return Workspace{Directory: abs, ConfigFilename: filename}, nil
}

func jobTmpDir(jobDir string) string {
	return filepath.Join(jobDir, "tmp")
}

func (w Workspace) Close() error {
	if w.Directory == "" {
		return nil
	}
	return os.RemoveAll(w.Directory)
}

func (w Workspace) Job() adapter.JobWorkspace {
	return adapter.JobWorkspace{Directory: w.Directory, ConfigFilename: w.ConfigFilename}
}
