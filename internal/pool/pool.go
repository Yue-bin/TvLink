// Package pool maintains the in-memory Tavily key allocation state.
package pool

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"time"
)

// ErrNoEligibleKey indicates that every configured key is unavailable.
var ErrNoEligibleKey = errors.New("no eligible tavily key")

// ErrGroupRebuildRequired indicates that grouped allocation needs refreshed usage.
var ErrGroupRebuildRequired = errors.New("key groups require rebuild")

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

// Workload describes endpoint-specific allocation behavior.
type Workload uint8

const (
	// WorkloadDefault uses normal Key eligibility.
	WorkloadDefault Workload = iota
	// WorkloadResearch preserves its reservation across usage refreshes.
	WorkloadResearch
)

// Selection describes one requested reservation.
type Selection struct {
	Estimate float64
	Workload Workload
	Excluded map[string]struct{}
}

// Lease identifies the Key reservation for one upstream request.
type Lease struct {
	ID       uint64
	Key      Key
	Estimate float64
	Workload Workload
	group    int
}

// Snapshot is the redacted state exposed to the monitor.
type Snapshot struct {
	Name             string
	Group            int
	Limit            int64
	RealUsage        int64
	RealUsageAt      time.Time
	EstimatedUsage   float64
	Remaining        float64
	Weight           float64
	State            State
	RetryAt          time.Time
	ResearchReserved float64
	ResearchBlocked  bool
}

// GroupSnapshot is the redacted aggregate state of one configured key group.
type GroupSnapshot struct {
	Index          int
	Active         bool
	Spent          bool
	KeyCount       int
	ReadyKeys      int
	Limit          int64
	RealUsage      int64
	EstimatedUsage float64
	Remaining      float64
	RoundUsage     float64
	RoundLimit     float64
}

// MonitorSnapshot is an atomic copy of key and group allocation state.
type MonitorSnapshot struct {
	Keys            []Snapshot
	Groups          []GroupSnapshot
	GroupingEnabled bool
	ActiveGroup     int
}

// GroupConfig enables optional key-group rotation.
type GroupConfig struct {
	Size       int
	UsageLimit float64
	Location   *time.Location
}

type keyState struct {
	key               Key
	limit             int64
	realUsage         int64
	realAt            time.Time
	estimated         float64
	ready             bool
	state             State
	retryAt           time.Time
	researchBlockedAt *float64
}

type reservation struct {
	lease      Lease
	persistent bool
	settled    bool
}

type groupState struct {
	keys      map[string]struct{}
	remaining float64
	reserved  float64
}

// Pool synchronizes key allocation, reservations, and circuit state.
type Pool struct {
	mu            sync.Mutex
	keys          map[string]*keyState
	random        *rand.Rand
	groupConfig   GroupConfig
	groups        []groupState
	activeGroup   int
	rotationMonth month
	reservations  map[uint64]*reservation
	nextLeaseID   uint64
}

type month struct {
	year  int
	month time.Month
}

// New creates a pool with the provided Tavily credentials.
func New(keys []Key, seed int64) *Pool {
	states := make(map[string]*keyState, len(keys))
	for _, key := range keys {
		states[key.Name] = &keyState{key: key, state: StatePending}
	}
	return &Pool{
		keys:         states,
		random:       rand.New(rand.NewSource(seed)),
		activeGroup:  -1,
		reservations: make(map[uint64]*reservation),
	}
}

// ConfigureGroups configures optional key-group rotation.
func (p *Pool) ConfigureGroups(config GroupConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if config == (GroupConfig{}) {
		p.groupConfig = GroupConfig{}
		p.groups = nil
		p.activeGroup = -1
		return nil
	}
	if config.Size <= 0 {
		return fmt.Errorf("group size must be positive")
	}
	if config.UsageLimit <= 0 {
		return fmt.Errorf("group usage limit must be positive")
	}
	if config.Location == nil {
		return fmt.Errorf("group location is required")
	}
	p.groupConfig = config
	p.groups = nil
	p.activeGroup = -1
	return nil
}

