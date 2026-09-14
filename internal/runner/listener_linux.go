//go:build linux

package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func ownsListener(pid, port int) bool {
	if pid <= 0 {
		return false
	}
	data, err := os.ReadFile("/proc/net/tcp")
	if err != nil {
		return false
	}
	wanted := fmt.Sprintf("0100007F:%04X", port)
	inodes := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 9 && fields[1] == wanted && fields[3] == "0A" {
			inodes["socket:["+fields[9]+"]"] = true
		}
	}
	directory := filepath.Join("/proc", strconv.Itoa(pid), "fd")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		link, err := os.Readlink(filepath.Join(directory, entry.Name()))
		if err == nil && inodes[link] {
			return true
		}
	}
	return false
}
