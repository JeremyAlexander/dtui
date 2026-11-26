package catalog

import (
	"regexp"
	"strconv"
	"strings"
)

// Java traces: "at pkg.Class.method(File.java:1)" frames under a
// "Type: message" line, the causes of an exception in "Caused by:" sections
// after it.

var (
	// A frame with or without "at" (OneAgent call stacks and some log
	// formats leave it out). The source location in the parentheses is what
	// tells a frame from prose and from a .NET frame, whose parentheses
	// hold parameters.
	javaFrameRe = regexp.MustCompile(
		`^(?:at )?([\w$.<>/@-]+\.[\w$<>]+) ?\((?:[\w$-]+\.\w+(?::\d+)?|Native Method|Unknown Source|<generated>)\)`)
	javaMoreRe = regexp.MustCompile(`^\.\.\. (\d+) (?:more|common frames omitted)`)

	// Classes generated at run time are named after the class they stand in
	// for plus a suffix that differs in every process: CGLIB subclasses
	// ("$$EnhancerBy…$$1a2b", "$$FastClassBy…$$3c4d", "$$SpringCGLIB$$0"),
	// Hibernate, ByteBuddy and Mockito proxies ("$HibernateProxy$kL2x9A"),
	// javassist ("_$$_jvst3c0_1f"); then reflection accessors, dynamic
	// proxies, and the numbered package the JDK puts proxies in.
	javaProxyRe = regexp.MustCompile(
		`(?:\$\$(?:Enhancer|FastClass|SpringCGLIB)|\$(?:HibernateProxy|ByteBuddy|MockitoMock)\$|_\$\$_).*$`)
	javaGeneratedRe = regexp.MustCompile(`(?:Generated(?:Method|Constructor)Accessor|\$Proxy)\d+`)
	javaProxyPkgRe  = regexp.MustCompile(`^jdk\.proxy\d+\.`)
	javaLambdaRe    = regexp.MustCompile(`^lambda\$(.+)\$\d+$`)
	// A Scala closure is a class "Owner$$anonfun$method$1" up to 2.11 and a
	// method "$anonfun$method$1" since; either way it is code of the method.
	javaScalaClassRe  = regexp.MustCompile(`^(.*?)\$\$anonfun\$(\w+)\$\d+`)
	javaScalaMethodRe = regexp.MustCompile(`^\$anonfun\$(\w+)\$\d+(?:\$adapted)?$`)
)

// javaVendorPrefixes are the runtime and framework packages that are never
// the application's.
var javaVendorPrefixes = []string{
	"java.", "javax.", "jakarta.", "jdk.", "sun.", "com.sun.", "kotlin.", "kotlinx.", "scala.", "groovy.",
	"org.springframework.", "org.apache.", "org.hibernate.", "org.jboss.", "org.eclipse.jetty.", "org.slf4j.",
	"io.netty.", "io.grpc.", "io.undertow.", "io.vertx.", "io.quarkus.", "io.micrometer.", "io.opentelemetry.",
	"reactor.", "akka.", "okhttp3.", "ch.qos.logback.", "com.fasterxml.", "com.google.common.", "com.zaxxer.",
	"com.dynatrace.",
}

func javaFrame(line string) (StackFrame, bool) {
	m := javaFrameRe.FindStringSubmatch(line)
	if m == nil {
		return StackFrame{}, false
	}
	name := m[1]
	// A lambda's synthetic class ("Cart$$Lambda$14/0x0800.accept") is
	// plumbing between two real frames.
	if strings.Contains(name, "$$Lambda") {
		return StackFrame{}, false
	}
	// Module and class-loader prefixes: "java.base/", "app//".
	name = name[strings.LastIndexByte(name, '/')+1:]
	dot := strings.LastIndexByte(name, '.')
	module, function := name[:dot], name[dot+1:]
	function = javaLambdaRe.ReplaceAllString(function, "$1")
	function = javaScalaMethodRe.ReplaceAllString(function, "$1")
	if m := javaScalaClassRe.FindStringSubmatch(module); m != nil {
		module, function = m[1], m[2]
	}
	module = javaProxyRe.ReplaceAllString(module, FrameAuto)
	module = javaGeneratedRe.ReplaceAllString(module, FrameAuto)
	module = javaProxyPkgRe.ReplaceAllString(module, "jdk.proxy.")
	return StackFrame{Module: module, Function: function, Syntax: StackJava}, true
}

// javaSection is one exception of a trace: its frames, and how many frames
// of the enclosing exception a "... N more" line stands for.
type javaSection struct {
	frames []StackFrame
	more   int
}

// javaRootCause returns the frames of the last "Caused by:" section that has
// any, completed from the enclosing one where it ends in "... N more"; a
// last cause printed without frames (cut off, or thrown without a stack)
// leaves the one before it as the root.
//
// A "Suppressed:" block is left out with everything in it, including its own
// "Caused by:" sections. The block reaches as far as the lines are indented
// at least as deep as its first: Java indents a suppressed exception and its
// causes one step further than the exception they belong to. A trace whose
// indentation was stripped therefore ends at its first suppressed block.
func javaRootCause(lines []string) []StackFrame {
	sections := make([]javaSection, 1)
	suppressed := -1 // indentation of the suppressed block being skipped
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		indent := len(raw) - len(strings.TrimLeft(raw, " \t"))
		if suppressed >= 0 {
			if line == "" || indent >= suppressed {
				continue
			}
			suppressed = -1
		}
		cur := &sections[len(sections)-1]
		switch {
		case strings.HasPrefix(line, "Suppressed:"):
			suppressed = indent
		case strings.HasPrefix(line, "Caused by:"):
			sections = append(sections, javaSection{})
		default:
			if m := javaMoreRe.FindStringSubmatch(line); m != nil {
				cur.more, _ = strconv.Atoi(m[1])
			} else if f, ok := javaFrame(line); ok {
				cur.frames = append(cur.frames, f)
			}
		}
	}
	var full, root []StackFrame
	for _, sec := range sections {
		// "... N more": the last N frames of the enclosing exception.
		shared := min(sec.more, len(full))
		full = append(sec.frames[:len(sec.frames):len(sec.frames)], full[len(full)-shared:]...)
		if len(full) > 0 {
			root = full
		}
	}
	return root
}

func javaInApp(f StackFrame) bool {
	return !strings.Contains(f.Module, FrameAuto) && !hasAnyPrefix(f.Module, javaVendorPrefixes)
}
