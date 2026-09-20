package keys

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"
)

// validateSequenceStart rejects a sequence whose first Key could never
// actually begin it once the Sequencer processes real keypresses: <Esc>
// unconditionally resets the pending buffer before it is ever compared
// against the keymap (see Sequencer.Feed), so any sequence starting with it
// — including <Esc> alone — could never fire; a leading digit is reserved
// for the Sequencer's numeric count prefix, so a multi-key sequence
// starting with one would never see its digit as part of the sequence
// (only as a count), and even a single bare digit is rejected for the same
// reason a config author would expect it to behave like <Esc>: reserved,
// not bindable.
func validateSequenceStart(seq []Key) error {
	if len(seq) == 0 {
		return nil // Parse already rejects empty input; defensive only.
	}
	first := seq[0]
	if first.Kind == KindSpecial && first.Special == tcell.KeyEsc {
		return errors.New("sequence cannot start with <Esc>: Esc always resets the pending key sequence, so a binding starting with it could never fire")
	}
	if _, ok := digitValue(first); ok {
		return errors.New("sequence cannot start with a digit: digits are reserved for the numeric count prefix, so a binding starting with one could never fire")
	}
	return nil
}

// MatchKind reports how a key sequence relates to the bindings of a context.
type MatchKind int

const (
	// NoMatch means the sequence matches no binding and is not a prefix of
	// one either.
	NoMatch MatchKind = iota
	// Prefix means the sequence is not itself bound, but is a prefix of a
	// longer bound sequence: the caller should keep waiting for more keys.
	Prefix
	// Exact means the sequence is bound to an Action.
	Exact
)

// Binding pairs a bound key sequence (in vim notation) with the Action it
// triggers, as returned by Keymap.Bindings for building the "?" help
// overlay.
type Binding struct {
	Sequence string
	Action   Action
}

// binding is Keymap's internal, pre-resolution form: seq is the raw Key
// slice (not a string — see ctxTable's doc comment for why comparing
// rendered strings is not safe for a prefix check).
type binding struct {
	ctx    Context
	seq    []Key
	action Action
}

// ctxTable is one context's compiled lookup table. exact maps the
// canonical string form of a bound sequence (see sequenceKey) to its
// Action, for an O(1) exact-match lookup; entries additionally keeps every
// bound sequence's raw Key slice, compared element-wise for the "is this a
// prefix of a longer bound sequence" check. Comparing the canonical
// *strings* for that check is not safe: a rune key's own rendered notation
// can coincide with the leading characters of an unrelated special key's
// bracketed notation — the literal rune "<" renders as the single
// character "<", which is also the leading character of "<Enter>",
// "<C-d>", and every other bracketed name, even though "<" shares no key
// in common with any of them.
type ctxTable struct {
	exact   map[string]Action
	entries []binding
}

func (t ctxTable) lookup(seq []Key) (Action, MatchKind) {
	if a, ok := t.exact[sequenceKey(seq)]; ok {
		return a, Exact
	}
	for _, e := range t.entries {
		if isKeyPrefix(seq, e.seq) {
			return "", Prefix
		}
	}
	return "", NoMatch
}

// isKeyPrefix reports whether short is a proper (strictly shorter) prefix
// of long, comparing Keys element-wise. Key is a plain comparable struct
// (an int Kind, a rune, a tcell.Key, and a tcell.ModMask), so == is exact
// and unambiguous, unlike comparing each Key's own String() rendering.
func isKeyPrefix(short, long []Key) bool {
	if len(short) >= len(long) {
		return false
	}
	for i := range short {
		if short[i] != long[i] {
			return false
		}
	}
	return true
}

// Keymap resolves a (Context, key sequence) pair to an Action. Build one
// with Defaults, then Merge in a user's config overrides.
type Keymap struct {
	entries []binding
	tables  map[Context]ctxTable
}

// sequenceKey joins a slice of Keys into the canonical string a Keymap
// uses as its exact-match map key and shows in help text and error
// messages. A NUL byte separates each Key's own rendering, so a sequence's
// canonical string is (for any keymap actually reachable through Parse,
// which never produces a NUL byte itself) uniquely determined by its Keys
// — this is not relied on for the *prefix* check (see ctxTable's doc
// comment), only for exact-match map keys and display text.
func sequenceKey(seq []Key) string {
	var b strings.Builder
	for i, k := range seq {
		if i > 0 {
			b.WriteByte(0)
		}
		b.WriteString(k.String())
	}
	return b.String()
}

