package exec

import (
	"os"
	"runtime"
	"strings"
)

func cleanEnv(jobDir string) []string {
	tmp := jobTmpDir(jobDir)
	env := []string{
		"HOME=" + jobDir,
		"PWD=" + jobDir,
		"LANG=C",
		"LC_ALL=C",
		"PATH=" + minimalPath(),
		"TMPDIR=" + tmp,
		"TMP=" + tmp,
		"TEMP=" + tmp,
		"XDG_CACHE_HOME=" + tmp,
		"XDG_CONFIG_HOME=" + tmp,
	}
	if runtime.GOOS == "windows" {
		for _, key := range []string{"SYSTEMROOT", "SYSTEMDRIVE", "WINDIR"} {
			if value, ok := os.LookupEnv(key); ok && value != "" {
				env = append(env, key+"="+value)
			}
		}
	}
	for _, item := range env {
		name, _, _ := strings.Cut(item, "=")
		if forbiddenEnv(name) {
			panic("clean environment included a forbidden variable")
		}
	}
	return env
}

func minimalPath() string {
	if runtime.GOOS == "windows" {
		root := os.Getenv("SYSTEMROOT")
		if root == "" {
			root = `C:\Windows`
		}
		return root + `\System32`
	}
	return "/usr/bin:/bin"
}

func forbiddenEnv(name string) bool {
	switch strings.ToUpper(name) {
	case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "FTP_PROXY":
		return true
	case "XRAY_LOCATION_ASSET", "XRAY_LOCATION_CONFDIR", "XRAY_LOCATION_CONFIG":
		return true
	case "CLASH_HOME", "MIHOMO_HOME":
		return true
	default:
		return false
	}
}
