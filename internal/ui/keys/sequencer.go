package keys

import "github.com/gdamore/tcell/v2"

// Result is what Sequencer.Feed reports after accepting one Key.
type Result struct {
	// Action is set when Feed resolved a complete sequence.
	Action Action
	// Count is the numeric prefix the user typed before the sequence
	// (e.g. 3 for "3j"), or 1 when none was typed.
	Count int
	// Pending reports that Feed is waiting for more keys: the buffer so
	// far (a count, a prefix like "g", or both) is not yet a complete
	// sequence.
	Pending bool
	// Consumed reports whether Feed used the key at all. A key that is
	// not consumed matches no binding, in any of the given contexts, at
	// any prefix length, and should be handled by whatever comes next in
	// the router (typically: dropped, or forwarded to a focused
	// tview primitive).
	Consumed bool
}

// Sequencer accumulates keys into gprt's multi-key bindings (gg, <C-w>l, a
// numeric count before a motion) with no timers: a sequence resolves the
// moment it becomes unambiguous, and an abandoned prefix (a key that
// continues no bound sequence) is dropped so the abandoning key can be
// resolved fresh on its own.
type Sequencer struct {
	km      Keymap
	pending []Key
	count   int
}

// NewSequencer creates a Sequencer resolving sequences against km.
func NewSequencer(km Keymap) *Sequencer {
	return &Sequencer{km: km}
}

// Feed accepts one normalized Key. ctxs is the ordered list of contexts to
// resolve the accumulated sequence against — normally the currently focused
// pane's context followed by ContextGlobal, matching Keymap.Lookup's own
// fallback order.
func (s *Sequencer) Feed(k Key, ctxs []Context) Result {
	if isEsc(k) {
		hadState := len(s.pending) > 0 || s.count > 0
		s.reset()
		return Result{Consumed: hadState}
	}

	if len(s.pending) == 0 {
		if d, ok := digitValue(k); ok && (d != 0 || s.count > 0) && !s.boundAlone(k, ctxs) {
			s.count = s.count*10 + d
			return Result{Pending: true, Consumed: true, Count: s.count}
		}
	}

	s.pending = append(s.pending, k)
	action, kind := s.resolve(ctxs)

	switch kind {
	case Exact:
		count := s.effectiveCount()
		s.reset()
		return Result{Action: action, Count: count, Consumed: true}
	case Prefix:
		return Result{Pending: true, Consumed: true, Count: s.effectiveCount()}
	default: // NoMatch
		abandoning := len(s.pending) > 1
		s.pending = nil
		if abandoning {
			// The just-fed key continues no bound sequence: drop the
			// abandoned prefix and resolve this key fresh, as if it had
			// arrived with an empty buffer.
			return s.Feed(k, ctxs)
		}
		s.count = 0
		return Result{Consumed: false}
	}
}

func (s *Sequencer) resolve(ctxs []Context) (Action, MatchKind) {
	for _, ctx := range ctxs {
		if a, kind := s.km.tables[ctx].lookup(s.pending); kind != NoMatch {
			return a, kind
		}
	}
	return "", NoMatch
}

// boundAlone reports whether k, on its own, is already an exact binding in
// any of ctxs — used to decide whether a digit key should start a count
// prefix or be treated as an ordinary bound key.
func (s *Sequencer) boundAlone(k Key, ctxs []Context) bool {
	for _, ctx := range ctxs {
		if _, kind := s.km.tables[ctx].lookup([]Key{k}); kind == Exact {
			return true
		}
	}
	return false
}

func (s *Sequencer) effectiveCount() int {
	if s.count == 0 {
		return 1
	}
	return s.count
}

func (s *Sequencer) reset() {
	s.pending = nil
	s.count = 0
}

func isEsc(k Key) bool {
	return k.Kind == KindSpecial && k.Special == tcell.KeyEsc
}

// digitValue reports the numeric value of k when it is a plain '0'-'9'
// rune with no modifier.
func digitValue(k Key) (int, bool) {
	if k.Kind != KindRune || k.Mod != 0 {
		return 0, false
	}
	if k.Rune < '0' || k.Rune > '9' {
		return 0, false
	}
	return int(k.Rune - '0'), true
}