// build compiles a flat binding list into per-context lookup tables,
// validating that no two different actions claim the same (context,
// sequence) pair, that no bound sequence in a context is itself a prefix
// of another bound sequence in that same context, and that no non-global
// context's binding is equal to, a prefix of, or prefixed by a global
// binding — since Keymap.Lookup and Sequencer.Feed both fall back from a
// specific context to ContextGlobal only when the specific context has no
// match at all, any of these three relationships would make one side of
// the pair permanently unreachable while that context has focus, which the
// Sequencer could never resolve without a timeout.
func build(entries []binding) (Keymap, error) {
	tables := make(map[Context]ctxTable, len(entries))
	var errs []error

	for _, e := range entries {
		t := tables[e.ctx]
		if t.exact == nil {
			t.exact = map[string]Action{}
		}
		key := sequenceKey(e.seq)
		if existing, ok := t.exact[key]; ok && existing != e.action {
			errs = append(errs, fmt.Errorf("keys: %q is bound to both %q and %q in context %q", key, existing, e.action, e.ctx))
			tables[e.ctx] = t
			continue
		}
		t.exact[key] = e.action
		t.entries = append(t.entries, e)
		tables[e.ctx] = t
	}

	for ctx, t := range tables {
		for _, a := range t.entries {
			for _, b := range t.entries {
				if isKeyPrefix(a.seq, b.seq) {
					errs = append(errs, fmt.Errorf("keys: %q is bound in context %q but is also a prefix of %q", sequenceKey(a.seq), ctx, sequenceKey(b.seq)))
				}
			}
		}
	}

	if global, ok := tables[ContextGlobal]; ok {
		for ctx, t := range tables {
			if ctx == ContextGlobal {
				continue
			}
			for _, a := range t.entries {
				for _, g := range global.entries {
					switch {
					case sequenceKey(a.seq) == sequenceKey(g.seq):
						errs = append(errs, fmt.Errorf("keys: action %q's %q in context %q equals global action %q's sequence; since global is only checked when %q has no match at all, one binding would always shadow the other", a.action, sequenceKey(a.seq), ctx, g.action, ctx))
					case isKeyPrefix(a.seq, g.seq):
						errs = append(errs, fmt.Errorf("keys: action %q's %q in context %q is a prefix of global action %q's %q, which would never be reachable while %q has focus", a.action, sequenceKey(a.seq), ctx, g.action, sequenceKey(g.seq), ctx))
					case isKeyPrefix(g.seq, a.seq):
						errs = append(errs, fmt.Errorf("keys: action %q's %q in context %q is prefixed by global action %q's %q, which would block it from ever resolving while %q has focus", a.action, sequenceKey(a.seq), ctx, g.action, sequenceKey(g.seq), ctx))
					}
				}
			}
		}
	}

	if err := errors.Join(errs...); err != nil {
		return Keymap{}, err
	}
	return Keymap{entries: entries, tables: tables}, nil
}

// Lookup resolves seq against ctx's bindings, falling back to
// ContextGlobal when ctx has no match at all (neither Exact nor Prefix) for
// seq.
func (km Keymap) Lookup(ctx Context, seq []Key) (Action, MatchKind) {
	if a, kind := km.tables[ctx].lookup(seq); kind != NoMatch {
		return a, kind
	}
	if ctx != ContextGlobal {
		return km.tables[ContextGlobal].lookup(seq)
	}
	return "", NoMatch
}

// Bindings returns every binding registered directly in ctx (not including
// anything inherited via the ContextGlobal fallback), sorted by sequence for
// stable, deterministic output — used to render the "?" help overlay.
func (km Keymap) Bindings(ctx Context) []Binding {
	t := km.tables[ctx]
	out := make([]Binding, 0, len(t.entries))
	for _, e := range t.entries {
		out = append(out, Binding{Sequence: sequenceKey(e.seq), Action: e.action})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Sequence < out[j].Sequence })
	return out
}

// Merge builds a Keymap starting from defaults and applying overrides,
// where each overrides key is an Action ID (see AllActions) and its value a
// vim-notation key sequence (see Parse). An override replaces every default
// binding of that action — in every context the action was bound in — with
// one new binding at the given sequence. Errors (unknown action IDs,
// unparsable sequences, and any duplicate-sequence or prefix conflict the
// resulting keymap would have) are joined and returned together rather than
// stopping at the first one, so a config error report can name every
// offending key at once.
func Merge(defaults Keymap, overrides map[string]string) (Keymap, error) {
	known := make(map[Action]bool, len(AllActions()))
	for _, a := range AllActions() {
		known[a] = true
	}

	entries := append([]binding(nil), defaults.entries...)
	var errs []error

	for actionID, seqStr := range overrides {
		action := Action(actionID)
		if !known[action] {
			errs = append(errs, fmt.Errorf("keys: unknown action %q", actionID))
			continue
		}
		parsed, err := Parse(seqStr)
		if err != nil {
			errs = append(errs, fmt.Errorf("keys: action %q: %w", actionID, err))
			continue
		}
		if err := validateSequenceStart(parsed); err != nil {
			errs = append(errs, fmt.Errorf("keys: action %q: %w", actionID, err))
			continue
		}

		ctxSet := map[Context]struct{}{}
		for _, e := range entries {
			if e.action == action {
				ctxSet[e.ctx] = struct{}{}
			}
		}

		filtered := make([]binding, 0, len(entries))
		for _, e := range entries {
			if e.action != action {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
		for ctx := range ctxSet {
			entries = append(entries, binding{ctx: ctx, seq: parsed, action: action})
		}
	}

	if err := errors.Join(errs...); err != nil {
		return Keymap{}, err
	}
	return build(entries)
}
