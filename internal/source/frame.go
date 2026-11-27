// Package source finds the file a profiled frame was compiled from, inside
// one directory of the machine dtui runs on, and words the link that opens
// it in an editor. It knows no terminal framework and nothing of Dynatrace.
//
// A frame's label comes from the environment and is text an attacker may
// have chosen. It is never run, never joined to a path and never opened: it
// is only compared with the names of files a walk of the directory found
// (docs/adr/0023-flame-graph-is-a-bespoke-screen.md).
package source

import "strings"

// Notation is the way a label spells a frame.
type Notation int

const (
	// Unknown is a label with no function in it.
	Unknown Notation = iota
	// Dotted is package.Type.function, as the JVM and .NET spell a frame.
	Dotted
	// Go is import/path.Type.Function or import/path.Function.
	Go
	// Path is path/file.ext:function, as a script's frame carries its file.
	Path
)

// Frame is a label taken apart. The parts are the label's own text.
type Frame struct {
	Notation Notation
	// Package is the package, namespace or import path; "" without one.
	Package string
	// Type is the class or receiver as spelled, nested classes included.
	Type string
	// Function is the method or function, without parameters.
	Function string
	// File is the path a Path frame carries.
	File string
	// Runtime names the runtime or standard library a frame belongs to; ""
	// for a frame that may be the application's.
	Runtime string
}

// runtimes are the packages of the runtimes themselves: their source is not
// in an application's repository, so nothing is searched for them.
var runtimes = []struct{ prefix, name string }{
	{"java.", "the JDK"}, {"javax.", "the JDK"}, {"jdk.", "the JDK"}, {"sun.", "the JDK"}, {"com.sun.", "the JDK"},
	{"kotlin.", "the Kotlin standard library"}, {"kotlinx.", "the Kotlin standard library"},
	{"scala.", "the Scala standard library"},
	{"System.", ".NET"}, {"Microsoft.", ".NET"},
}

// goStd are the first elements of the import paths of Go's standard library.
// It is a list and not a rule: "no domain in the first element" would also
// refuse an application whose module is named without one.
var goStd = map[string]bool{
	"archive": true, "bufio": true, "builtin": true, "bytes": true, "cmp": true, "compress": true,
	"container": true, "context": true, "crypto": true, "database": true, "debug": true, "embed": true,
	"encoding": true, "errors": true, "expvar": true, "flag": true, "fmt": true, "go": true, "hash": true,
	"html": true, "image": true, "index": true, "internal": true, "io": true, "iter": true, "log": true,
	"maps": true, "math": true, "mime": true, "net": true, "os": true, "path": true, "plugin": true,
	"reflect": true, "regexp": true, "runtime": true, "slices": true, "sort": true, "strconv": true,
	"strings": true, "structs": true, "sync": true, "syscall": true, "testing": true, "text": true,
	"time": true, "unicode": true, "unique": true, "unsafe": true, "vendor": true, "weak": true,
}

// dependencies are the directories package managers keep other people's code
// in. A script whose path passes through one is not the application's, and
// the walk does not index them either.
var dependencies = map[string]bool{
	"node_modules": true, "bower_components": true, "site-packages": true, "dist-packages": true, "vendor": true,
}

// scripts are the extensions a Path frame's file may have.
var scripts = []string{".js", ".mjs", ".cjs", ".jsx", ".ts", ".mts", ".cts", ".tsx", ".py", ".rb", ".php"}

// Parse takes a label apart. It never fails: a label it cannot read has the
// notation Unknown.
func Parse(label string) Frame {
	if f, ok := parsePath(label); ok {
		return f
	}
	label = withoutHiddenClassAddress(label)
	// A parameter list, which Go's (*Type) receiver is not.
	if i := strings.IndexByte(label, '('); i > 0 && !strings.HasPrefix(label[i:], "(*") && !strings.Contains(label[:i], "/") {
		label = label[:i]
	}
	// Type parameters hold dots and slashes of their own ([go.shape.int],
	// [*example.com/pkg.T]): they say nothing of where the frame is.
	label = withoutTypeParameters(label)
	if strings.Contains(label, "/") || strings.Contains(label, "(*") {
		return parseGo(label)
	}
	head, function := cutLast(label, '.')
	// .NET spells a constructor Type..ctor: the second dot is the name's.
	if (function == "ctor" || function == "cctor") && strings.HasSuffix(head, ".") {
		head, function = strings.TrimSuffix(head, "."), "."+function
	}
	if function == "" {
		return Frame{}
	}
	f := Frame{Notation: Dotted, Function: function}
	if head != "" {
		f.Package, f.Type = cutLast(head, '.')
		if f.Type == "" {
			f.Package, f.Type = f.Type, f.Package
		}
	}
	for _, r := range runtimes {
		if strings.HasPrefix(label, r.prefix) {
			f.Runtime = r.name
		}
	}
	// Go's standard library spelled without a slash: time.Now, runtime.main,
	// time.Time.Add. Only a package of one name is taken for it, so that
	// io.example.Type.method stays an application's.
	if std := f.Package; (std == "" && goStd[f.Type]) || goStd[std] {
		f.Runtime = goStdName
	}
	return f
}

