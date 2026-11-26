package source

import (
	"bufio"
	"context"
	"io"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
)

// How says what a match's line is.
type How int

const (
	// FileOnly is a file with no line: nothing in it could be told to be
	// the frame.
	FileOnly How = iota
	// TypeOnly is the line the frame's type is declared on: the function
	// was not found by name.
	TypeOnly
	// FirstOfSeveral is the first of several declarations of the function's
	// name (overloads): a guess among them.
	FirstOfSeveral
	// Declaration is the one line that declares the function's name.
	Declaration
)

// Match is a file a frame may have been compiled from.
type Match struct {
	// Path is relative to the index's root, with slashes.
	Path string
	// Line is 0 when no line is known. A profile carries no line numbers,
	// so a line is always where a declaration was found by its name.
	Line int
	How  How
}

const (
	// maxCandidates is the most files read for one frame, maxMatches the
	// most offered, and maxFileBytes the most read of one file.
	maxCandidates = 400
	maxMatches    = 9
	maxFileBytes  = 2 << 20
)

// Likely reports, without reading a file, whether the index has a file the
// frame could be in.
func (ix *Index) Likely(f Frame) bool {
	files, _ := ix.candidates(f)
	return len(files) > 0
}

// Resolve finds the files a frame may have been compiled from, the best
// first. A runtime's frame has none. It reads the candidate files, up to a
// bound, to find the line the function is declared on; unread is how many
// files that could hold the frame were beyond that bound, so that one match
// is not taken for the only one.
func (ix *Index) Resolve(ctx context.Context, f Frame) (matches []Match, unread int) {
	files, need := ix.candidates(f)
	if len(files) > maxCandidates {
		files, unread = files[:maxCandidates], len(files)-maxCandidates
	}
	for _, rel := range files {
		if ctx.Err() != nil {
			break
		}
		m, has := ix.locate(rel, f, need.pkg)
		if (need.function && !has.function) || (need.pkg != "" && !has.pkg) {
			continue
		}
		matches = append(matches, m)
	}
	slices.SortStableFunc(matches, func(a, b Match) int { return int(b.How) - int(a.How) })
	if len(matches) > maxMatches {
		matches = matches[:maxMatches]
	}
	return matches, unread
}

// needs is what a candidate file must hold to count, where its name and
// place say too little: a declaration of the function, or of the package.
type needs struct {
	function bool
	pkg      string
}

