//go:build unix

package exec

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	ose "os/exec"
)

func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

func terminate(cmd *ose.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
}

func killProcess(cmd *ose.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}

func prepareReaper() {
	enableChildSubreaper()
}

func reapOrphans() {
	deadline := time.Now().Add(killGrace)
	for {
		liveMu.Lock()
		leftover := childrenOfSelfLocked()
		liveMu.Unlock()
		for _, child := range leftover {
			if child.state != 'Z' && child.state != 'X' {
				_ = syscall.Kill(child.pid, syscall.SIGKILL)
				liveMu.Lock()
				liveGroup := isLivePidLocked(child.pgrp)
				liveMu.Unlock()
				if child.pgrp > 1 && !liveGroup {
					_ = syscall.Kill(-child.pgrp, syscall.SIGKILL)
				}
			}
			var status syscall.WaitStatus
			_, _ = syscall.Wait4(child.pid, &status, syscall.WNOHANG, nil)
		}
		liveMu.Lock()
		empty := len(childrenOfSelfLocked()) == 0
		if inFlight == 0 {
			drainWait4Locked()
		}
		liveMu.Unlock()
		if empty {
			return
		}
		if time.Now().After(deadline) {
			liveMu.Lock()
			if inFlight == 0 {
				drainWait4Locked()
			}
			liveMu.Unlock()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func drainWait4Locked() {
	for {
		var status syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &status, syscall.WNOHANG, nil)
		if pid > 0 {
			continue
		}
		if err == syscall.EINTR {
			continue
		}
		return
	}
}

type procStat struct {
	pid   int
	ppid  int
	pgrp  int
	state byte
}

func childrenOfSelfLocked() []procStat {
	self := os.Getpid()
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []procStat
	for _, ent := range ents {
		pid, err := strconv.Atoi(ent.Name())
		if err != nil || pid <= 1 || pid == self {
			continue
		}
		raw, err := os.ReadFile("/proc/" + ent.Name() + "/stat")
		if err != nil {
			continue
		}
		child, ok := parseLinuxProcStat(pid, raw)
		if !ok || child.ppid != self || isLivePidLocked(child.pid) {
			continue
		}
		out = append(out, child)
	}
	return out
}

func parseLinuxProcStat(pid int, raw []byte) (procStat, bool) {
	s := string(raw)
	rparen := strings.LastIndex(s, ")")
	if rparen < 0 || rparen+2 >= len(s) {
		return procStat{}, false
	}
	fields := strings.Fields(s[rparen+2:])
	if len(fields) < 3 || len(fields[0]) != 1 {
		return procStat{}, false
	}
	ppid, err1 := strconv.Atoi(fields[1])
	pgrp, err2 := strconv.Atoi(fields[2])
	if err1 != nil || err2 != nil {
		return procStat{}, false
	}
	return procStat{pid: pid, ppid: ppid, pgrp: pgrp, state: fields[0][0]}, true
}
