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

// ExceptionKeyVersion prefixes every key. A change to the fingerprint, the
// frame parser or the message parameteriser that moves an existing
// exception to another key must raise it; the golden keys under
// testdata/exceptions fail until it does.
const ExceptionKeyVersion = "e1"

// FingerprintFrames is how many frames, counted from the throw site, go
// into a key besides the throw site itself. Deeper frames are the callers,
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
// of one span (false when there are none). The event is the root of the
// cause chain, the last one where no chain is marked. classify decides which
// frames are application code; nil means the built-in rules, the only ones
// whose keys are the same for everybody (see FrameClassifier).
func FingerprintExceptions(excs []SpanException, classify FrameClassifier) (ExceptionFingerprint, bool) {
	if len(excs) == 0 {
		return ExceptionFingerprint{}, false
	}
	inApp, classifier := builtinInApp, ""
	if classify != nil {
		inApp, classifier = classify.InApp, customClassifierField+classify.ID()
	}
	root := rootException(excs)
	fp := ExceptionFingerprint{
		Rung:      RungType,
		EventType: strings.TrimSpace(root.Type),
		Message:   ParameterizeMessage(root.Message),
	}
	fp.Type = fp.EventType

	// The type and the frames of a key come from one exception. Below the
	// frame rungs that is the event's, which the message belongs to as well.
	all, rootType := parseStack(root.StackTrace)
	if rootType != "" && len(all) > 0 {
		fp.Type = rootType
	}
	var evidence []string
	if app := topFrames(all, inApp); len(app) > 0 {
		fp.Rung, fp.Frames = RungAppFrames, app
	} else if len(all) > 0 {
		fp.Rung, fp.Frames = RungFrames, topFrames(all, func(StackFrame) bool { return true })
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

// topFrames picks the frames of a key from a root cause's frames: the throw
// site, then the frames below it that count and are not pass-through, until
// FingerprintFrames such frames are in (the throw site included where it is
// one). The throw site is part of every key whether it counts or not: what
// is thrown where tells two bugs apart even where the same application
// frames lead to both. nil when no frame counts.
func topFrames(all []StackFrame, counts func(StackFrame) bool) []StackFrame {
	if len(all) == 0 {
		return nil
	}
	top := []StackFrame{all[0]}
	counted := counts(all[0])
	left := FingerprintFrames
	if counted && !isPassThrough(all[0]) {
		left--
	}
	for _, f := range all[1:] {
		// Dropping the frames between two calls of one function leaves a
		// repeat behind; it is the same recursion.
		if left > 0 && counts(f) && !isPassThrough(f) && top[len(top)-1] != f {
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

// rootException picks the event a span's fingerprint is computed from: the
// one flagged as the root of the cause chain; else the end of a chain linked
// by ids (an exception that caused another and has no cause among the
// events); else the last event.
func rootException(excs []SpanException) SpanException {
	for _, ex := range excs {
		if ex.CauseRoot {
			return ex
		}
	}
	ids := map[string]bool{}
	causes := map[string]bool{}
	for _, ex := range excs {
		ids[ex.ID] = true
		causes[ex.CausedByID] = true
	}
	delete(ids, "") // an event without an id is nobody's cause
	for _, ex := range excs {
		if ex.ID != "" && causes[ex.ID] && !ids[ex.CausedByID] {
			return ex
		}
	}
	return excs[len(excs)-1]
}
