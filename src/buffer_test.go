package main

import (
	"reflect"
	"testing"
)

// TestRingBufferFIFOOverwrite is the headline Phase 1 gate: a capacity-3 buffer
// fed 5 items must retain exactly items 3, 4, and 5, in that chronological
// order.
func TestRingBufferFIFOOverwrite(t *testing.T) {
	ring := NewRingBuffer(3)

	for _, v := range []string{"1", "2", "3", "4", "5"} {
		ring.Push(v)
	}

	got := ring.GetContext()
	want := []string{"3", "4", "5"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GetContext() = %v, want %v", got, want)
	}
	if ring.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", ring.Len())
	}
}

// TestRingBufferUnderCapacity verifies that a partially filled buffer returns
// only what was pushed, in order.
func TestRingBufferUnderCapacity(t *testing.T) {
	ring := NewRingBuffer(3)
	ring.Push("a")
	ring.Push("b")

	got := ring.GetContext()
	want := []string{"a", "b"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GetContext() = %v, want %v", got, want)
	}
}

// TestRingBufferEmpty verifies that a fresh buffer returns a non-nil, empty
// slice so JSON serialization yields [] rather than null.
func TestRingBufferEmpty(t *testing.T) {
	ring := NewRingBuffer(3)

	got := ring.GetContext()
	if got == nil {
		t.Fatal("GetContext() returned nil; want non-nil empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("GetContext() = %v, want empty", got)
	}
}

// TestRingBufferExactMultipleOfCapacity pushes a multiple of the capacity so the
// head wraps exactly back to slot 0; an off-by-one in the modular arithmetic
// would surface here and not in the 5-into-3 case.
func TestRingBufferExactMultipleOfCapacity(t *testing.T) {
	ring := NewRingBuffer(2)
	for _, v := range []string{"1", "2", "3", "4"} {
		ring.Push(v)
	}

	got := ring.GetContext()
	want := []string{"3", "4"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GetContext() = %v, want %v", got, want)
	}
}

// TestRingBufferZeroAndNegativeCapacity verifies that --context 0 is a safe
// no-op rather than a panic or an allocation.
func TestRingBufferZeroAndNegativeCapacity(t *testing.T) {
	for _, capacity := range []int{0, -1} {
		ring := NewRingBuffer(capacity)
		ring.Push("a")
		ring.Push("b")

		if got := ring.GetContext(); len(got) != 0 {
			t.Fatalf("capacity %d: GetContext() = %v, want empty", capacity, got)
		}
		if ring.Len() != 0 {
			t.Fatalf("capacity %d: Len() = %d, want 0", capacity, ring.Len())
		}
		if ring.Cap() != 0 {
			t.Fatalf("capacity %d: Cap() = %d, want 0", capacity, ring.Cap())
		}
	}
}

// TestRingBufferReset verifies that Reset clears retained entries and that
// subsequent pushes start from a clean head.
func TestRingBufferReset(t *testing.T) {
	ring := NewRingBuffer(2)
	ring.Push("1")
	ring.Push("2")
	ring.Push("3")
	ring.Reset()

	if ring.Len() != 0 {
		t.Fatalf("after Reset, Len() = %d, want 0", ring.Len())
	}

	ring.Push("x")
	got := ring.GetContext()
	want := []string{"x"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("after Reset+Push, GetContext() = %v, want %v", got, want)
	}
}

// TestRingBufferGetContextIsACopy verifies the returned slice is not an alias of
// internal storage, so callers may retain or mutate it across further pushes.
func TestRingBufferGetContextIsACopy(t *testing.T) {
	ring := NewRingBuffer(2)
	ring.Push("1")
	ring.Push("2")

	snapshot := ring.GetContext()
	ring.Push("3")

	if !reflect.DeepEqual(snapshot, []string{"1", "2"}) {
		t.Fatalf("snapshot was mutated by a later Push: %v", snapshot)
	}
	if got := ring.GetContext(); !reflect.DeepEqual(got, []string{"2", "3"}) {
		t.Fatalf("GetContext() = %v, want [2 3]", got)
	}
}
