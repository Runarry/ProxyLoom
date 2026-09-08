//go:build linux

package exec

import (
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	ose "os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

type sandboxRequest struct {
	Executable string        `json:"executable"`
	Arguments  []string      `json:"arguments"`
	Directory  string        `json:"directory"`
	Policy     SandboxPolicy `json:"policy"`
}

func sandboxCommand(path string, args []string, dir string, env []string, policy SandboxPolicy, expectedSHA string) (*ose.Cmd, func(), error) {
	if !policy.valid() || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		return nil, nil, ErrSandboxUnavailable
	}
	core, err := sealedExecutable(path, expectedSHA)
	if err != nil {
		return nil, nil, err
	}
	self, err := os.Open("/proc/self/exe")
	if err != nil {
		_ = core.Close()
		return nil, nil, ErrSandboxUnavailable
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		_ = core.Close()
		_ = self.Close()
		return nil, nil, ErrSandboxUnavailable
	}
	request, err := json.Marshal(sandboxRequest{Executable: path, Arguments: args, Directory: dir, Policy: policy})
	if err != nil {
		_ = reader.Close()
		_ = writer.Close()
		_ = core.Close()
		_ = self.Close()
		return nil, nil, ErrSandboxUnavailable
	}
	go func() {
		_, _ = writer.Write(request)
		_ = writer.Close()
	}()
	// fd 5 references the running supervisor's executable inode. No mutable
	// filesystem path is resolved to choose the helper during re-exec.
	cmd := ose.Command("/proc/self/fd/5", sandboxFlag)
	cmd.Dir = dir
	// sing-box's pinned fakecgo runtime uses glibc pthreads even with CGO=0.
	// One malloc arena prevents per-thread 64MiB virtual arenas from consuming
	// the job's address-space budget. These are fixed supervisor values.
	cmd.Env = append(env, "GOMAXPROCS=2", "GOMEMLIMIT=256MiB", "MALLOC_ARENA_MAX=1")
	cmd.SysProcAttr = sysProcAttr()
	cmd.ExtraFiles = []*os.File{reader, core, self}
	return cmd, func() { _ = reader.Close(); _ = writer.Close(); _ = core.Close(); _ = self.Close() }, nil
}

func sealedExecutable(path, expectedSHA string) (*os.File, error) {
	if len(expectedSHA) != 64 {
		return nil, ErrDigestMismatch
	}
	source, err := os.Open(path)
	if err != nil {
		return nil, ErrMissingBinary
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 256<<20 {
		return nil, ErrMissingBinary
	}
	fd, err := unix.MemfdCreate("proxyloom-core", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, ErrSandboxUnavailable
	}
	file := os.NewFile(uintptr(fd), "sealed-core")
	failed := true
	defer func() {
		if failed {
			_ = file.Close()
		}
	}()
	hash := sha256.New()
	count, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(source, (256<<20)+1))
	if err != nil || count != info.Size() || hex.EncodeToString(hash.Sum(nil)) != expectedSHA {
		return nil, ErrDigestMismatch
	}
	if unix.Fchmod(fd, 0o500) != nil {
		return nil, ErrSandboxUnavailable
	}
	seals := unix.F_SEAL_WRITE | unix.F_SEAL_GROW | unix.F_SEAL_SHRINK | unix.F_SEAL_SEAL
	if _, err := unix.FcntlInt(file.Fd(), unix.F_ADD_SEALS, seals); err != nil {
		return nil, ErrSandboxUnavailable
	}
	failed = false
	return file, nil
}

