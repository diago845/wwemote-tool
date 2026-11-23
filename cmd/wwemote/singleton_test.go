package main

import "testing"

// Two copies share <panel>/user and would interleave writes into the same ini.
// The mutex is the only thing preventing that, and its failure mode is silent
// (both windows open, both claim success), so it is pinned here.
func TestSecondLaunchIsBlocked(t *testing.T) {
	if !acquireSingleInstance() {
		t.Fatal("the first acquire must succeed")
	}
	if acquireSingleInstance() {
		t.Fatal("a second acquire must report that an instance already exists")
	}
}