const goStdName = "the Go standard library"

// withoutHiddenClassAddress drops the address the JVM appends to the name of
// a hidden class (CartService$$Lambda$14/0x000000080012.run): the slash is
// no import path's.
func withoutHiddenClassAddress(label string) string {
	at := strings.Index(label, "/0x")
	if at < 0 || !strings.Contains(label[:at], "$") {
		return label
	}
	end := at + len("/0x")
	for end < len(label) && isHex(label[end]) {
		end++
	}
	if end == at+len("/0x") || (end < len(label) && label[end] != '.') {
		return label
	}
	return label[:at] + label[end:]
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// withoutTypeParameters drops every bracketed list of a label, nested ones
// with the list they are in. A label whose brackets do not pair is left as
// it is.
func withoutTypeParameters(label string) string {
	if !strings.Contains(label, "[") {
		return label
	}
	var b strings.Builder
	depth := 0
	for i := 0; i < len(label); i++ {
		switch c := label[i]; {
		case c == '[':
			depth++
		case c == ']' && depth > 0:
			depth--
		case depth == 0:
			b.WriteByte(c)
		}
	}
	if depth != 0 {
		return label
	}
	return b.String()
}

// cutLast cuts s at its last sep; without one, all of s is after it.
func cutLast(s string, sep byte) (before, after string) {
	if i := strings.LastIndexByte(s, sep); i >= 0 {
		return s[:i], s[i+1:]
	}
	return "", s
}

func parsePath(label string) (Frame, bool) {
	i := strings.LastIndexByte(label, ':')
	if i < 1 {
		return Frame{}, false
	}
	file, function := label[:i], label[i+1:]
	// A parameter list after the name; "(anonymous)" is a name.
	if open := strings.IndexByte(function, '('); open > 0 && strings.HasSuffix(function, ")") {
		function = function[:open]
	}
	if strings.HasPrefix(file, "node:") {
		return Frame{Notation: Path, File: file, Function: function, Runtime: "Node.js"}, true
	}
	for _, ext := range scripts {
		if !strings.HasSuffix(file, ext) {
			continue
		}
		f := Frame{Notation: Path, File: file, Function: function}
		for _, dir := range strings.FieldsFunc(file, func(r rune) bool { return r == '/' || r == '\\' }) {
			if dependencies[dir] {
				f.Runtime = "a dependency (" + dir + ")"
			}
		}
		return f, true
	}
	return Frame{}, false
}

func parseGo(label string) Frame {
	// The package ends at the first dot after the last slash.
	slash := strings.LastIndexByte(label, '/')
	dot := strings.IndexByte(label[slash+1:], '.')
	if dot < 0 {
		return Frame{}
	}
	f := Frame{Notation: Go, Package: label[:slash+1+dot]}
	// A method value is named after its method: AddItem-fm.
	rest := strings.TrimSuffix(label[slash+1+dot+1:], "-fm")
	parts := strings.Split(rest, ".")
	// Closures are named after the function they are in: F.func1, F.func1.2.
	for len(parts) > 1 && closure(parts[len(parts)-1]) {
		parts = parts[:len(parts)-1]
	}
	switch {
	case len(parts) == 1:
		f.Function = parts[0]
	default:
		f.Type = strings.Trim(parts[0], "(*)")
		f.Function = parts[1]
	}
	if f.Function == "" {
		return Frame{}
	}
	if first, _, _ := strings.Cut(f.Package, "/"); goStd[first] {
		f.Runtime = goStdName
	}
	return f
}

func closure(part string) bool {
	if part == "" {
		return false
	}
	part = strings.TrimPrefix(part, "func")
	part = strings.TrimPrefix(part, "gowrap")
	for i := 0; i < len(part); i++ {
		if part[i] < '0' || part[i] > '9' {
			return false
		}
	}
	return true
}

// OuterType is the type a source file is named after: the class without its
// nested and generated classes.
func (f Frame) OuterType() string {
	t := f.Type
	for _, sep := range "$+`" {
		if i := strings.IndexRune(t, sep); i > 0 {
			t = t[:i]
		}
	}
	return t
}

// Symbol is the name to look for in the file: the function, the function a
// lambda was written in, or the type for a constructor. It is "" when the
// frame has no name a source file spells (a static initializer, an
// anonymous function).
func (f Frame) Symbol() string {
	fn := f.Function
	switch {
	case fn == "<init>" || fn == ".ctor":
		return f.OuterType()
	case fn == ".cctor":
		return "" // a static constructor, like <clinit>
	case strings.HasPrefix(fn, "lambda$"):
		// lambda$method$3
		name, _, _ := strings.Cut(strings.TrimPrefix(fn, "lambda$"), "$")
		return name
	case strings.HasPrefix(fn, "<") && strings.Contains(fn, ">") && !strings.HasSuffix(fn, ">"):
		// A C# closure: <Method>b__0
		return fn[1:strings.IndexByte(fn, '>')]
	}
	if fn == "" || strings.ContainsAny(fn, "<>()") {
		return ""
	}
	return fn
}