func sandboxChild() error {
	// Restrict and exec on the same kernel thread. Exec removes the Go helper's
	// other threads; the resulting core and all of its threads inherit both
	// irrevocable policies. No untrusted config is read before exec.
	runtime.LockOSThread()
	_ = unix.Close(5)
	unix.CloseOnExec(4)
	file := os.NewFile(3, "sandbox-request")
	if file == nil {
		return ErrSandboxUnavailable
	}
	defer file.Close()
	dec := json.NewDecoder(io.LimitReader(file, 16<<10))
	dec.DisallowUnknownFields()
	var request sandboxRequest
	if dec.Decode(&request) != nil || dec.Decode(new(any)) != io.EOF || !request.Policy.valid() || !filepath.IsAbs(request.Executable) || !filepath.IsAbs(request.Directory) || forbidShell(request.Executable) != nil {
		return ErrSandboxUnavailable
	}
	// A self-reexec is not an arbitrary command facility. Only the three
	// existing adapters' exact configuration-check argv are recognized.
	switch strings.Join(request.Arguments, " ") {
	case "run -test -c config.json", "check -c config.json", "-t -d . -f config.yaml":
	default:
		return ErrSandboxUnavailable
	}
	if err := file.Close(); err != nil {
		return ErrSandboxUnavailable
	}
	if err := os.Chdir(request.Directory); err != nil {
		return ErrSandboxUnavailable
	}
	path, err := unix.BytePtrFromString("")
	if err != nil {
		return ErrSandboxUnavailable
	}
	argv, err := stringPointers(append([]string{request.Executable}, request.Arguments...))
	if err != nil {
		return ErrSandboxUnavailable
	}
	env, err := stringPointers(os.Environ())
	if err != nil {
		return ErrSandboxUnavailable
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return ErrSandboxUnavailable
	}
	// No dump may contain the task's plaintext, and ptrace-style access to the
	// supervisor is denied even when it shares this non-root Unix identity.
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return ErrSandboxUnavailable
	}
	for resource, limit := range map[int]uint64{
		unix.RLIMIT_CPU:   request.Policy.CPUSeconds,
		unix.RLIMIT_NPROC: request.Policy.Processes, unix.RLIMIT_NOFILE: 64,
		unix.RLIMIT_CORE: 0, unix.RLIMIT_FSIZE: 16 << 20,
	} {
		if err := unix.Setrlimit(resource, &unix.Rlimit{Cur: limit, Max: limit}); err != nil {
			_, _ = os.Stderr.WriteString("sandbox_resource_policy_failed\n")
			return ErrSandboxUnavailable
		}
	}
	if err := restrictFilesystem(request.Directory, 4); err != nil {
		_, _ = os.Stderr.WriteString("sandbox_filesystem_policy_failed\n")
		return ErrSandboxUnavailable
	}
	if err := restrictSyscalls(); err != nil {
		_, _ = os.Stderr.WriteString("sandbox_syscall_policy_failed\n")
		return ErrSandboxUnavailable
	}
	return sandboxExec(path, argv, env, request.Policy.MemoryBytes)
}

//go:norace
func sandboxExec(path *byte, argv, env []*byte, memoryBytes uint64) error {
	// Go reserves a large virtual arena before this helper starts. Installing
	// RLIMIT_AS earlier can prevent the helper from allocating the BPF/argv
	// buffers even though the fresh checker fits. Make no Go allocation, syscall
	// wrapper, defer or runtime call between this final limit and raw execve.
	limit := unix.Rlimit{Cur: memoryBytes, Max: memoryBytes}
	_, _, errno := unix.RawSyscall6(unix.SYS_PRLIMIT64, 0, unix.RLIMIT_AS, uintptr(unsafe.Pointer(&limit)), 0, 0, 0)
	if errno == 0 {
		_, _, errno = unix.RawSyscall6(unix.SYS_EXECVEAT, 4, uintptr(unsafe.Pointer(path)), uintptr(unsafe.Pointer(&argv[0])), uintptr(unsafe.Pointer(&env[0])), unix.AT_EMPTY_PATH, 0)
	}
	// Fixed diagnostics reveal no path, configuration or caller-controlled text.
	message := "sandbox_exec_failed\n"
	switch errno {
	case unix.EPERM:
		message = "sandbox_exec_operation_denied\n"
	case unix.EACCES:
		message = "sandbox_exec_access_denied\n"
	case unix.ENOMEM:
		message = "sandbox_exec_memory_limit\n"
	case unix.ENOENT:
		message = "sandbox_exec_loader_missing\n"
	case unix.ENOEXEC:
		message = "sandbox_exec_format_rejected\n"
	}
	unix.RawSyscall(unix.SYS_WRITE, 2, uintptr(unsafe.Pointer(unsafe.StringData(message))), uintptr(len(message)))
	unix.RawSyscall(unix.SYS_EXIT_GROUP, sandboxFailureExit, 0, 0)
	for {
	} // Raw exit cannot return successfully.
}

