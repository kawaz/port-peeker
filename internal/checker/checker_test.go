package checker

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const procNetTCPSample = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 7891 1 ffff8801340db080 100 0 0 10 0
   1: 00000000:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 12345 1 ffff8801340db100 100 0 0 10 0
   2: 0100007F:0050 0A0A0A0A:1F90 01 00000000:00000000 02:000000F4 00000000  1000        0 23456 1 ffff8801340db200 0 0 0 10 0
`

const procNetTCP6Sample = `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000000000000:0050 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 99999 1 ffffffffabcdef00 100 0 0 10 0
`

func TestScanInodes(t *testing.T) {
	cases := []struct {
		name string
		data string
		port int
		want []string
	}{
		{"ssh listening 22", procNetTCPSample, 22, []string{"7891"}},
		{"http listening 8080", procNetTCPSample, 8080, []string{"12345"}},
		{"port 80 established only", procNetTCPSample, 80, nil},
		{"unrelated port", procNetTCPSample, 9999, nil},
		{"tcp6 80", procNetTCP6Sample, 80, []string{"99999"}},
		{"empty", "", 22, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dst := map[string]struct{}{}
			if err := scanInodes(strings.NewReader(tc.data), tc.port, dst); err != nil {
				t.Fatalf("err: %v", err)
			}
			got := keys(dst)
			sort.Strings(got)
			want := append([]string(nil), tc.want...)
			sort.Strings(want)
			if len(got) == 0 && len(want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("port=%d got=%v want=%v", tc.port, got, want)
			}
		})
	}
}

func TestScanInodes_RejectsBadPort(t *testing.T) {
	dst := map[string]struct{}{}
	if err := scanInodes(strings.NewReader(""), 0, dst); err == nil {
		t.Fatal("port=0 should error")
	}
	if err := scanInodes(strings.NewReader(""), 65536, dst); err == nil {
		t.Fatal("port=65536 should error")
	}
}

func TestResolveProcessNames(t *testing.T) {
	root := newFakeProcRoot(t, []fakePid{
		{pid: "1234", comm: "sshd", fds: map[string]string{"3": "socket:[7891]", "4": "/dev/null"}},
		{pid: "2345", comm: "dovecot", fds: map[string]string{"10": "socket:[55555]"}},
		{pid: "9999", comm: "other", fds: map[string]string{"5": "socket:[7891]"}}, // shares same socket inode (e.g. fork)
		{pid: "1111", comm: "noisefd"},        // no fd dir
		{pid: "x_not_a_pid", comm: "ignored"}, // non-numeric, must be skipped
	})

	names, err := resolveProcessNames(root, map[string]struct{}{"7891": {}})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	sort.Strings(names)
	want := []string{"other", "sshd"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("got=%v want=%v", names, want)
	}
}

func TestResolveProcessNames_EmptyInodes(t *testing.T) {
	root := newFakeProcRoot(t, nil)
	names, err := resolveProcessNames(root, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("want empty, got %v", names)
	}
}

func TestCheckerInspect_FullStack(t *testing.T) {
	tmp := t.TempDir()

	tcp4 := filepath.Join(tmp, "tcp")
	if err := os.WriteFile(tcp4, []byte(procNetTCPSample), 0o644); err != nil {
		t.Fatal(err)
	}
	tcp6 := filepath.Join(tmp, "tcp6")
	if err := os.WriteFile(tcp6, []byte(procNetTCP6Sample), 0o644); err != nil {
		t.Fatal(err)
	}

	procRoot := newFakeProcRoot(t, []fakePid{
		{pid: "1234", comm: "sshd", fds: map[string]string{"3": "socket:[7891]"}},
		{pid: "2345", comm: "nginx", fds: map[string]string{"10": "socket:[12345]"}},
	})

	c := &Checker{NetFiles: []string{tcp4, tcp6}, ProcRoot: procRoot}

	t.Run("port 22 with process resolution", func(t *testing.T) {
		s, err := c.Inspect(22, true)
		if err != nil {
			t.Fatal(err)
		}
		if !s.Listening || !reflect.DeepEqual(s.Processes, []string{"sshd"}) {
			t.Fatalf("got=%+v", s)
		}
	})
	t.Run("port 22 listening-only", func(t *testing.T) {
		s, err := c.Inspect(22, false)
		if err != nil {
			t.Fatal(err)
		}
		if !s.Listening || s.Processes != nil {
			t.Fatalf("got=%+v", s)
		}
	})
	t.Run("port 9999 not listening", func(t *testing.T) {
		s, err := c.Inspect(9999, true)
		if err != nil {
			t.Fatal(err)
		}
		if s.Listening {
			t.Fatalf("got=%+v", s)
		}
	})
	t.Run("port 8080 listening but pid not resolvable", func(t *testing.T) {
		// inode 12345 belongs to pid 2345 (nginx) in our fake proc tree.
		s, err := c.Inspect(8080, true)
		if err != nil {
			t.Fatal(err)
		}
		if !s.Listening || !reflect.DeepEqual(s.Processes, []string{"nginx"}) {
			t.Fatalf("got=%+v", s)
		}
	})
}

// --- helpers ---

type fakePid struct {
	pid  string
	comm string
	fds  map[string]string // fd -> link target
}

func newFakeProcRoot(t *testing.T, pids []fakePid) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range pids {
		dir := filepath.Join(root, p.pid)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "comm"), []byte(p.comm+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if p.fds == nil {
			continue
		}
		fdDir := filepath.Join(dir, "fd")
		if err := os.MkdirAll(fdDir, 0o755); err != nil {
			t.Fatal(err)
		}
		for fd, target := range p.fds {
			if err := os.Symlink(target, filepath.Join(fdDir, fd)); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

func keys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
