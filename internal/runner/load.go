package runner

import (
	"os"
	"strconv"
	"strings"
)

// The observation describes container load over the execution window. It does
// not attribute shared CPU throttling to one node or infer network throughput.
func cpuThrottleCount() (uint64, bool) {
	data, err := os.ReadFile("/sys/fs/cgroup/cpu.stat")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(data))
	for i := 0; i+1 < len(fields); i += 2 {
		if fields[i] == "nr_throttled" {
			n, err := strconv.ParseUint(fields[i+1], 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}
