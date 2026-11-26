package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// exceptionKeySources are the files the key algorithm lives in.
const exceptionKeySources = "exc*.go"

// keyInput is one package-level var or const declaration of the key
// algorithm: a table, an expression, a limit.
type keyInput struct {
	name, digest string
}

// TestExceptionKeyInputsArePinned pins everything the key algorithm is
// configured by: every package-level var and const of its files — the
// vendor and pass-through tables, the expressions, the message rules and
// their order, the limits. The corpus pins keys, but only for the inputs it
// happens to hold; a prefix added to a table or a quantifier widened in an
// expression moves keys it has no case for. So a declaration whose tokens
// change fails here, whatever the corpus says. Comments and layout are not
// part of a digest.
//
// The fix is the one TestExceptionKeysAreStable asks for: raise
// ExceptionKeyVersion, which starts a new golden file. -update only adds
// declarations that are new.
func TestExceptionKeyInputsArePinned(t *testing.T) {
	golden := filepath.Join(exceptionCorpusDir, ExceptionKeyVersion+"-inputs.golden")
	pinned := map[string]string{}
	raw, err := os.ReadFile(golden)
	if err != nil && !*updateExceptionKeys {
		t.Fatalf("%v (new key version? run with -update to create it)", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if digest, name, ok := strings.Cut(line, "\t"); ok {
			pinned[name] = digest
		}
	}

	var lines []string
	for _, in := range exceptionKeyInputs(t) {
		want, ok := pinned[in.name]
		switch {
		case ok && want != in.digest:
			t.Errorf("%s changed — it is part of the %s key algorithm; raise ExceptionKeyVersion instead of repinning",
				in.name, ExceptionKeyVersion)
		case !ok && !*updateExceptionKeys:
			t.Errorf("%s is not pinned; if no %s key depends on it yet, run with -update to add it", in.name, ExceptionKeyVersion)
		}
		delete(pinned, in.name)
		lines = append(lines, in.digest+"\t"+in.name)
	}
	for name := range pinned {
		t.Errorf("%s is pinned but no longer declared — it was part of the %s key algorithm; raise ExceptionKeyVersion",
			name, ExceptionKeyVersion)
	}
	if *updateExceptionKeys && !t.Failed() {
		if err := os.WriteFile(golden, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// The pin above covers package-level declarations only, so nothing the
// algorithm is configured by may be built where it cannot see: no expression
// compiled, no replacer built and no table of literals written inside a
// function. A table is a slice, array or map literal that holds a literal.
func TestExceptionKeyInputsArePackageLevel(t *testing.T) {
	eachExceptionKeySource(t, func(fset *token.FileSet, file *ast.File, _ []byte) {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.SelectorExpr:
					if pkg, ok := n.X.(*ast.Ident); ok && (pkg.Name == "regexp" || pkg.Name == "strings" && n.Sel.Name == "NewReplacer") {
						t.Errorf("%s: %s.%s in %s; declare it at package level", fset.Position(n.Pos()), pkg.Name, n.Sel.Name, fn.Name.Name)
					}
				case *ast.CompositeLit:
					if !isTableType(n.Type) {
						break
					}
					for _, elt := range n.Elts {
						if kv, ok := elt.(*ast.KeyValueExpr); ok {
							elt = kv.Value
						}
						if _, ok := elt.(*ast.BasicLit); ok {
							t.Errorf("%s: a table of literals in %s; declare it at package level", fset.Position(n.Pos()), fn.Name.Name)
							break
						}
					}
				}
				return true
			})
		}
	})
}

func isTableType(e ast.Expr) bool {
	switch e.(type) {
	case *ast.ArrayType, *ast.MapType:
		return true
	}
	return false
}

