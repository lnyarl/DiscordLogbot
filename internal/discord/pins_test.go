package discord

import (
	"fmt"
	"sort"
	"sync"
	"testing"
)

func setOf(ids ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		m[id] = struct{}{}
	}
	return m
}

func TestDiffPins(t *testing.T) {
	tests := []struct {
		name        string
		prev        map[string]struct{}
		current     map[string]struct{}
		wantAdded   []string
		wantRemoved []string
	}{
		{
			"both empty",
			setOf(), setOf(),
			nil, nil,
		},
		{
			"first pin added",
			setOf(), setOf("m1"),
			[]string{"m1"}, nil,
		},
		{
			"only removed",
			setOf("m1", "m2"), setOf(),
			nil, []string{"m1", "m2"},
		},
		{
			"swap one pin",
			setOf("m1", "m2"), setOf("m1", "m3"),
			[]string{"m3"}, []string{"m2"},
		},
		{
			"identical sets",
			setOf("m1", "m2", "m3"), setOf("m1", "m2", "m3"),
			nil, nil,
		},
		{
			"all replaced",
			setOf("a", "b"), setOf("c", "d"),
			[]string{"c", "d"}, []string{"a", "b"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := diffPins(tt.prev, tt.current)
			sort.Strings(d.Added)
			sort.Strings(d.Removed)
			sort.Strings(tt.wantAdded)
			sort.Strings(tt.wantRemoved)
			if !equalStrSlice(d.Added, tt.wantAdded) {
				t.Errorf("added: got=%v want=%v", d.Added, tt.wantAdded)
			}
			if !equalStrSlice(d.Removed, tt.wantRemoved) {
				t.Errorf("removed: got=%v want=%v", d.Removed, tt.wantRemoved)
			}
		})
	}
}

func equalStrSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPinCache_FirstObservationSignal(t *testing.T) {
	c := newPinCache()
	if _, ok := c.Get("ch1"); ok {
		t.Fatal("expected ok=false for unseeded channel")
	}
	c.Set("ch1", setOf("m1"))
	got, ok := c.Get("ch1")
	if !ok {
		t.Fatal("expected ok=true after Set")
	}
	if _, in := got["m1"]; !in {
		t.Fatal("m1 missing")
	}
}

func TestPinCache_GetReturnsCopy(t *testing.T) {
	c := newPinCache()
	c.Set("ch1", setOf("m1", "m2"))
	got, _ := c.Get("ch1")
	delete(got, "m1") // mutate the returned copy

	// Internal state must remain intact.
	again, _ := c.Get("ch1")
	if _, in := again["m1"]; !in {
		t.Fatal("Get returned a reference, not a defensive copy")
	}
}

func TestPinCache_ConcurrentSetGet(t *testing.T) {
	c := newPinCache()
	const n = 200
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			c.Set(fmt.Sprintf("ch%d", i), setOf(fmt.Sprintf("m%d", i)))
		}(i)
		go func(i int) {
			defer wg.Done()
			_, _ = c.Get(fmt.Sprintf("ch%d", i))
		}(i)
	}
	wg.Wait()
}

func TestFirstRunes(t *testing.T) {
	tests := []struct {
		in   string
		n    int
		want string
	}{
		{"", 5, ""},
		{"hi", 5, "hi"},
		{"hello world", 5, "hello"},
		{"안녕하세요반갑", 4, "안녕하세"},
		{"abcdef", 0, ""},
	}
	for _, tt := range tests {
		if got := firstRunes(tt.in, tt.n); got != tt.want {
			t.Errorf("firstRunes(%q, %d) = %q, want %q", tt.in, tt.n, got, tt.want)
		}
	}
}