// RebuildGroups redistributes known, non-exhausted keys by remaining capacity.
func (p *Pool) RebuildGroups(now time.Time) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.groupConfig == (GroupConfig{}) {
		return nil
	}
	keys := make([]*keyState, 0, len(p.keys))
	for _, state := range p.keys {
		if !state.ready || state.state == StateExhausted || state.remaining() <= 0 {
			continue
		}
		keys = append(keys, state)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].remaining() == keys[j].remaining() {
			return keys[i].key.Name < keys[j].key.Name
		}
		return keys[i].remaining() > keys[j].remaining()
	})
	if len(keys) == 0 {
		p.groups = nil
		p.activeGroup = -1
		p.rotationMonth = monthOf(now, p.groupConfig.Location)
		return nil
	}

	groupCount := (len(keys) + p.groupConfig.Size - 1) / p.groupConfig.Size
	baseSize := len(keys) / groupCount
	extra := len(keys) % groupCount
	capacities := make([]int, groupCount)
	for index := range capacities {
		capacities[index] = baseSize
		if index < extra {
			capacities[index]++
		}
	}
	p.groups = optimalGroups(keys, capacities)
	orderGroupsForRotation(p.groups)
	if p.activeGroup < 0 {
		p.activeGroup = 0
	} else {
		p.activeGroup = (p.activeGroup + 1) % len(p.groups)
	}
	p.rotationMonth = monthOf(now, p.groupConfig.Location)
	return nil
}

type partitionBucket struct {
	keys  []*keyState
	total float64
}

type partitionScore struct {
	spread   float64
	variance float64
}

// optimalGroups finds the minimum-spread partition while preserving group sizes.
func optimalGroups(keys []*keyState, capacities []int) []groupState {
	buckets := make([]partitionBucket, len(capacities))
	var best []partitionBucket
	var bestScore partitionScore
	searchPartition(keys, capacities, buckets, 0, &best, &bestScore)

	groups := make([]groupState, len(capacities))
	for index, bucket := range best {
		groups[index].keys = make(map[string]struct{}, len(bucket.keys))
		groups[index].remaining = bucket.total
		for _, state := range bucket.keys {
			groups[index].keys[state.key.Name] = struct{}{}
		}
	}
	return groups
}

func orderGroupsForRotation(groups []groupState) {
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].remaining != groups[j].remaining {
			return groups[i].remaining > groups[j].remaining
		}
		return groupRotationNameLess(groups[i], groups[j])
	})
}

func groupRotationNameLess(left, right groupState) bool {
	leftNames := groupRotationNames(left)
	rightNames := groupRotationNames(right)
	for index := range min(len(leftNames), len(rightNames)) {
		if leftNames[index] != rightNames[index] {
			return leftNames[index] < rightNames[index]
		}
	}
	return len(leftNames) < len(rightNames)
}

