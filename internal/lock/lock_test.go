package lock

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestAcquireAndRelease(t *testing.T) {
	p := filepath.Join(t.TempDir(), "lock")
	ok, err := Acquire(p)
	if err != nil || !ok {
		t.Fatalf("first acquire: ok=%v err=%v", ok, err)
	}
	Release(p)
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("lock file should be removed on release")
	}
}

func TestSecondAcquireRejectedWhileAlive(t *testing.T) {
	p := filepath.Join(t.TempDir(), "lock")
	if ok, err := Acquire(p); err != nil || !ok {
		t.Fatalf("first acquire: %v %v", ok, err)
	}
	// Writing our own pid to the lock simulates a second instance.
	// Since our process is alive, a second Acquire must fail.
	ok, err := Acquire(p)
	if err != nil {
		t.Fatalf("second acquire err: %v", err)
	}
	if ok {
		t.Error("second acquire should fail while first instance is alive")
	}
	Release(p)
}

func TestStaleLockIsReclaimed(t *testing.T) {
	p := filepath.Join(t.TempDir(), "lock")
	// a lock file claiming a pid that does not exist (0) is stale
	if err := os.WriteFile(p, []byte(strconv.Itoa(0)), 0o644); err != nil {
		t.Fatal(err)
	}
	ok, err := Acquire(p)
	if err != nil || !ok {
		t.Fatalf("stale lock should be reclaimed: ok=%v err=%v", ok, err)
	}
	// now we hold it for real
	ok, err = Acquire(p)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("should not hold lock twice")
	}
	Release(p)
}

func TestPidAliveSelf(t *testing.T) {
	if !pidAlive(os.Getpid()) {
		t.Error("own pid should be alive")
	}
	if pidAlive(0) || pidAlive(-1) {
		t.Error("invalid pid reported alive")
	}
}
