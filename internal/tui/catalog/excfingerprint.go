package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// Exception fingerprinting: span exceptions carry no grouping key, so one is
// derived here. The key is a hash over the exception type plus the most
// specific stable evidence the event offers, found by walking a fallback
// ladder from in-app stack frames down to the type alone. Same input, same
// key — on every machine, in every locale.

// ExceptionKeyVersion prefixes every key. Once a release has shipped keys of
// a version, a change to the fingerprint, the frame parser or the message
// parameteriser that moves an exception to another key must raise it. Two
// golden files under testdata/exceptions stand in the way of doing that
// unnoticed: a digest of every table, expression and limit of these files
// (TestExceptionKeyInputsArePinned), and the keys of a corpus
// (TestExceptionKeysAreStable). The code of a function is covered by cases
// alone: a change to it shows only where the corpus or a test has an input
// it moves.
const ExceptionKeyVersion = "e1"

// FingerprintFrames is how many frames that count go into a key, from the
// throw site down: on the app-frames rung the application's frames that are
// not pass-through, on the all-frames rung every frame. The throw site is
// one of them where it counts. Where it does not (a runtime or pass-through
// frame above the application's) it is in the key on top of them, so a key
// holds at most FingerprintFrames+1 frames. Deeper frames are the callers,
// and one bug has many.
const FingerprintFrames = 5

// fingerprintKeyLen is the number of hex characters kept from the hash.
const fingerprintKeyLen = 12

// FingerprintRung is the step of the fallback ladder that produced a key,
// most specific first.
type FingerprintRung int

const (
	// RungAppFrames: type + the throw site + the top in-app frames.
	RungAppFrames FingerprintRung = iota + 1
	// RungFrames: type + the throw site + the top frames; no in-app frame
	// counts.
	RungFrames
	// RungMessage: type + the parameterised message; no frames parsed.
	RungMessage
	// RungType: the type alone.
	RungType
)

// String is the rung's label for display.
func (r FingerprintRung) String() string {
	switch r {
	case RungAppFrames:
		return "app frames"
	case RungFrames:
		return "frames"
	case RungMessage:
		return "message"
	case RungType:
		return "type"
	}
	return ""
}

// ExceptionFingerprint is the grouping key of one span's exception and what
// it was computed from.
type ExceptionFingerprint struct {
	Key  string // ExceptionKeyVersion + ":" + 12 hex characters
	Rung FingerprintRung
	// Type is the exception type that went into the key. It names the same
	// exception as Frames: where the stack text of the event holds a chain
	// ("Caused by:", an inner exception, an earlier traceback), that is the
	// root cause, whose type is read from the text.
	Type string
	// EventType is the exception.type of the event the key was computed
	// from, for display: the outermost exception where Type is a deeper one.
	EventType string
	// Message is the event's message, parameterised (ParameterizeMessage).
	// It is set on every rung and hashed only on RungMessage.
	Message string
	// Frames are the frames that went into the key, the throw site first;
	// nil below RungFrames.
	Frames []StackFrame
}

// FingerprintExceptions computes the grouping key for the exception events
// of one span. The event is the root of the cause chain, the last one where
// no chain is marked (rootException); false when no event says anything
// about an exception.
// classify decides which frames are application code; nil means the built-in
// rules, the only ones whose keys are the same for everybody (see
// FrameClassifier).
func FingerprintExceptions(excs []SpanException, classify FrameClassifier) (ExceptionFingerprint, bool) {
	root, ok := rootException(excs)
	if !ok {
		return ExceptionFingerprint{}, false
	}
	inApp, classifier := builtinInApp, ""
	if classify != nil {
		inApp, classifier = classify.InApp, customClassifierField+classify.ID()
	}
	fp := ExceptionFingerprint{
		Rung:      RungType,
		EventType: strings.TrimSpace(root.Type),
		Message:   ParameterizeMessage(root.Message),
	}
	fp.Type = fp.EventType

	// The type and the frames of a key come from one exception. Below the
	// frame rungs that is the event's, which the message belongs to as well.
	all, rootType, atFault := parseStack(root.StackTrace)
	if !atFault {
		// A Go trace that is not a panic's: where the error was seen.
		all = nil
	}
	if rootType != "" && len(all) > 0 {
		fp.Type = rootType
	}
	var evidence []string
	if app := topFrames(all, inApp, isPassThrough); len(app) > 0 {
		fp.Rung, fp.Frames = RungAppFrames, app
	} else if len(all) > 0 {
		fp.Rung, fp.Frames = RungFrames, topFrames(all, anyFrame, noFrame)
	} else if fp.Message != "" {
		fp.Rung, evidence = RungMessage, []string{fp.Message}
	}
	for _, f := range fp.Frames {
		evidence = append(evidence, f.Module, f.Function)
	}
	fp.Key = fingerprintKey(fp.Rung, classifier, fp.Type, evidence)
	return fp, true
}

