package catalog

import (
	"regexp"
	"strings"
)

// .NET traces: "at Ns.Type.Method(String arg) in File.cs:line 1" frames. An
// exception that wraps another prints the inner one first, closed by an
// "End of inner exception stack trace" line. Mono writes a space before the
// parameters: "at Ns.Type.Method (System.String arg) [0x0001a] in <…>:0".

var (
	// The method name, the space Mono puts after it, and the parameters.
	dotnetFrameRe = regexp.MustCompile("^at ([\\w.<>|$`\\[\\],+]+)( ?)\\(([^()]*)\\)")
	// Mono's notation is close to prose ("at checkout (final step)"), so
	// there the name must be qualified and every parameter must start with
	// a type name.
	dotnetMonoParamsRe = regexp.MustCompile("^(?:[A-Z][\\w.`\\[\\]<>&*+]*(?: \\w+)?(?:, |$))*$")
	dotnetTypeArgsRe   = regexp.MustCompile(`\[[^\]]*\]`)
	// Compiler-generated names: async state machines, closure classes and
	// the lambdas in them.
	dotnetAsyncRe   = regexp.MustCompile(`<(\w+)>d__\d+\.MoveNext$`)
	dotnetClosureRe = regexp.MustCompile(`<>c(?:__DisplayClass\d+_\d+)?\.`)
	dotnetLambdaRe  = regexp.MustCompile(`<(\w+)>b__\d+(?:_\d+)?`)
)

// dotnetVendorPrefixes are the runtime and framework namespaces. Matching is
// case-sensitive: "System." is .NET, "system." is somebody's namespace.
var dotnetVendorPrefixes = []string{
	"System.", "Microsoft.", "Newtonsoft.", "Grpc.", "OpenTelemetry.", "Dynatrace.",
}

const dotnetInnerEnd = "--- End of inner exception stack trace"

func dotnetFrame(line string) (StackFrame, bool) {
	m := dotnetFrameRe.FindStringSubmatch(line)
	if m == nil {
		return StackFrame{}, false
	}
	name := m[1]
	if m[2] != "" && (!strings.Contains(name, ".") || !dotnetMonoParamsRe.MatchString(m[3])) {
		return StackFrame{}, false
	}
	name = dotnetTypeArgsRe.ReplaceAllString(name, "")
	name = dotnetAsyncRe.ReplaceAllString(name, "$1")
	name = dotnetClosureRe.ReplaceAllString(name, "")
	name = dotnetLambdaRe.ReplaceAllString(name, "$1")

	module, function := "", name
	if dot := strings.LastIndexByte(name, '.'); dot >= 0 {
		module, function = name[:dot], name[dot+1:]
	}
	// Constructors are "Type..ctor".
	if trimmed, ok := strings.CutSuffix(module, "."); ok {
		module, function = trimmed, "."+function
	}
	if function == "" {
		return StackFrame{}, false
	}
	return StackFrame{Module: module, Function: function, Syntax: StackDotNet}, true
}

// dotnetRootCause returns the frames of the innermost exception, which is
// printed first.
func dotnetRootCause(lines []string) []StackFrame {
	var out []StackFrame
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(out) > 0 && strings.HasPrefix(line, dotnetInnerEnd) {
			break
		}
		if f, ok := dotnetFrame(line); ok {
			out = append(out, f)
		}
	}
	return out
}

func dotnetInApp(f StackFrame) bool {
	return f.Module != "" && !hasAnyPrefix(f.Module, dotnetVendorPrefixes)
}