// A declaration the pin does not see would be an input it does not cover.
func TestExceptionKeyInputsCoverTheTables(t *testing.T) {
	names := map[string]bool{}
	for _, in := range exceptionKeyInputs(t) {
		for _, name := range strings.Fields(strings.NewReplacer("const", "", "var", "", ",", " ").Replace(in.name)) {
			names[name] = true
		}
	}
	for _, name := range []string{
		"ExceptionKeyVersion", "FingerprintFrames", "fingerprintKeyLen", "RungAppFrames", "customClassifierField",
		"messageRules", "msgNumberRe", "exceptionMessageInput", "ExceptionMessageMax",
		"stackNotations", "StackJava", "deployRoots", "sourceReleaseRe",
		"javaVendorPrefixes", "javaPassThrough", "javaLocationRe", "dotnetVendorPrefixes", "dotnetPassThrough",
		"jsPassThrough", "jsDependencyDir", "pythonPackageDirs", "pythonPassThrough",
		"goStdlib", "goVendorPrefixes", "goPassThrough", "goDependencyRe",
	} {
		if !names[name] {
			t.Errorf("%s is not among the pinned inputs", name)
		}
	}
}

// exceptionKeyInputs digests every package-level var and const declaration
// of the key algorithm's files, sorted by name. A const block is one input,
// since iota makes its values depend on their order.
func exceptionKeyInputs(t *testing.T) []keyInput {
	t.Helper()
	var out []keyInput
	eachExceptionKeySource(t, func(fset *token.FileSet, file *ast.File, src []byte) {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR && gen.Tok != token.CONST {
				continue
			}
			var block []string
			for _, spec := range gen.Specs {
				spec := spec.(*ast.ValueSpec)
				var names []string
				for _, name := range spec.Names {
					names = append(names, name.Name)
				}
				if gen.Tok == token.CONST {
					block = append(block, names...)
					continue
				}
				out = append(out, keyInput{"var " + strings.Join(names, ", "),
					tokenDigest(t, src[fset.Position(spec.Pos()).Offset:fset.Position(spec.End()).Offset])})
			}
			if gen.Tok == token.CONST {
				out = append(out, keyInput{"const " + strings.Join(block, ", "),
					tokenDigest(t, src[fset.Position(gen.Pos()).Offset:fset.Position(gen.End()).Offset])})
			}
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func eachExceptionKeySource(t *testing.T, visit func(*token.FileSet, *ast.File, []byte)) {
	t.Helper()
	paths, err := filepath.Glob(exceptionKeySources)
	if err != nil || len(paths) == 0 {
		t.Fatalf("no source matches %s: %v", exceptionKeySources, err)
	}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		visit(fset, file, src)
	}
}

// tokenDigest hashes the tokens of a piece of source: comments, layout, and
// the comma and line end a wrapped list closes with are left out, a string
// is taken as it is written.
func tokenDigest(t *testing.T, src []byte) string {
	t.Helper()
	fset := token.NewFileSet()
	var s scanner.Scanner
	s.Init(fset.AddFile("", fset.Base(), len(src)), src, func(pos token.Position, msg string) {
		t.Fatalf("%s: %s", pos, msg)
	}, 0)
	h := sha256.New()
	comma := false
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.SEMICOLON && lit == "\n" {
			continue
		}
		closing := tok == token.RBRACE || tok == token.RPAREN
		if comma && !closing {
			h.Write([]byte(",\x00"))
		}
		if comma = tok == token.COMMA; comma {
			continue
		}
		if lit == "" {
			lit = tok.String()
		}
		h.Write([]byte(lit + "\x00"))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func TestTokenDigestIgnoresLayoutOnly(t *testing.T) {
	same := [][2]string{
		{`x = []string{"a", "b"}`, "x = []string{\n\t\"a\", // first\n\t\"b\",\n}"},
		{`x = 5`, "x   =   5 // five"},
	}
	for _, pair := range same {
		if tokenDigest(t, []byte(pair[0])) != tokenDigest(t, []byte(pair[1])) {
			t.Errorf("%q and %q differ", pair[0], pair[1])
		}
	}
	differ := [][2]string{
		{`x = []string{"a", "b"}`, `x = []string{"a", "b", "c"}`},
		{`x = []string{"a", "b"}`, `x = []string{"b", "a"}`},
		{`x = []string{"a", "b"}`, `x = []string{"ab"}`},
		{"x = `a{1,7}`", "x = `a{1,8}`"},
		{"x = `a b`", "x = `a  b`"},
		{`x = 200`, `x = 201`},
		{`x = f(a, b)`, `x = f(a)(b)`},
	}
	for _, pair := range differ {
		if tokenDigest(t, []byte(pair[0])) == tokenDigest(t, []byte(pair[1])) {
			t.Errorf("%q and %q share a digest", pair[0], pair[1])
		}
	}
}