// customClassifierField starts the classifier field of a key computed with
// a FrameClassifier; the field is empty for the built-in rules, so that even
// a classifier without an ID has keys of its own.
const customClassifierField = "custom:"

// anyFrame and noFrame are the two answers that do not depend on the frame.
func anyFrame(StackFrame) bool { return true }
func noFrame(StackFrame) bool  { return false }

// topFrames picks the frames of a key from a root cause's frames: the throw
// site, then the frames below it that count and do not only hand the call
// on, until FingerprintFrames such frames are in (the throw site included
// where it is one). The throw site is part of every key whether it counts
// or not: what is thrown where tells two bugs apart even where the same
// application frames lead to both. nil when no frame counts.
//
// Pass-through frames are left out only next to the application's other
// frames (handsOn is isPassThrough on the app-frames rung). On the
// all-frames rung every frame counts and none is left out: there a handler's
// or a filter's own frame may be all that tells it from the next one.
func topFrames(all []StackFrame, counts, handsOn func(StackFrame) bool) []StackFrame {
	if len(all) == 0 {
		return nil
	}
	top := []StackFrame{all[0]}
	counted := counts(all[0])
	left := FingerprintFrames
	if counted && !handsOn(all[0]) {
		left--
	}
	for _, f := range all[1:] {
		// Dropping the frames between two calls of one function leaves a
		// repeat behind; it is the same recursion.
		if left > 0 && counts(f) && !handsOn(f) && top[len(top)-1] != f {
			top, counted = append(top, f), true
			left--
		}
	}
	if !counted {
		return nil
	}
	return top
}

// fingerprintKey hashes the canonical form of a fingerprint: the rung's
// number, the classifier field, the type and the evidence fields in order (a
// message, or a module and a function per frame). Each field is written
// behind its length in bytes ("7:addItem"), so no field can run into the
// next whatever it holds.
func fingerprintKey(rung FingerprintRung, classifier, typ string, evidence []string) string {
	canonical := appendKeyField(nil, strconv.Itoa(int(rung)))
	canonical = appendKeyField(canonical, classifier)
	canonical = appendKeyField(canonical, typ)
	for _, field := range evidence {
		canonical = appendKeyField(canonical, field)
	}
	sum := sha256.Sum256(canonical)
	return ExceptionKeyVersion + ":" + hex.EncodeToString(sum[:])[:fingerprintKeyLen]
}

func appendKeyField(b []byte, field string) []byte {
	b = strconv.AppendInt(b, int64(len(field)), 10)
	b = append(b, ':')
	return append(b, field...)
}

// rootException picks the event a span's fingerprint is computed from, of
// those that say something about an exception (saysNothing); false when
// none does. The later event wins every tie, as the last one does where
// nothing marks a chain:
//
//  1. the last event flagged exception.is_caused_by_root;
//  2. else the last event that caused another (its exception.id is some
//     event's exception.caused_by_id) and has no cause among the events;
//  3. else the last event.
//
// Step 2 is unverified. The live spans seen so far carry exception.id and
// the root flag but no exception.caused_by_id, so their chains are found by
// step 1 and everything else by step 3; step 2 is kept for instrumentation
// that links its events without flagging one. It is a pass over the events,
// not a walk along the links, so a cycle or an id used twice cannot make it
// loop: a cycle has no event without a cause and falls through to step 3.
func rootException(excs []SpanException) (SpanException, bool) {
	for i := len(excs) - 1; i >= 0; i-- {
		if excs[i].CauseRoot && !saysNothing(excs[i]) {
			return excs[i], true
		}
	}
	ids := map[string]bool{}
	causes := map[string]bool{}
	for _, ex := range excs {
		ids[ex.ID] = true
		causes[ex.CausedByID] = true
	}
	delete(ids, "") // an event without an id is nobody's cause
	for i := len(excs) - 1; i >= 0; i-- {
		if ex := excs[i]; ex.ID != "" && causes[ex.ID] && !ids[ex.CausedByID] && !saysNothing(ex) {
			return ex, true
		}
	}
	for i := len(excs) - 1; i >= 0; i-- {
		if !saysNothing(excs[i]) {
			return excs[i], true
		}
	}
	return SpanException{}, false
}

// saysNothing reports whether an event has no type, no message and no stack
// trace. A key for it would be the same for every such event of every
// service, and as the last event of a span it would stand in for the real
// one before it.
func saysNothing(ex SpanException) bool {
	return strings.TrimSpace(ex.Type) == "" && strings.TrimSpace(ex.Message) == "" &&
		strings.TrimSpace(ex.StackTrace) == ""
}
