package server

import (
	"testing"
)

// SetTestSpawnHook sets SpawnHook on the manager and activates the env-var
// guard. All tests that call rm.Start() must go through this helper; the
// guard in Start() panics if GO_TEST_RUNNING=1 and SpawnHook is nil.
func SetTestSpawnHook(t *testing.T, rm *RunManager, fn func(StartOptions)) {
	t.Helper()
	t.Setenv("GO_TEST_RUNNING", "1")
	rm.SpawnHook = fn
}

func TestSpawnGuardPanicsWithoutHook(t *testing.T) {
	t.Setenv("GO_TEST_RUNNING", "1")
	dir := t.TempDir()
	rm, err := NewRunManager(dir, "/nonexistent/twincut.sh")
	if err != nil {
		t.Fatal(err)
	}
	// SpawnHook intentionally NOT set — should panic.
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic from spawn guard, got none")
		}
		msg, ok := r.(string)
		if !ok || len(msg) == 0 {
			t.Fatalf("expected string panic message, got %T: %v", r, r)
		}
	}()
	_, _ = rm.Start(StartOptions{Mode: "test"})
}
