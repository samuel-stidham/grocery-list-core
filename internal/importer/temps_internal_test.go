package importer

import (
	"os"
	"testing"
)

func TestTempPID(t *testing.T) {
	cases := []struct {
		tail string
		pid  int
		ok   bool
	}{
		{"4242", 4242, true},
		{"4242-wal", 4242, true},
		{"4242-shm", 4242, true},
		{"4242-journal", 4242, true},
		{"junk", 0, false},
		{"", 0, false},
		{"-5", 0, false},
		{"0", 0, false},
		{"12abc", 0, false},
	}
	for _, tc := range cases {
		pid, ok := tempPID(tc.tail)
		if pid != tc.pid || ok != tc.ok {
			t.Errorf("tempPID(%q) = %d, %v; want %d, %v", tc.tail, pid, ok, tc.pid, tc.ok)
		}
	}
}

func TestProcessAlive(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Error("this process is alive")
	}

	if !processAlive(os.Getppid()) {
		t.Error("the parent process is alive")
	}

	// Above pid_max on Linux (4194304) and PID_MAX on macOS (99998).
	if processAlive(1 << 30) {
		t.Error("no process can hold a pid above the kernel maximum")
	}
}
