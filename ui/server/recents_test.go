package server

import (
	"reflect"
	"strconv"
	"testing"
)

func TestRecentsStore_AddListRemove(t *testing.T) {
	store := NewRecentsStore(t.TempDir())

	got, err := store.List()
	if err != nil {
		t.Fatalf("List on empty: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty, got %v", got)
	}

	for _, p := range []string{"/a", "/b", "/c"} {
		if err := store.Add(p); err != nil {
			t.Fatalf("Add %s: %v", p, err)
		}
	}
	got, _ = store.List()
	want := []string{"/c", "/b", "/a"} // most-recent-first
	if !reflect.DeepEqual(got, want) {
		t.Errorf("List = %v, want %v", got, want)
	}

	// Re-adding an existing entry should float it to the top.
	if err := store.Add("/a"); err != nil {
		t.Fatalf("re-Add: %v", err)
	}
	got, _ = store.List()
	want = []string{"/a", "/c", "/b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("after re-add: List = %v, want %v", got, want)
	}

	// Remove drops a path.
	if err := store.Remove("/c"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	got, _ = store.List()
	want = []string{"/a", "/b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("after remove: List = %v, want %v", got, want)
	}
}

func TestRecentsStore_TrimsToMax(t *testing.T) {
	store := NewRecentsStore(t.TempDir())
	for i := 0; i < recentsMax+5; i++ {
		if err := store.Add("/p" + strconv.Itoa(i)); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	got, _ := store.List()
	if len(got) != recentsMax {
		t.Errorf("len = %d, want %d", len(got), recentsMax)
	}
}

func TestRecentsStore_IgnoresEmpty(t *testing.T) {
	store := NewRecentsStore(t.TempDir())
	if err := store.Add(""); err != nil {
		t.Fatalf("Add(\"\"): %v", err)
	}
	got, _ := store.List()
	if len(got) != 0 {
		t.Errorf("empty add added %v", got)
	}
}
