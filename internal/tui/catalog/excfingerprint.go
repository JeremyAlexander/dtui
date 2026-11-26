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
// into a key. Deeper frames are the callers, and one bug has many.
const FingerprintFrames = 5

// fingerprintKeyLen is the number of hex characters kept from the hash.
const fingerprintKeyLen = 12

// FingerprintRung is the step of the fallback ladder that produced a key,
// most specific first.
type FingerprintRung int

const (
	// RungAppFrames: type + the top in-app frames.
	RungAppFrames FingerprintRung = iota + 1
	// RungFrames: type + the top frames, none of them in-app.
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
	// Type and Message describe the event the key was computed from, the
	// message parameterised (ParameterizeMessage). Message is set on every
	// rung, hashed only on RungMessage.
	Type    string
	Message string
	// Frames are the frames that went into the key; nil below RungFrames.
	Frames []StackFrame
}

// FingerprintExceptions computes the grouping key for the exception events
// of one span (false when there are none). The event is the root of the
// cause chain, the last one where no chain is marked. classify decides which
// frames are application code; nil means DefaultFrameClassifier.
func FingerprintExceptions(excs []SpanException, classify FrameClassifier) (ExceptionFingerprint, bool) {
	if len(excs) == 0 {
		return ExceptionFingerprint{}, false
	}
	if classify == nil {
		classify = DefaultFrameClassifier
	}
	root := rootException(excs)
	fp := ExceptionFingerprint{
		Rung:    RungType,
		Type:    strings.TrimSpace(root.Type),
		Message: ParameterizeMessage(root.Message),
	}

	all := ParseStackFrames(root.StackTrace)
	var app []StackFrame
	for _, f := range all {
		if classify.InApp(f) {
			app = append(app, f)
		}
	}
	// Dropping the framework frames between two calls of one function
	// leaves a repeat behind; it is the same recursion.
	app = collapseFrames(app)

	var evidence []string
	switch {
	case len(app) > 0:
		fp.Rung, fp.Frames = RungAppFrames, app[:min(len(app), FingerprintFrames)]
	case len(all) > 0:
		fp.Rung, fp.Frames = RungFrames, all[:min(len(all), FingerprintFrames)]
	case fp.Message != "":
		fp.Rung, evidence = RungMessage, []string{fp.Message}
	}
	for _, f := range fp.Frames {
		evidence = append(evidence, f.Module, f.Function)
	}
	fp.Key = fingerprintKey(fp.Rung, fp.Type, evidence)
	return fp, true
}

// fingerprintKey hashes the canonical form of a fingerprint: the rung's
// number, the type and the evidence fields in order (a message, or a module
// and a function per frame). Each field is written behind its length in
// bytes ("7:addItem"), so no field can run into the next whatever it holds.
func fingerprintKey(rung FingerprintRung, typ string, evidence []string) string {
	canonical := appendKeyField(nil, strconv.Itoa(int(rung)))
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
