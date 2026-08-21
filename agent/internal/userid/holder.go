package userid

import (
	"sort"
	"strings"
	"sync"
)

// Holder guards the active policy. The server pushes a new policy on every
// heartbeat while process and foreground events resolve usernames concurrently,
// so reads and replacements have to be safe against each other.
type Holder struct {
	mu     sync.RWMutex
	policy *Policy
}

// NewHolder returns a holder seeded with the built-in defaults, which is what
// the agent uses until the first heartbeat lands (or forever, when no central
// server is configured).
func NewHolder() *Holder {
	return &Holder{policy: NewPolicy()}
}

// Set replaces the active policy.
func (h *Holder) Set(p *Policy) {
	if p == nil {
		return
	}
	h.mu.Lock()
	h.policy = p
	h.mu.Unlock()
}

// Resolve returns the canonical username and whether the account is ignored.
func (h *Holder) Resolve(raw string) (string, bool) {
	h.mu.RLock()
	p := h.policy
	h.mu.RUnlock()
	return p.Resolve(raw)
}

// FromServer builds a policy from the payload the server sends with each
// heartbeat. Alias keys are patterns (they may contain "*") and their values are
// the canonical username to merge into.
func FromServer(stripDomain bool, ignorePatterns []string, aliases map[string]string) *Policy {
	p := &Policy{StripDomain: stripDomain}
	patterns := make([]string, 0, len(aliases))
	for pattern := range aliases {
		patterns = append(patterns, pattern)
	}
	// Rules are first-match-wins, so order has to be deterministic across
	// heartbeats: exact patterns before wildcards, longer before shorter.
	sort.Slice(patterns, func(i, j int) bool {
		wi := strings.Contains(patterns[i], "*")
		wj := strings.Contains(patterns[j], "*")
		if wi != wj {
			return !wi
		}
		if len(patterns[i]) != len(patterns[j]) {
			return len(patterns[i]) > len(patterns[j])
		}
		return patterns[i] < patterns[j]
	})
	for _, pattern := range patterns {
		p.Rules = append(p.Rules, Rule{Pattern: pattern, Canonical: aliases[pattern]})
	}
	p.ExtraIgnored = append(p.ExtraIgnored, ignorePatterns...)
	return p
}