// candidates are the files of the index a frame's label points at, and what
// each must hold to be a match.
func (ix *Index) candidates(f Frame) (files []string, need needs) {
	if f.Runtime != "" || f.Notation == Unknown {
		return nil, needs{}
	}
	switch f.Notation {
	case Path:
		want := segments(strings.ReplaceAll(f.File, `\`, "/"), "/")
		if len(want) == 0 {
			return nil, needs{}
		}
		return bestBySuffix(ix.byName[want[len(want)-1]], want, 1, func(rel string) []string { return segments(rel, "/") }), needs{}
	case Go:
		return ix.goFiles(f.Package), needs{function: true}
	}
	pkg := segments(f.Package, ".")
	outer := f.OuterType()
	if outer == "" {
		return nil, needs{}
	}
	// A Kotlin file's functions are in a class named after the file.
	names := dedupe([]string{outer, strings.TrimSuffix(outer, "Kt")})
	dirOf := func(rel string) []string { return segments(path.Dir(rel), "/") }
	// The JVM: a class in the directory its package names.
	for _, name := range names {
		for _, ext := range []string{".java", ".kt", ".scala", ".groovy"} {
			files = append(files, bestBySuffix(ix.byName[name+ext], pkg, len(pkg), dirOf)...)
		}
	}
	// Python: package.module.function, or package.module.Class.method.
	files = append(files, bestBySuffix(ix.byName[outer+".py"], pkg, len(pkg), dirOf)...)
	if len(pkg) > 0 {
		files = append(files, bestBySuffix(ix.byName[pkg[len(pkg)-1]+".py"], pkg[:len(pkg)-1], len(pkg)-1, dirOf)...)
	}
	if len(files) > 0 {
		return dedupe(files), needs{}
	}
	// .NET, and a JVM language that does not keep its packages in
	// directories: a file named after the type, wherever it is, that
	// declares the package or namespace.
	for _, name := range names {
		for _, ext := range []string{".cs", ".fs", ".vb", ".kt", ".scala", ".groovy"} {
			files = append(files, ix.byName[name+ext]...)
		}
	}
	if len(files) > 0 {
		return dedupe(files), needs{pkg: f.Package}
	}
	// Go spelled without an import path: main.main.
	if f.Package == "" {
		return ix.goFiles(f.Type), needs{function: true}
	}
	return nil, needs{}
}

// goFiles are the Go files of the directory an import path ends in. A path
// no directory fits (package main, a module's root) leaves every Go file,
// each of which must then declare the function.
func (ix *Index) goFiles(pkg string) []string {
	pool := slices.DeleteFunc(slices.Clone(ix.byExt[".go"]), func(rel string) bool { return strings.HasSuffix(rel, "_test.go") })
	want := segments(pkg, "/")
	if best := bestBySuffix(pool, want, 1, func(rel string) []string { return segments(path.Dir(rel), "/") }); len(best) > 0 {
		return best
	}
	return pool
}

func segments(s, sep string) []string {
	return slices.DeleteFunc(strings.Split(s, sep), func(p string) bool { return p == "" || p == "." })
}

// bestBySuffix keeps the files whose own segments end like want, as far as
// the best of them does and at least atLeast segments far.
func bestBySuffix(files, want []string, atLeast int, of func(rel string) []string) []string {
	best, score := []string(nil), atLeast
	for _, rel := range files {
		have, n := of(rel), 0
		for n < len(have) && n < len(want) && have[len(have)-1-n] == want[len(want)-1-n] {
			n++
		}
		switch {
		case n > score:
			best, score = []string{rel}, n
		case n == score:
			best = append(best, rel)
		}
	}
	return best
}

func dedupe(files []string) []string {
	seen := map[string]bool{}
	return slices.DeleteFunc(files, func(rel string) bool {
		dup := seen[rel]
		seen[rel] = true
		return dup
	})
}

// holds is what a file was found to declare.
type holds struct{ function, pkg bool }

// locate finds the frame's line in one file, and says whether the file
// declares the function by name and the package pkg.
func (ix *Index) locate(rel string, f Frame, pkg string) (Match, holds) {
	m := Match{Path: rel}
	abs, err := ix.Abs(rel)
	if err != nil {
		return m, holds{}
	}
	file, err := os.Open(abs)
	if err != nil {
		return m, holds{}
	}
	defer file.Close()
	var inPackage *regexp.Regexp
	if pkg != "" {
		inPackage = compile(`^\s*(?:package|namespace|Namespace)\s+` + regexp.QuoteMeta(pkg) + `\s*[;{]?\s*$`)
	}
	pkgSeen := false

	ext := strings.ToLower(path.Ext(rel))
	function, typ := declaration(ext, f), typeDeclaration(ext, f)
	found, typeLine := 0, 0
	scan := bufio.NewScanner(io.LimitReader(file, maxFileBytes))
	scan.Buffer(make([]byte, 64<<10), 1<<20)
	for n := 1; scan.Scan(); n++ {
		line := scan.Bytes()
		if function != nil && function.Match(line) && !callSite(line) {
			if found++; found == 1 {
				m.Line = n
			}
		}
		if typeLine == 0 && typ != nil && typ.Match(line) {
			typeLine = n
		}
		pkgSeen = pkgSeen || (inPackage != nil && inPackage.Match(line))
	}
	switch {
	case found == 1:
		m.How = Declaration
	case found > 1:
		m.How = FirstOfSeveral
	case typeLine > 0:
		m.Line, m.How = typeLine, TypeOnly
	}
	return m, holds{function: found > 0, pkg: pkgSeen}
}

var callWord = regexp.MustCompile(`^\s*(?:return|new|throw|else|case|await|yield|if|while|for|switch)\b`)

// callSite tells a use of a name from its declaration, for the languages
// whose declarations have no keyword of their own.
func callSite(line []byte) bool {
	return callWord.Match(line)
}

// declaration is the pattern of the line that declares the frame's function
// in a file of the extension; nil when the frame has no name to look for.
// The name is quoted: no text of a label is ever a pattern.
func declaration(ext string, f Frame) *regexp.Regexp {
	symbol := f.Symbol()
	if i := strings.LastIndexByte(symbol, '.'); i >= 0 {
		symbol = symbol[i+1:] // a script's Class.method
	}
	if symbol == "" {
		return nil
	}
	s := regexp.QuoteMeta(symbol)
	var pattern string
	switch ext {
	case ".go":
		pattern = `^func\s+(?:\([^)]*\)\s*)?` + s + `\s*[(\[]`
		if f.Type != "" && f.Notation == Go {
			pattern = `^func\s+\(\s*\w*\s*\*?` + regexp.QuoteMeta(f.Type) + `(?:\[[^\]]*\])?\s*\)\s*` + s + `\s*[(\[]`
		}
	case ".py":
		pattern = `^\s*(?:async\s+)?def\s+` + s + `\s*\(`
	case ".rb":
		pattern = `^\s*def\s+(?:self\.)?` + s + `\b`
	case ".php":
		pattern = `\bfunction\s+&?` + s + `\s*\(`
	case ".kt":
		pattern = `\bfun\s+(?:<[^>]+>\s*)?(?:[\w.<>?, ]+\.)?` + s + `\s*[(<]`
		if symbol == f.OuterType() {
			return nil // a constructor: the class line is the place
		}
	case ".scala":
		pattern = `\bdef\s+` + s + `\b`
	case ".fs":
		pattern = `\b(?:let|member)\s+(?:\w+\.)?` + s + `\b`
	case ".vb":
		pattern = `(?i)\b(?:Sub|Function)\s+` + s + `\b`
	case ".js", ".mjs", ".cjs", ".jsx", ".ts", ".mts", ".cts", ".tsx":
		pattern = `\bfunction\s*\*?\s*` + s + `\s*[(<]` +
			`|\b(?:const|let|var)\s+` + s + `\s*[:=]` +
			`|\b` + s + `\s*[:=]\s*(?:async\s*)?(?:function\b|\()` +
			`|^\s*(?:(?:public|private|protected|static|async|get|set|readonly|override)\s+)*` + s + `\s*(?:<[^>]*>)?\s*\([^)]*\)\s*(?::[^{]+)?\{`
	default: // .java, .groovy, .cs: a type or a modifier, then the name
		pattern = `^\s*(?:[\w@][\w.$<>\[\],?@]*\s+)+` + s + `\s*(?:<[^>]*>)?\s*\(`
		if ext == ".groovy" {
			pattern += `|\bdef\s+` + s + `\s*\(`
		}
		if symbol == f.OuterType() {
			pattern = `^\s*(?:(?:public|protected|private|internal)\s+)?` + s + `\s*\(`
		}
	}
	return compile(pattern)
}

// compile is the pattern, or nil for one that does not compile: a name that
// is not valid UTF-8 cannot be quoted into one, and then matches no line.
func compile(pattern string) *regexp.Regexp {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil
	}
	return re
}


// typeDeclaration is the pattern of the line that declares the frame's type.
func typeDeclaration(ext string, f Frame) *regexp.Regexp {
	outer := f.OuterType()
	if outer == "" {
		return nil
	}
	if ext == ".go" {
		if f.Notation != Go {
			return nil // main.main: main is the package
		}
		return compile(`^type\s+` + regexp.QuoteMeta(f.Type) + `\b`)
	}
	return compile(`(?i:\b(?:class|interface|enum|record|object|struct|trait|module|type))\s+` + regexp.QuoteMeta(outer) + `\b`)
}