func stringPointers(values []string) ([]*byte, error) {
	result := make([]*byte, len(values)+1)
	for i, value := range values {
		var err error
		result[i], err = unix.BytePtrFromString(value)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func restrictFilesystem(directory string, coreFD int) error {
	version, _, errno := unix.RawSyscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 || version < 3 {
		return ErrSandboxUnavailable
	}
	// ABI 3 includes REFER and TRUNCATE; older kernels cannot meet this policy.
	const handled = uint64((1 << 15) - 1)
	attr := unix.LandlockRulesetAttr{Access_fs: handled}
	fd, _, errno := unix.RawSyscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), 8, 0)
	if errno != 0 {
		return errno
	}
	defer unix.Close(int(fd))
	allowFD := func(pathfd int, rights uint64) error {
		rule := unix.LandlockPathBeneathAttr{Allowed_access: rights, Parent_fd: int32(pathfd)}
		_, _, errnum := unix.RawSyscall6(unix.SYS_LANDLOCK_ADD_RULE, fd, unix.LANDLOCK_RULE_PATH_BENEATH, uintptr(unsafe.Pointer(&rule)), 0, 0, 0)
		if errnum != 0 {
			return errnum
		}
		return nil
	}
	allow := func(path string, rights uint64) error {
		pathfd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		defer unix.Close(pathfd)
		return allowFD(pathfd, rights)
	}
	// Anonymous sealed memfds are not filesystem hierarchies, so Landlock does
	// not mediate them and rejects an add_rule with EBADFD. Descriptor 4 is an
	// authenticated, sealed snapshot. Seccomp below admits only execveat(4,...,
	// AT_EMPTY_PATH), forbids creating other memfds, and fd 4 closes on exec.
	file, err := elf.NewFile(descriptorReaderAt(coreFD))
	if err != nil {
		return ErrSandboxUnavailable
	}
	defer file.Close()
	for _, program := range file.Progs {
		if program.Type != elf.PT_INTERP {
			continue
		}
		loader, err := io.ReadAll(io.LimitReader(program.Open(), 256))
		if err != nil {
			return ErrSandboxUnavailable
		}
		name := strings.TrimRight(string(loader), "\x00")
		if runtime.GOARCH == "amd64" && name != "/lib64/ld-linux-x86-64.so.2" || runtime.GOARCH == "arm64" && name != "/lib/ld-linux-aarch64.so.1" {
			return ErrSandboxUnavailable
		}
		if err := allow(name, unix.LANDLOCK_ACCESS_FS_EXECUTE|unix.LANDLOCK_ACCESS_FS_READ_FILE); err != nil {
			return err
		}
		// The pinned Debian runtime and current dynamic sing-box build need
		// only these glibc objects. No entire system directory becomes readable.
		libraryRoot := "/lib/x86_64-linux-gnu"
		if runtime.GOARCH == "arm64" {
			libraryRoot = "/lib/aarch64-linux-gnu"
		}
		allowedLibraries := map[string]bool{"libc.so.6": true, "libdl.so.2": true, "libpthread.so.0": true}
		needed, err := file.DynString(elf.DT_NEEDED)
		if err != nil {
			return ErrSandboxUnavailable
		}
		for _, library := range needed {
			if !allowedLibraries[library] {
				return ErrSandboxUnavailable
			}
		}
		for library := range allowedLibraries {
			if err := allow(filepath.Join(libraryRoot, library), unix.LANDLOCK_ACCESS_FS_READ_FILE); err != nil {
				return err
			}
		}
		if _, err := os.Stat("/etc/ld.so.cache"); err == nil {
			if err := allow("/etc/ld.so.cache", unix.LANDLOCK_ACCESS_FS_READ_FILE); err != nil {
				return err
			}
		}
	}
	const workRights = unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR | unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_REMOVE_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_FILE | unix.LANDLOCK_ACCESS_FS_MAKE_DIR | unix.LANDLOCK_ACCESS_FS_MAKE_REG | unix.LANDLOCK_ACCESS_FS_TRUNCATE
	if err := allow(directory, workRights); err != nil {
		return err
	}
	_, _, errno = unix.RawSyscall(unix.SYS_LANDLOCK_RESTRICT_SELF, fd, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// The inherited descriptor is owned by execveat, not by an os.File finalizer.
type descriptorReaderAt int

func (fd descriptorReaderAt) ReadAt(buffer []byte, offset int64) (int, error) {
	n, err := unix.Pread(int(fd), buffer, offset)
	if err == nil && n < len(buffer) {
		err = io.EOF
	}
	return n, err
}

func restrictSyscalls() error {
	arch := uint32(unix.AUDIT_ARCH_X86_64)
	if runtime.GOARCH == "arm64" {
		arch = unix.AUDIT_ARCH_AARCH64
	} else if runtime.GOARCH != "amd64" {
		return ErrSandboxUnavailable
	}
	load := func(offset uint32) unix.SockFilter {
		return unix.SockFilter{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: offset}
	}
	eq := func(number uint32, yes, no uint8) unix.SockFilter {
		return unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: number, Jt: yes, Jf: no}
	}
	ret := func(value uint32) unix.SockFilter { return unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: value} }
	deny := uint32(unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM))
	filter := []unix.SockFilter{load(4), eq(arch, 1, 0), ret(unix.SECCOMP_RET_KILL_PROCESS), load(0)}
	// Reject x32 ABI numbers; otherwise they could bypass the amd64 list.
	if runtime.GOARCH == "amd64" {
		filter = append(filter, unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JSET | unix.BPF_K, K: 0x40000000, Jf: 1}, ret(unix.SECCOMP_RET_KILL_PROCESS))
	}
	blocked := []uint32{
		unix.SYS_SOCKET, unix.SYS_SOCKETPAIR, unix.SYS_CONNECT, unix.SYS_BIND,
		unix.SYS_LISTEN, unix.SYS_ACCEPT, unix.SYS_ACCEPT4, unix.SYS_SENDTO,
		unix.SYS_SENDMSG, unix.SYS_SENDMMSG, unix.SYS_RECVFROM, unix.SYS_RECVMSG, unix.SYS_RECVMMSG,
		unix.SYS_PTRACE, unix.SYS_PROCESS_VM_READV, unix.SYS_PROCESS_VM_WRITEV, unix.SYS_PIDFD_GETFD,
		unix.SYS_IO_URING_SETUP, unix.SYS_BPF, unix.SYS_PERF_EVENT_OPEN,
		unix.SYS_MEMFD_CREATE, unix.SYS_EXECVE,
		unix.SYS_UNSHARE, unix.SYS_SETNS, unix.SYS_SETSID, unix.SYS_SETPGID,
		unix.SYS_MOUNT, unix.SYS_UMOUNT2, unix.SYS_PIVOT_ROOT, unix.SYS_CHROOT,
		unix.SYS_OPEN_BY_HANDLE_AT, unix.SYS_NAME_TO_HANDLE_AT, unix.SYS_USERFAULTFD,
		unix.SYS_KEYCTL, unix.SYS_ADD_KEY, unix.SYS_REQUEST_KEY,
		unix.SYS_KILL, unix.SYS_TKILL, unix.SYS_PIDFD_SEND_SIGNAL,
		unix.SYS_RT_SIGQUEUEINFO, unix.SYS_RT_TGSIGQUEUEINFO, unix.SYS_PROCESS_MADVISE,
		unix.SYS_FCHMOD, unix.SYS_FCHMODAT, unix.SYS_FCHOWN, unix.SYS_FCHOWNAT,
		unix.SYS_UTIMENSAT,
	}
	blocked = append(blocked, extraBlockedSyscalls()...)
	for _, nr := range blocked {
		filter = append(filter, eq(nr, 0, 1), ret(deny))
	}
	// clone3 has pointer arguments, so cannot be safely inspected by BPF. ENOSYS
	// lets libc/Go fall back to clone; clone permits CLONE_THREAD only.
	filter = append(filter, eq(unix.SYS_CLONE3, 0, 1), ret(unix.SECCOMP_RET_ERRNO|uint32(unix.ENOSYS)))
	filter = append(filter, eq(unix.SYS_CLONE, 0, 4), load(16), unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JSET | unix.BPF_K, K: unix.CLONE_THREAD, Jt: 1}, ret(deny), ret(unix.SECCOMP_RET_ALLOW))
	// Go's preemption signal uses tgkill. Permit only the current process.
	filter = append(filter, eq(unix.SYS_TGKILL, 0, 4), load(16), eq(uint32(os.Getpid()), 1, 0), ret(deny), ret(unix.SECCOMP_RET_ALLOW))
	// A same-UID core must not alter its supervisor's process limits.
	filter = append(filter, eq(unix.SYS_PRLIMIT64, 0, 5), load(16), eq(0, 2, 0), eq(uint32(os.Getpid()), 1, 0), ret(deny), ret(unix.SECCOMP_RET_ALLOW))
	filter = append(filter, eq(unix.SYS_EXECVEAT, 0, 6), load(16), eq(4, 0, 2), load(48), eq(unix.AT_EMPTY_PATH, 1, 0), ret(deny), ret(unix.SECCOMP_RET_ALLOW))
	filter = append(filter, ret(unix.SECCOMP_RET_ALLOW))
	program := unix.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}
	_, _, errno := unix.RawSyscall(unix.SYS_PRCTL, unix.PR_SET_SECCOMP, unix.SECCOMP_MODE_FILTER, uintptr(unsafe.Pointer(&program)))
	if errno != 0 {
		return errno
	}
	return nil
}
