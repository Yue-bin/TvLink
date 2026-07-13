// Package pool maintains the in-memory Tavily key allocation state.
package pool

import (
	"errors"
	"math/rand"
	"sort"
	"sync"
	"time"
)

// ErrNoEligibleKey indicates that every configured key is unavailable.
var ErrNoEligibleKey = errors.New("no eligible tavily key")

// State describes a key's current allocation state.
type State string

const (
	// StateReady means a key can receive requests.
	StateReady State = "ready"
	// StateCooling means Tavily returned 429 and Retry-After has not elapsed.
	StateCooling State = "cooling"
	// StateProbing means one request is testing a key after cooldown.
	StateProbing State = "probing"
	// StateExhausted means Tavily reported that the key cannot spend more credits.
	StateExhausted State = "exhausted"
	// StatePending means TvLink has not obtained a successful usage snapshot.
	StatePending State = "pending"
)

// Key is one configured Tavily credential.
type Key struct {
	Name   string
	APIKey string
}

// Usage is the relevant part of Tavily's usage response.
type Usage struct {
	Limit int64
	Used  int64
}

// Lease identifies the key reserved for one upstream request.
type Lease struct {
	Key      Key
	Estimate float64
}

// Snapshot is the redacted state exposed to the monitor.
type Snapshot struct {
	Name           string
	Limit          int64
	RealUsage      int64
	RealUsageAt    time.Time
	EstimatedUsage float64
	Remaining      float64
	Weight         float64
	State          State
	RetryAt        time.Time
}

type keyState struct {
	key       Key
	limit     int64
	realUsage int64
	realAt    time.Time
	estimated float64
	ready     bool
	state     State
	retryAt   time.Time
}

// Pool synchronizes key allocation, reservations, and circuit state.
type Pool struct {
	mu     sync.Mutex
	keys   map[string]*keyState
	random *rand.Rand
}

// New creates a pool with the provided Tavily credentials.
func New(keys []Key, seed int64) *Pool {
	states := make(map[string]*keyState, len(keys))
	for _, key := range keys {
		states[key.Name] = &keyState{key: key, state: StatePending}
	}
	return &Pool{
		keys:   states,
		random: rand.New(rand.NewSource(seed)),
	}
}

// UpdateUsage replaces a key's authoritative Tavily usage snapshot.
func (p *Pool) UpdateUsage(name string, usage Usage, fetchedAt time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()

	state, ok := p.keys[name]
	if !ok {
		return
	}
	state.limit = usage.Limit
	state.realUsage = usage.Used
	state.realAt = fetchedAt
	state.estimated = 0
	state.ready = true
	if usage.Limit <= usage.Used {
		state.state = StateExhausted
		return
	}
	if state.state == StatePending || state.state == StateExhausted {
		state.state = StateReady
	}
}

// Key returns one configured credential by name.
func (p *Pool) Key(name string) (Key, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	state, ok := p.keys[name]
	if !ok {
		return Key{}, false
	}
	return state.key, true
}

// Select picks a key by weight and reserves the estimated upstream credit cost.
func (p *Pool) Select(now time.Time, estimate float64) (Lease, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	candidates := p.candidates(now)
	if len(candidates) == 0 {
		return Lease{}, ErrNoEligibleKey
	}

	totalWeight := 0.0
	for _, candidate := range candidates {
		totalWeight += candidate.weight
	}
	target := p.random.Float64() * totalWeight
	chosen := candidates[len(candidates)-1]
	for _, candidate := range candidates {
		target -= candidate.weight
		if target <= 0 {
			chosen = candidate
			break
		}
	}

	chosen.state.estimated += estimate
	if chosen.state.state == StateCooling {
		chosen.state.state = StateProbing
	}
	return Lease{Key: chosen.state.key, Estimate: estimate}, nil
}

// Resolve records an upstream HTTP result for a previously selected key.
func (p *Pool) Resolve(lease Lease, statusCode int, retryAfter time.Duration, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()

	state, ok := p.keys[lease.Key.Name]
	if !ok {
		return
	}
	switch statusCode {
	case 429:
		state.estimated = max(0, state.estimated-lease.Estimate)
		if retryAfter <= 0 {
			retryAfter = time.Minute
		}
		state.retryAt = now.Add(retryAfter)
		state.state = StateCooling
	case 432, 433:
		state.estimated = 0
		state.realUsage = state.limit
		state.state = StateExhausted
	case 200, 201:
		if state.state == StateProbing {
			state.state = StateReady
		}
	default:
		if statusCode >= 400 && statusCode < 500 {
			state.estimated = max(0, state.estimated-lease.Estimate)
		}
	}
}

// Snapshots returns monitor-safe copies of all key states.
func (p *Pool) Snapshots(now time.Time) []Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()

	snapshots := make([]Snapshot, 0, len(p.keys))
	for _, state := range p.keys {
		remaining := state.remaining()
		snapshots = append(snapshots, Snapshot{
			Name:           state.key.Name,
			Limit:          state.limit,
			RealUsage:      state.realUsage,
			RealUsageAt:    state.realAt,
			EstimatedUsage: state.estimated,
			Remaining:      remaining,
			Weight:         p.weight(state, now),
			State:          state.state,
			RetryAt:        state.retryAt,
		})
	}
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].Name < snapshots[j].Name
	})
	return snapshots
}

type candidate struct {
	state  *keyState
	weight float64
}

func (p *Pool) candidates(now time.Time) []candidate {
	states := make([]*keyState, 0, len(p.keys))
	averageFraction := 0.0
	for _, state := range p.keys {
		if !p.eligible(state, now) {
			continue
		}
		states = append(states, state)
		averageFraction += state.remaining() / float64(state.limit)
	}
	if len(states) == 0 {
		return nil
	}
	averageFraction /= float64(len(states))

	candidates := make([]candidate, 0, len(states))
	for _, state := range states {
		fraction := state.remaining() / float64(state.limit)
		correction := min(1.25, max(0.75, 1+0.5*(fraction-averageFraction)))
		candidates = append(candidates, candidate{state: state, weight: state.remaining() * correction})
	}
	return candidates
}

func (p *Pool) eligible(state *keyState, now time.Time) bool {
	if !state.ready || state.limit <= 0 || state.remaining() <= 0 || state.state == StateExhausted || state.state == StateProbing {
		return false
	}
	return state.state != StateCooling || !now.Before(state.retryAt)
}

func (p *Pool) weight(state *keyState, now time.Time) float64 {
	if !p.eligible(state, now) {
		return 0
	}
	return state.remaining()
}

func (s *keyState) remaining() float64 {
	return max(0, float64(s.limit-s.realUsage)-s.estimated)
}