func groupRotationNames(group groupState) []string {
	names := make([]string, 0, len(group.keys))
	for name := range group.keys {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func searchPartition(keys []*keyState, capacities []int, buckets []partitionBucket, keyIndex int, best *[]partitionBucket, bestScore *partitionScore) {
	if keyIndex == len(keys) {
		score := scorePartition(buckets)
		if len(*best) == 0 || betterPartition(score, *bestScore) {
			*best = cloneBuckets(buckets)
			*bestScore = score
		}
		return
	}

	state := keys[keyIndex]
	for groupIndex := range buckets {
		if len(buckets[groupIndex].keys) >= capacities[groupIndex] || equivalentBucket(buckets, capacities, groupIndex) {
			continue
		}
		buckets[groupIndex].keys = append(buckets[groupIndex].keys, state)
		buckets[groupIndex].total += state.remaining()
		searchPartition(keys, capacities, buckets, keyIndex+1, best, bestScore)
		buckets[groupIndex].total -= state.remaining()
		buckets[groupIndex].keys = buckets[groupIndex].keys[:len(buckets[groupIndex].keys)-1]
	}
}

func equivalentBucket(buckets []partitionBucket, capacities []int, index int) bool {
	for previous := 0; previous < index; previous++ {
		if capacities[previous] == capacities[index] && len(buckets[previous].keys) == len(buckets[index].keys) && buckets[previous].total == buckets[index].total {
			return true
		}
	}
	return false
}

func scorePartition(buckets []partitionBucket) partitionScore {
	minimum, maximum := buckets[0].total, buckets[0].total
	total := 0.0
	for _, bucket := range buckets {
		minimum = min(minimum, bucket.total)
		maximum = max(maximum, bucket.total)
		total += bucket.total
	}
	mean := total / float64(len(buckets))
	variance := 0.0
	for _, bucket := range buckets {
		difference := bucket.total - mean
		variance += difference * difference
	}
	return partitionScore{spread: maximum - minimum, variance: variance / float64(len(buckets))}
}

func betterPartition(candidate, current partitionScore) bool {
	const epsilon = 1e-9
	if candidate.spread < current.spread-epsilon {
		return true
	}
	return candidate.spread <= current.spread+epsilon && candidate.variance < current.variance-epsilon
}

func cloneBuckets(buckets []partitionBucket) []partitionBucket {
	clone := make([]partitionBucket, len(buckets))
	for index, bucket := range buckets {
		clone[index].keys = append([]*keyState(nil), bucket.keys...)
		clone[index].total = bucket.total
	}
	return clone
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
	state.ready = true
	for id, reservation := range p.reservations {
		if reservation.lease.Key.Name != name {
			continue
		}
		if reservation.persistent && !reservation.settled {
			continue
		}
		p.removeReservation(id, false)
	}
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

// Select picks a Key for a normal workload.
func (p *Pool) Select(now time.Time, estimate float64) (Lease, error) {
	return p.SelectFor(now, Selection{Estimate: estimate})
}

// SelectFor picks a Key that can hold the requested reservation.
func (p *Pool) SelectFor(now time.Time, selection Selection) (Lease, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.groupConfig != (GroupConfig{}) && len(p.groups) > 0 && monthOf(now, p.groupConfig.Location) != p.rotationMonth {
		return Lease{}, ErrGroupRebuildRequired
	}

	groupIndex := -1
	candidates := p.candidates(now, selection)
	if p.groupConfig != (GroupConfig{}) && len(p.groups) > 0 {
		groupIndex, candidates = p.groupCandidates(now, selection)
		if len(candidates) == 0 && p.allGroupsSpent() {
			return Lease{}, ErrGroupRebuildRequired
		}
	}
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

	p.nextLeaseID++
	lease := Lease{
		ID:       p.nextLeaseID,
		Key:      chosen.state.key,
		Estimate: selection.Estimate,
		Workload: selection.Workload,
		group:    groupIndex,
	}
	p.reservations[lease.ID] = &reservation{
		lease:      lease,
		persistent: selection.Workload == WorkloadResearch,
	}
	chosen.state.estimated += selection.Estimate
	if groupIndex >= 0 {
		p.groups[groupIndex].reserved += selection.Estimate
	}
	if chosen.state.state == StateCooling {
		chosen.state.state = StateProbing
	}
	return lease, nil
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
		p.removeReservation(lease.ID, true)
		if retryAfter <= 0 {
			retryAfter = time.Minute
		}
		state.retryAt = now.Add(retryAfter)
		state.state = StateCooling
	case 432, 433:
		p.removeReservation(lease.ID, true)
		if lease.Workload == WorkloadResearch {
			remaining := state.remaining()
			state.researchBlockedAt = &remaining
		}
	case 200, 201:
		if state.state == StateProbing {
			state.state = StateReady
		}
	default:
		if statusCode >= 400 && statusCode < 500 {
			p.removeReservation(lease.ID, true)
		}
	}
}

// SettleResearch marks a persistent reservation for removal by the next usage refresh.
func (p *Pool) SettleResearch(lease Lease) {
	p.mu.Lock()
	defer p.mu.Unlock()

	reservation, ok := p.reservations[lease.ID]
	if !ok || reservation.lease.Workload != WorkloadResearch {
		return
	}
	reservation.settled = true
}

// Snapshots returns monitor-safe copies of all key states.
func (p *Pool) Snapshots(now time.Time) []Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()

	snapshots := make([]Snapshot, 0, len(p.keys))
	for _, state := range p.keys {
		remaining := state.remaining()
		snapshots = append(snapshots, Snapshot{
			Name:             state.key.Name,
			Limit:            state.limit,
			RealUsage:        state.realUsage,
			RealUsageAt:      state.realAt,
			EstimatedUsage:   state.estimated,
			Remaining:        remaining,
			Weight:           p.weight(state, now),
			State:            state.state,
			RetryAt:          state.retryAt,
			ResearchReserved: p.researchReserved(state.key.Name),
			ResearchBlocked:  p.researchBlocked(state),
		})
	}
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].Name < snapshots[j].Name
	})
	return snapshots
}

// MonitorSnapshot returns one consistent view of key and group monitor state.
func (p *Pool) MonitorSnapshot(now time.Time) MonitorSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()

	groupingEnabled := p.groupConfig != (GroupConfig{})
	membership := make(map[string]int, len(p.keys))
	weights := make(map[string]float64, len(p.keys))
	candidates := p.candidates(now, Selection{})
	if groupingEnabled && p.activeGroup >= 0 && p.activeGroup < len(p.groups) {
		candidates = p.candidatesFor(now, p.groups[p.activeGroup].keys, Selection{})
	}
	for _, candidate := range candidates {
		weights[candidate.state.key.Name] = candidate.weight
	}

	groups := make([]GroupSnapshot, 0, len(p.groups))
	for index, group := range p.groups {
		snapshot := GroupSnapshot{
			Index:      index + 1,
			Active:     index == p.activeGroup,
			Spent:      group.reserved >= p.groupConfig.UsageLimit,
			KeyCount:   len(group.keys),
			RoundUsage: group.reserved,
			RoundLimit: p.groupConfig.UsageLimit,
		}
		for name := range group.keys {
			membership[name] = index + 1
			state := p.keys[name]
			snapshot.Limit += state.limit
			snapshot.RealUsage += state.realUsage
			snapshot.EstimatedUsage += state.estimated
			snapshot.Remaining += state.remaining()
			if state.state == StateReady {
				snapshot.ReadyKeys++
			}
		}
		groups = append(groups, snapshot)
	}

	keys := make([]Snapshot, 0, len(p.keys))
	for name, state := range p.keys {
		keys = append(keys, Snapshot{
			Name:             state.key.Name,
			Group:            membership[name],
			Limit:            state.limit,
			RealUsage:        state.realUsage,
			RealUsageAt:      state.realAt,
			EstimatedUsage:   state.estimated,
			Remaining:        state.remaining(),
			Weight:           weights[name],
			State:            state.state,
			RetryAt:          state.retryAt,
			ResearchReserved: p.researchReserved(name),
			ResearchBlocked:  p.researchBlocked(state),
		})
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Name < keys[j].Name })
	activeGroup := 0
	if p.activeGroup >= 0 && p.activeGroup < len(p.groups) {
		activeGroup = p.activeGroup + 1
	}
	return MonitorSnapshot{
		Keys:            keys,
		Groups:          groups,
		GroupingEnabled: groupingEnabled,
		ActiveGroup:     activeGroup,
	}
}

