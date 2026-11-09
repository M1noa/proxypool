package lifecycle

import (
	"strings"
	"testing"
)

// the model is only as good as its coverage: a state missing from edges would
// silently report as terminal, and the log line would silently omit it.
func TestEdgesCoverEveryState(t *testing.T) {
	for _, s := range order {
		if _, ok := edges[s]; !ok {
			t.Errorf("%s has no entry in edges", s)
		}
	}
	if len(edges) != len(order) {
		t.Errorf("edges describes %d states, order lists %d", len(edges), len(order))
	}
}

func TestCanTransition(t *testing.T) {
	legal := [][2]State{
		{Fetched, Skipped},
		{Fetched, Alive},
		{Fetched, Dead},
		{Carried, Skipped},
		{Carried, Alive},
		{Carried, Dead},
		{Dead, Revived},
		{Alive, Published},
		{Revived, Published},
	}
	for _, e := range legal {
		if !CanTransition(e[0], e[1]) {
			t.Errorf("CanTransition(%s, %s) = false, want true", e[0], e[1])
		}
	}

	illegal := [][2]State{
		{Skipped, Alive},
		{Skipped, Published},
		{Dead, Alive},
		{Dead, Published},
		{Published, Alive},
		{Published, Dead},
		{Alive, Revived},
		{Revived, Dead},
		{Fetched, Published},
		{Carried, Revived},
		{Fetched, Fetched},
	}
	for _, e := range illegal {
		if CanTransition(e[0], e[1]) {
			t.Errorf("CanTransition(%s, %s) = true, want false", e[0], e[1])
		}
	}
}

func TestTerminal(t *testing.T) {
	for _, s := range []State{Skipped, Published} {
		if !Terminal(s) {
			t.Errorf("Terminal(%s) = false, want true", s)
		}
	}
	for _, s := range []State{Fetched, Carried, Alive, Revived, Dead} {
		if Terminal(s) {
			t.Errorf("Terminal(%s) = true, want false", s)
		}
	}
}

func TestCountsString(t *testing.T) {
	got := Counts{
		Fetched: 1, Carried: 2, Skipped: 3, Alive: 4,
		Revived: 5, Dead: 6, Published: 7,
	}.String()
	want := "lifecycle: fetched=1 carried=2 skipped=3 alive=4 revived=5 dead=6 published=7"
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestCountsCheck(t *testing.T) {
	// alive+revived (4) is exactly published, and the rest were skipped or died
	balanced := Counts{
		Fetched: 5, Carried: 2, Skipped: 1, Alive: 3,
		Revived: 1, Dead: 2, Published: 4,
	}
	if err := balanced.Check(); err != nil {
		t.Errorf("Check() = %v, want nil", err)
	}

	// -skip-check: nothing was skipped or probed, everything was published
	unprobed := Counts{Fetched: 5, Carried: 2, Published: 7}
	if err := unprobed.Check(); err != nil {
		t.Errorf("Check() on a skip-check run = %v, want nil", err)
	}

	if err := (Counts{}).Check(); err != nil {
		t.Errorf("Check() on an empty run = %v, want nil", err)
	}

	missing := balanced
	missing.Published--
	err := missing.Check()
	if err == nil {
		t.Fatal("Check() on a short run = nil, want an error")
	}
	for _, want := range []string{"do not balance", "entered 7", "accounted 6"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Check() error %q does not mention %q", err, want)
		}
	}
}
