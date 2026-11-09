// Package lifecycle names the states a proxy record moves through in one
// pipeline run, the legal transitions between them, and the invariant that ties
// a run's counts together.
//
// Those states already existed: fresh versus carried was item.carried, "not
// probed" was the absence of a row in check.CheckAll's kept jobs, the second
// chance was a retry slice, and "published" was writeOutputs' return value.
// Each site inferred the ones it needed and nothing named the whole flow, so a
// summary could say ten carried plus five fresh and publish twenty without any
// code noticing. This package is that name.
//
// It is deliberately pure: no I/O, no clock, no imports outside the standard
// library, so the model can be read and tested on its own and the pipeline can
// log it without paying for it.
//
// Skipped and dead both leave the record out of proxies.json, and the
// difference matters to anyone wondering why a proxy vanished:
//
//   - skipped: history suppressed every claimed probe this run, so no row in
//     proxy_state was touched. The record is absent from the output but its
//     history survives, which is what lets it come back on a later run.
//   - dead: every probe ran and nothing answered. The outcomes are written to
//     proxy_state, so the failure streak grows and the row prunes after
//     history's 14-day window.
package lifecycle

import (
	"fmt"
	"strconv"
	"strings"
)

// State is one record's disposition in a single run. The zero value is not a
// state: every record that enters the check phase is fetched or carried, and
// every one that leaves it is skipped, dead, or published.
type State string

const (
	// Fetched is a source's report of an address this run had not seen yet.
	Fetched State = "fetched"
	// Carried is an address recycled from the previous run's proxies.json,
	// either because no source republished it or because -skip-fetch was set.
	Carried State = "carried"
	// Skipped means history suppressed every claimed probe: a recent failure
	// streak, and less than the 24h recheck floor since its last check.
	Skipped State = "skipped"
	// Alive means at least one claimed protocol answered on the first pass.
	Alive State = "alive"
	// Revived means the first pass answered nothing, but the second-chance
	// re-probe did. Alive and revived together are exactly what is published.
	Revived State = "revived"
	// Dead means every probe ran and nothing answered. The outcomes are
	// recorded, so the row ages out of history after pruneDays.
	Dead State = "dead"
	// Published is the record as written to proxies.json (and, un-carried, to
	// the README's sources table).
	Published State = "published"
)

// edges is the legal next state for each state, in one run. Entry points reach
// any probe result; a dead record gets exactly one second chance; a skipped
// record ends the run unpublished; published is final.
//
// Dead -> Published is absent on purpose. A record only reaches proxies.json
// through Alive or Revived, so a revived record has already passed through Dead
// and back out; nothing publishes straight off a failed first pass.
var edges = map[State][]State{
	Fetched:   {Skipped, Alive, Dead},
	Carried:   {Skipped, Alive, Dead},
	Skipped:   nil,
	Alive:     {Published},
	Revived:   {Published},
	Dead:      {Revived},
	Published: nil,
}

// order is the order states are reported in: how the record entered, then what
// happened to it, then where it ended up. Counts.String and the tests both read
// this so the log line cannot drift from the model.
var order = []State{Fetched, Carried, Skipped, Alive, Revived, Dead, Published}

// CanTransition reports whether from may be followed by to in a single run.
func CanTransition(from, to State) bool {
	for _, s := range edges[from] {
		if s == to {
			return true
		}
	}
	return false
}

// Terminal reports whether a state has no successor in this run. An unknown
// state reports true because it has no known successor either.
func Terminal(s State) bool {
	return len(edges[s]) == 0
}

// Counts is one run's per-state tallies. The pipeline fills the entry fields
// from the records it loaded, the probe fields from check.Stats, and published
// from the write phase, then logs String and calls Check.
type Counts struct {
	Fetched   int
	Carried   int
	Skipped   int
	Alive     int
	Revived   int
	Dead      int
	Published int
}

// Total is how many records entered the check phase.
func (c Counts) Total() int { return c.Fetched + c.Carried }

// String renders the run summary in lifecycle order, e.g.
// "lifecycle: fetched=3811 carried=1204 skipped=17 alive=2402 revived=31
// dead=2565 published=2433".
func (c Counts) String() string {
	parts := make([]string, len(order))
	for i, s := range order {
		parts[i] = string(s) + "=" + strconv.Itoa(c.get(s))
	}
	return "lifecycle: " + strings.Join(parts, " ")
}

func (c Counts) get(s State) int {
	switch s {
	case Fetched:
		return c.Fetched
	case Carried:
		return c.Carried
	case Skipped:
		return c.Skipped
	case Alive:
		return c.Alive
	case Revived:
		return c.Revived
	case Dead:
		return c.Dead
	case Published:
		return c.Published
	}
	return 0
}

// Check is the run invariant: every record that entered the check phase was
// either skipped by history, died, or published. Alive and revived are not in
// the sum because they both end at published, so counting them would count the
// same record twice.
//
// It holds for a run that skipped probing too: skipped and dead are then zero
// and every record is published, which is exactly what -skip-check writes. It
// is a warning at the call site rather than a failure, because a run that
// aborts mid-check (an unreachable check endpoint, a budget hit before any
// aggregation) legitimately accounts for fewer records than it loaded, and the
// caller has already logged the reason.
func (c Counts) Check() error {
	entered, accounted := c.Total(), c.Skipped+c.Dead+c.Published
	if entered != accounted {
		return fmt.Errorf(
			"counts do not balance: entered %d (fetched %d + carried %d), accounted %d (skipped %d + dead %d + published %d)",
			entered, c.Fetched, c.Carried, accounted, c.Skipped, c.Dead, c.Published)
	}
	return nil
}