type candidate struct {
	state  *keyState
	weight float64
}

func (p *Pool) candidates(now time.Time, selection Selection) []candidate {
	return p.candidatesFor(now, nil, selection)
}

func (p *Pool) candidatesFor(now time.Time, allowed map[string]struct{}, selection Selection) []candidate {
	states := make([]*keyState, 0, len(p.keys))
	averageFraction := 0.0
	for name, state := range p.keys {
		if allowed != nil {
			if _, ok := allowed[name]; !ok {
				continue
			}
		}
		if _, excluded := selection.Excluded[name]; excluded {
			continue
		}
		if !p.eligible(state, now, selection) {
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

func (p *Pool) groupCandidates(now time.Time, selection Selection) (int, []candidate) {
	for offset := 0; offset < len(p.groups); offset++ {
		index := (p.activeGroup + offset) % len(p.groups)
		group := &p.groups[index]
		if group.reserved > 0 && group.reserved+selection.Estimate > p.groupConfig.UsageLimit {
			continue
		}
		candidates := p.candidatesFor(now, group.keys, selection)
		if len(candidates) == 0 {
			continue
		}
		p.activeGroup = index
		return index, candidates
	}
	return -1, nil
}

func (p *Pool) allGroupsSpent() bool {
	if len(p.groups) == 0 {
		return false
	}
	for _, group := range p.groups {
		if group.reserved < p.groupConfig.UsageLimit {
			return false
		}
	}
	return true
}

func monthOf(now time.Time, location *time.Location) month {
	local := now.In(location)
	return month{year: local.Year(), month: local.Month()}
}

func (p *Pool) rollbackGroupEstimate(lease Lease) {
	if lease.group >= 0 && lease.group < len(p.groups) {
		p.groups[lease.group].reserved = max(0, p.groups[lease.group].reserved-lease.Estimate)
	}
}

func (p *Pool) eligible(state *keyState, now time.Time, selection Selection) bool {
	if !state.ready || state.limit <= 0 || state.remaining() <= 0 || state.state == StateExhausted || state.state == StateProbing {
		return false
	}
	if state.remaining() < selection.Estimate {
		return false
	}
	if selection.Workload == WorkloadResearch && p.researchBlocked(state) {
		return false
	}
	return state.state != StateCooling || !now.Before(state.retryAt)
}

func (p *Pool) weight(state *keyState, now time.Time) float64 {
	if !p.eligible(state, now, Selection{}) {
		return 0
	}
	return state.remaining()
}

func (s *keyState) remaining() float64 {
	return max(0, float64(s.limit-s.realUsage)-s.estimated)
}

func (p *Pool) removeReservation(id uint64, rollbackGroup bool) {
	reservation, ok := p.reservations[id]
	if !ok {
		return
	}
	state := p.keys[reservation.lease.Key.Name]
	state.estimated = max(0, state.estimated-reservation.lease.Estimate)
	if rollbackGroup {
		p.rollbackGroupEstimate(reservation.lease)
	}
	delete(p.reservations, id)
}

func (p *Pool) researchReserved(name string) float64 {
	total := 0.0
	for _, reservation := range p.reservations {
		if reservation.lease.Key.Name == name && reservation.persistent && !reservation.settled {
			total += reservation.lease.Estimate
		}
	}
	return total
}

func (p *Pool) researchBlocked(state *keyState) bool {
	if state.researchBlockedAt == nil {
		return false
	}
	if state.remaining() > *state.researchBlockedAt {
		state.researchBlockedAt = nil
		return false
	}
	return true
}
