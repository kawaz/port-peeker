// Package checker inspects host listening state by reading /proc directly.
//
// No external commands are spawned: /proc/net/tcp{,6} carries the inode of
// every listening socket, and /proc/<pid>/fd/* readlinks of the form
// "socket:[INODE]" let us map a socket back to its owning process. This
// avoids the per-request fork/exec cost of `ss -tlnp` and removes a runtime
// dependency on the iproute2 package.
package checker

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// DefaultProcNetTCPFiles enumerates the procfs sources for IPv4 + IPv6
// listen sockets. Override via Checker.NetFiles in tests.
var DefaultProcNetTCPFiles = []string{"/proc/net/tcp", "/proc/net/tcp6"}

// DefaultProcRoot is /proc.
const DefaultProcRoot = "/proc"

// Checker is safe for concurrent use; it holds no mutable state.
type Checker struct {
	NetFiles []string
	ProcRoot string
}

func New() *Checker {
	return &Checker{NetFiles: DefaultProcNetTCPFiles, ProcRoot: DefaultProcRoot}
}

// Status reports the LISTEN state of a port. Processes is populated only
// when Inspect is called with wantProcesses=true and at least one socket
// inode could be resolved to a /proc/<pid>/comm.
type Status struct {
	Listening bool
	Processes []string
}

// Inspect reports whether port is in LISTEN state. If wantProcesses is true
// and the port is listening, it also resolves the owning process names by
// walking /proc and matching socket inodes. Process resolution silently
// skips entries that cannot be read (permission errors on other users'
// /proc/<pid>/fd are expected for non-root callers).
func (c *Checker) Inspect(port int, wantProcesses bool) (Status, error) {
	if port < 1 || port > 65535 {
		return Status{}, fmt.Errorf("port %d out of range", port)
	}
	inodes, err := c.listenInodes(port)
	if err != nil {
		return Status{}, err
	}
	s := Status{Listening: len(inodes) > 0}
	if !s.Listening || !wantProcesses {
		return s, nil
	}
	procs, err := resolveProcessNames(c.procRoot(), inodes)
	if err != nil {
		return s, err
	}
	s.Processes = procs
	return s, nil
}

func (c *Checker) procRoot() string {
	if c.ProcRoot == "" {
		return DefaultProcRoot
	}
	return c.ProcRoot
}

func (c *Checker) listenInodes(port int) (map[string]struct{}, error) {
	files := c.NetFiles
	if len(files) == 0 {
		files = DefaultProcNetTCPFiles
	}
	inodes := make(map[string]struct{})
	for _, path := range files {
		if err := scanInodesFile(path, port, inodes); err != nil {
			return nil, err
		}
	}
	return inodes, nil
}

func scanInodesFile(path string, port int, dst map[string]struct{}) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return scanInodes(f, port, dst)
}

// scanInodes parses /proc/net/tcp{,6}-format data and adds each LISTEN
// entry's inode (column index 9) to dst when its local port matches.
//
// Layout (whitespace-separated):
//
//	sl local_addr rem_addr st tx_queue:rx_queue tr:tm retrnsmt uid timeout inode ...
//
// The header row and any short or non-LISTEN line is skipped naturally.
func scanInodes(r io.Reader, port int, dst map[string]struct{}) error {
	if port < 1 || port > 65535 {
		return errors.New("port out of range (1-65535)")
	}
	target := fmt.Sprintf("%04X", port)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 10 {
			continue
		}
		if fields[3] != "0A" {
			continue
		}
		local := fields[1]
		idx := strings.LastIndex(local, ":")
		if idx < 0 {
			continue
		}
		if !strings.EqualFold(local[idx+1:], target) {
			continue
		}
		inode := fields[9]
		if inode == "0" || inode == "" {
			continue
		}
		dst[inode] = struct{}{}
	}
	return sc.Err()
}

// resolveProcessNames walks procRoot/<pid>/fd/* looking for symlinks of the
// form "socket:[INODE]" whose inode is in inodes. For each matching pid the
// trimmed contents of /proc/<pid>/comm is appended (in arbitrary order).
//
// Permission errors on other users' /proc/<pid> entries are expected and
// silently skipped: the caller treats an empty result as "not resolvable"
// rather than an error.
func resolveProcessNames(procRoot string, inodes map[string]struct{}) ([]string, error) {
	if len(inodes) == 0 {
		return nil, nil
	}
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid := e.Name()
		if !isAllDigits(pid) {
			continue
		}
		matched, err := pidOwnsAnyInode(filepath.Join(procRoot, pid, "fd"), inodes)
		if err != nil || !matched {
			continue
		}
		comm, err := os.ReadFile(filepath.Join(procRoot, pid, "comm"))
		if err != nil {
			continue
		}
		names = append(names, strings.TrimSpace(string(comm)))
	}
	return names, nil
}

func pidOwnsAnyInode(fdDir string, inodes map[string]struct{}) (bool, error) {
	fds, err := os.ReadDir(fdDir)
	if err != nil {
		return false, err
	}
	for _, fd := range fds {
		link, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
		if err != nil {
			continue
		}
		if !strings.HasPrefix(link, "socket:[") || !strings.HasSuffix(link, "]") {
			continue
		}
		inode := link[len("socket:[") : len(link)-1]
		if _, ok := inodes[inode]; ok {
			return true, nil
		}
	}
	return false, nil
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
