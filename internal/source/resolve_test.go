package source

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// tree writes files under a new directory: path to content.
func tree(t testing.TB, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func scan(t testing.TB, root string) *Index {
	t.Helper()
	ix, err := Scan(context.Background(), root, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

// parts are the parts of a lookup's answer most tests look at.
func parts(f Found) (matches []Match, unread int, late bool) {
	return f.Matches, f.Unread, f.Late
}

// shop is a repository with one service per language.
var shop = map[string]string{
	".git/HEAD": "ref: refs/heads/main\n",
	"backend/src/main/java/com/example/shop/CartService.java": `package com.example.shop;

public class CartService {
    private final Prices prices;

    public CartService(Prices prices) {
        this.prices = prices;
    }

    public Cart addItem(String sku, int quantity) {
        return addItem(sku, quantity, false);
    }

    Cart addItem(String sku, int quantity, boolean gift) {
        items.forEach(i -> total(i));
        return new Cart();
    }

    @Override
    public String toString() { return total(null); }

    private String total(Item item) {
        return "";
    }

    static class Line {
        long total() { return 0; }
    }
}
// The class CartService is the first of its name here.
`,
	"backend/src/test/java/com/example/shop/CartServiceTest.java": "package com.example.shop;\nclass CartServiceTest {}\n",
	"backend/src/main/java/com/example/other/CartService.java":    "package com.example.other;\nclass CartService {\n  void addItem() {}\n}\n",
	"backend/src/main/kotlin/Pricing.kt":                          "package com.example.shop.pricing\n\nclass Pricing(val rate: Int) {\n    fun <T> priceOf(item: T): Int = rate\n    suspend fun String.discount(n: Int) = n\n}\n\nfun topLevel() = 1\n",
	"backend/src/main/scala/com/example/shop/Ranker.scala":        "package com.example.shop\n\nobject Ranker {\n  def score(x: Int): Int = x\n}\n",
	"backend/src/main/groovy/com/example/shop/Rules.groovy":       "package com.example.shop\n\nclass Rules {\n  def apply(order) { order }\n}\n",
	"api/internal/cart/service.go": `package cart

type Service struct{}

func (s *Service) AddItem(sku string) error { return nil }

func (s Service) Total() int { return 0 }

func Total() int { return 0 }

func Map[K comparable](k K) {}

func (s Service) Sum() int { return 0 }
`,
	"api/internal/cart/service_test.go": "package cart\n\nfunc Total() int { return 1 }\n",
	"api/internal/order/service.go":     "package order\n\ntype Service struct{}\n\nfunc (s *Service) AddItem() {}\n",
	"api/cmd/api/main.go":               "package main\n\nfunc main() {\n\trun()\n}\n\nfunc run() {}\n",
	// Names a library and the runtime have too.
	"api/internal/lock/lock.go":                   "package lock // of the cart\n\ntype Mutex struct{}\n\nfunc (m *Mutex) Lock() {}\n\nfunc NewServer() {}\n\nfunc Open() {}\n",
	"tool.go":                                     "package tool\n\nfunc Run() {}\n",
	"jobs/shop/reports/daily.py":                  "import os\n\nclass Report:\n    def render(self):\n        return render(self)\n\nasync def render(report):\n    pass\n",
	"web/src/server/handler.ts":                   "export async function resolve(req: Request) {\n  return other(req)\n}\n\nexport const other = (req) => 1\n\nclass Handler {\n  handle(req) {\n    return 1\n  }\n}\n",
	"web/src/client/handler.ts":                   "export function resolve() {}\n",
	"web/node_modules/graphql/type/definition.js": "function resolve() {}\n",
	"web/dist/server/handler.js":                  "function resolve() {}\n",
	"dotnet/Shop/Services/CartService.cs":         "namespace Shop.Services;\n\npublic class CartService\n{\n    public async Task<Cart> AddItem(string sku)\n    {\n        return await AddItem(sku);\n    }\n}\n",
	"dotnet/Legacy/CartService.cs":                "namespace Legacy\n{\n    public class CartService { }\n}\n",
	".idea/workspace/Hidden.java":                 "class Hidden {}\n",
	"vendor/github.com/lib/pq/conn.go":            "package pq\n\nfunc Open() {}\n",
	"README.md":                                   "# shop\n",
	// A copy of a runtime's source is still not the application's.
	"third/java/lang/Thread.java": "package java.lang;\n\nclass Thread {\n  void run() {}\n}\n",
}

func TestResolve(t *testing.T) {
	ix := scan(t, tree(t, shop))
	java := "backend/src/main/java/com/example/shop/CartService.java"
	for _, tc := range []struct {
		label string
		want  []Match
	}{
		// The JVM.
		{"com.example.shop.CartService.toString", []Match{{java, 20, Declaration}}},
		{"com.example.shop.CartService.addItem", []Match{{java, 10, FirstOfSeveral}}},
		{"com.example.shop.CartService.total", []Match{{java, 22, FirstOfSeveral}}},
		{"com.example.shop.CartService.<init>", []Match{{java, 6, Declaration}}},
		{"com.example.shop.CartService.lambda$addItem$0", []Match{{java, 10, FirstOfSeveral}}},
		{"com.example.shop.CartService$Line.total", []Match{{java, 22, FirstOfSeveral}}},
		{"com.example.shop.CartService$$Lambda$14.run", []Match{{java, 3, TypeOnly}}},
		{"com.example.shop.CartService.<clinit>", []Match{{java, 3, TypeOnly}}},
		{"com.example.shop.CartService.generatedAccessor", []Match{{java, 3, TypeOnly}}},
		{"com.example.other.CartService.addItem", []Match{{"backend/src/main/java/com/example/other/CartService.java", 3, Declaration}}},
		{"com.example.shop.pricing.Pricing.priceOf", []Match{{"backend/src/main/kotlin/Pricing.kt", 4, Declaration}}},
		{"com.example.shop.pricing.Pricing.discount", []Match{{"backend/src/main/kotlin/Pricing.kt", 5, Declaration}}},
		{"com.example.shop.pricing.Pricing.<init>", []Match{{"backend/src/main/kotlin/Pricing.kt", 3, TypeOnly}}},
		{"com.example.shop.pricing.PricingKt.topLevel", []Match{{"backend/src/main/kotlin/Pricing.kt", 8, Declaration}}},
		{"com.example.shop.Ranker$.score", []Match{{"backend/src/main/scala/com/example/shop/Ranker.scala", 4, Declaration}}},
		{"com.example.shop.Rules.apply", []Match{{"backend/src/main/groovy/com/example/shop/Rules.groovy", 4, Declaration}}},
		// A class of another package, a class the repository does not have,
		// and a runtime's: no file.
		{"com.acme.shop.CartService.addItem", nil},
		{"org.springframework.web.servlet.DispatcherServlet.doDispatch", nil},
		{"java.lang.Thread.run", nil},

		// Go.
		{"github.com/acme/shop/api/internal/cart.(*Service).AddItem", []Match{{"api/internal/cart/service.go", 5, Declaration}}},
		{"github.com/acme/shop/api/internal/cart.Service.Total", []Match{{"api/internal/cart/service.go", 7, Declaration}}},
		// A function is not the method of the same name, nor the other way round.
		{"github.com/acme/shop/api/internal/cart.Total", []Match{{"api/internal/cart/service.go", 9, Declaration}}},
		{"github.com/acme/shop/api/internal/cart.Map[go.shape.string]", []Match{{"api/internal/cart/service.go", 11, Declaration}}},
		{"github.com/acme/shop/api/internal/cart.Total.func1", []Match{{"api/internal/cart/service.go", 9, Declaration}}},
		{"github.com/acme/shop/api/internal/cart.Sum", nil},
		{"github.com/acme/shop/api/internal/cart.Service.Sum", []Match{{"api/internal/cart/service.go", 13, Declaration}}},
		{"cart.Sum", nil},
		{"cart.Total", []Match{{"api/internal/cart/service.go", 9, Declaration}}},
		{"github.com/acme/shop/api/internal/order.(*Service).AddItem", []Match{{"api/internal/order/service.go", 5, Declaration}}},
		{"github.com/acme/shop/api/internal/cart.(*Service).Missing", nil},
		{"main.main", []Match{{"api/cmd/api/main.go", 3, Declaration}}},
		{"main.run", []Match{{"api/cmd/api/main.go", 7, Declaration}}},
		{"github.com/lib/pq.Open", nil},
		{"net/http.(*conn).serve", nil},
		// A module named without a domain is not the standard library.
		{"shop/api/internal/cart.(*Service).AddItem", []Match{{"api/internal/cart/service.go", 5, Declaration}}},
		{"github.com/acme/shop/api/internal/lock.(*Mutex).Lock", []Match{{"api/internal/lock/lock.go", 5, Declaration}}},
		// The module's root, also behind a major version.
		{"example.com/tool.Run", []Match{{"tool.go", 3, Declaration}}},
		{"example.com/tool/v2.Run", []Match{{"tool.go", 3, Declaration}}},
		// The runtime's and a library's functions are not the application's
		// of the same name: the file must be of the frame's package.
		{"runtime.main", nil},
		{"runtime.gopark", nil},
		{"sync.(*Mutex).Lock", nil},
		{"google.golang.org/grpc.NewServer", nil},
		{"database/sql.Open", nil},

		// Python.
		{"shop.reports.daily.render", []Match{{"jobs/shop/reports/daily.py", 4, FirstOfSeveral}}},
		{"shop.reports.daily.Report.render", []Match{{"jobs/shop/reports/daily.py", 4, FirstOfSeveral}}},
		{"jobs/shop/reports/daily.py:render", []Match{{"jobs/shop/reports/daily.py", 4, FirstOfSeveral}}},

		// Scripts carry a path: the longest tail of it that a file has.
		{"/app/src/server/handler.ts:resolve", []Match{{"web/src/server/handler.ts", 1, Declaration}}},
		{"src/server/handler.ts:other", []Match{{"web/src/server/handler.ts", 5, Declaration}}},
		{"src/server/handler.ts:Handler.handle", []Match{{"web/src/server/handler.ts", 8, Declaration}}},
		{"src/server/handler.ts:(anonymous)", []Match{{"web/src/server/handler.ts", 0, FileOnly}}},
		{`C:\app\src\server\handler.ts:resolve`, []Match{{"web/src/server/handler.ts", 1, Declaration}}},
		// The file that declares the function before the one that only has the name.
		{"handler.ts:other", []Match{{"web/src/server/handler.ts", 5, Declaration}, {"web/src/client/handler.ts", 0, FileOnly}}},
		{"handler.ts:resolve", []Match{{"web/src/client/handler.ts", 1, Declaration}, {"web/src/server/handler.ts", 1, Declaration}}},
		{"graphql/type/definition.js:resolve", nil},
		// A dependency's file of the same name as one of the application's.
		{"/app/node_modules/express/lib/router/handler.ts:resolve", nil},
		{"node:internal/timers:processTimers", nil},

		// .NET: a file named after the type, the namespace's directory first.
		{"Shop.Services.CartService.AddItem", []Match{{"dotnet/Shop/Services/CartService.cs", 5, Declaration}}},
		{"Legacy.CartService.Missing", []Match{{"dotnet/Legacy/CartService.cs", 3, TypeOnly}}},
		{"Legacy.CartService..ctor", []Match{{"dotnet/Legacy/CartService.cs", 3, TypeOnly}}},
		{"System.Threading.Tasks.Task.Run", nil},

		{"", nil}, {"main", nil}, {"...", nil},
	} {
		got, unread, late := parts(ix.Resolve(context.Background(), Parse(tc.label)))
		if !reflect.DeepEqual(got, tc.want) || unread != 0 || late {
			t.Errorf("%s\n got  %+v (%d unread)\n want %+v", tc.label, got, unread, tc.want)
		}
		if likely := ix.Likely(Parse(tc.label)); !likely && len(tc.want) > 0 {
			t.Errorf("%s: resolves, but Likely says no", tc.label)
		}
	}
}

// A file whose name is all that agrees with a frame is the frame's only when
// it declares the frame's function or type. dtctl cuts a Node frame's path
// at node_modules, so a dependency's file reads like one of the checkout
// (graphql/lib/index.js), and a dotted name with no directory to agree on
// fits any script of that name.
func TestResolveNeedsMoreThanAFileName(t *testing.T) {
	ix := scan(t, tree(t, map[string]string{
		"src/index.js":     "function start() {}\n",
		"scripts/utils.py": "def tidy():\n    pass\n",
		"server.js":        "function handle() {}\n",
	}))
	for _, tc := range []struct {
		label string
		want  []Match
	}{
		// A dependency's file and a library's module: the name alone.
		{"graphql/lib/index.js:execute", nil},
		{"graphql/lib/index.js:(anonymous)", nil},
		{"utils.Helper.run", nil},
		{"utils.run", nil},
		// The same names with a declaration in the file.
		{"utils.tidy", []Match{{"scripts/utils.py", 1, Declaration}}},
		{"/app/server.js:handle", []Match{{"server.js", 1, Declaration}}},
		// The cost: a function without a name, in a file of which only the
		// name agrees, is not found any more.
		{"/app/server.js:(anonymous)", nil},
		// A label that names the file and nothing else disagrees with
		// nothing, and one whose directory agrees is told by its place.
		{"index.js:(anonymous)", []Match{{"src/index.js", 0, FileOnly}}},
		{"/app/src/index.js:(anonymous)", []Match{{"src/index.js", 0, FileOnly}}},
		// What is left open until the analysis says which frames are the
		// application's (docs/dev/tech-debt.md): a dependency's function
		// named like one the file declares.
		{"graphql/lib/index.js:start", []Match{{"src/index.js", 1, Declaration}}},
	} {
		got, unread, late := parts(ix.Resolve(context.Background(), Parse(tc.label)))
		if !reflect.DeepEqual(got, tc.want) || unread != 0 || late {
			t.Errorf("%s\n got  %+v (%d unread)\n want %+v", tc.label, got, unread, tc.want)
		}
	}
}

// Text of a label never becomes a path or a pattern: whatever it holds, a
// match is a file the walk saw inside the root, or there is none.
func TestResolveIsConfinedToTheRoot(t *testing.T) {
	outside := tree(t, map[string]string{"Secret.java": "class Secret { void run() {} }\n", "passwd.py": "def root(): pass\n"})
	root := tree(t, shop)
	// Links that leave the root: to a file, and to a directory.
	if err := os.Symlink(filepath.Join(outside, "Secret.java"), filepath.Join(root, "Secret.java")); err != nil {
		t.Skip("no symbolic links here:", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	ix := scan(t, root)
	for _, label := range []string{
		"Secret.run",
		"linked.Secret.run",
		"../Secret.run",
		"..Secret.run",
		"../../../../etc/passwd.py:root",
		outside + "/passwd.py:root",
		"/etc/passwd.py:root",
		"linked/passwd.py:root",
		"..\\..\\passwd.py:root",
		"a/../../" + filepath.Base(outside) + "/passwd.py:root",
		"com.example.shop.CartService.java:../../x",
		"$(touch pwned).Type.run",
		"`id`.Type.run; rm -rf /",
		"a b.Type|run&x",
		"%2e%2e/%2e%2e/passwd.py:root",
		"file:///etc/passwd.py:root",
		"vscode://file/etc/passwd.py:root",
		"com.example.shop.CartService.\x00addItem",
		"com.example.shop.CartService.add\x1b[31mItem",
		"com.example.shop.CartService.(?s).*",
		"com.example.shop.CartService.[",
		"com.example.shop.CartService.add\xffItem",
		"github.com/acme/shop/api/internal/cart.(*Ser\xffvice).AddItem",
		"Legacy.Cart\xffService.Missing",
		"Legacy\xff.CartService.Missing",
		strings.Repeat("a.", 5000) + "B.c",
		strings.Repeat("../", 2000) + "passwd.py:root",
	} {
		matches, _, _ := parts(ix.Resolve(context.Background(), Parse(label)))
		for _, m := range matches {
			abs, err := ix.Abs(m.Path)
			if err != nil || !strings.HasPrefix(abs, ix.Root()+string(filepath.Separator)) || strings.Contains(abs, "Secret") || strings.Contains(abs, "passwd") {
				t.Errorf("%q matched %q (%q, %v)", label, m.Path, abs, err)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(root, "pwned")); err == nil {
		t.Error("a label was run")
	}
	// Abs hands out nothing the walk did not see.
	for _, rel := range []string{"../" + filepath.Base(outside) + "/Secret.java", "Secret.java", "linked/Secret.java", "/etc/hosts", "", ".", "README.md", "api/../README.md"} {
		if abs, err := ix.Abs(rel); err == nil {
			t.Errorf("Abs(%q) = %q", rel, abs)
		}
	}
	// A file that became a link out of the root after the walk is refused.
	victim := filepath.Join(root, "api/cmd/api/main.go")
	if err := os.Remove(victim); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "passwd.py"), victim); err != nil {
		t.Fatal(err)
	}
	if abs, err := ix.Abs("api/cmd/api/main.go"); err == nil {
		t.Errorf("a file that now links outside the root is handed out as %q", abs)
	}
	if got, _, _ := parts(ix.Resolve(context.Background(), Parse("main.main"))); len(got) != 0 {
		t.Errorf("a file that now links outside the root is matched: %+v", got)
	}
}

// Only so many files are read for one frame. When more than that could hold
// it, the answer says how many were left unread: one match among them is
// then not known to be the only one.
func TestResolveSaysWhatItDidNotRead(t *testing.T) {
	files := map[string]string{}
	for i := range maxCandidates + 50 {
		files[fmt.Sprintf("cmd/tool%03d/run.go", i)] = "package main\n\nfunc run() {}\n"
	}
	files["cmd/tool007/run.go"] = "package main\n\nfunc main() {}\n"
	files[fmt.Sprintf("cmd/tool%03d/run.go", maxCandidates+20)] = "package main\n\nfunc main() {}\n"
	ix := scan(t, tree(t, files))
	got, unread, late := parts(ix.Resolve(context.Background(), Parse("main.main")))
	if len(got) != 1 || got[0].Path != "cmd/tool007/run.go" || unread != 50 || late {
		t.Errorf("got %+v, %d unread; want the one main among the files read and 50 unread", got, unread)
	}
	if got, unread, _ := parts(ix.Resolve(context.Background(), Parse("main.absent"))); len(got) != 0 || unread != 50 {
		t.Errorf("a miss: %+v, %d unread", got, unread)
	}
}

// Only so many matches are offered. The answer says how many more there
// were, so that the last one offered is not taken for the last there is.
func TestResolveSaysHowManyMatchesItDoesNotOffer(t *testing.T) {
	files := map[string]string{}
	for i := range maxMatches + 3 {
		files[fmt.Sprintf("cmd/tool%02d/main.go", i)] = "package main\n\nfunc main() {}\n"
	}
	ix := scan(t, tree(t, files))
	found := ix.Resolve(context.Background(), Parse("main.main"))
	if len(found.Matches) != maxMatches || found.More != 3 || found.Unread != 0 || found.Late {
		t.Errorf("%d matches, %d more, %d unread, late %v; want %d and 3 more", len(found.Matches), found.More, found.Unread, found.Late, maxMatches)
	}
	delete(files, "cmd/tool00/main.go")
	delete(files, "cmd/tool01/main.go")
	delete(files, "cmd/tool02/main.go")
	if found := scan(t, tree(t, files)).Resolve(context.Background(), Parse("main.main")); len(found.Matches) != maxMatches || found.More != 0 {
		t.Errorf("as many as are offered: %d matches, %d more", len(found.Matches), found.More)
	}
}

// Reading the files for one frame is bounded in time too: a slow disk does
// not keep a lookup going. What the time did not reach is counted as unread,
// and the answer says that it was the time that ran out.
func TestResolveIsBoundedInTime(t *testing.T) {
	if maxResolveTime > 5*time.Second {
		t.Errorf("a lookup may take %v", maxResolveTime)
	}
	ix := scan(t, tree(t, map[string]string{
		"cmd/a/main.go": "package main\n\nfunc main() {}\n",
		"cmd/b/main.go": "package main\n\nfunc main() {}\n",
		"cmd/c/run.go":  "package main\n\nfunc run() {}\n",
	}))
	over, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	got, unread, late := parts(ix.Resolve(over, Parse("main.main")))
	if len(got) != 0 || unread != 3 || !late {
		t.Errorf("out of time: %+v, %d unread, late %v; want nothing, 3 unread, late", got, unread, late)
	}
	if got, unread, late := parts(ix.Resolve(context.Background(), Parse("main.main"))); len(got) != 2 || unread != 0 || late {
		t.Errorf("in time: %+v, %d unread, late %v", got, unread, late)
	}
}

func TestScanSkipsWhatIsNotSource(t *testing.T) {
	ix := scan(t, tree(t, shop))
	for rel, want := range map[string]bool{
		"api/cmd/api/main.go": true, "web/src/server/handler.ts": true, "api/internal/cart/service_test.go": true,
		"web/node_modules/graphql/type/definition.js": false, "web/dist/server/handler.js": false,
		".idea/workspace/Hidden.java": false, "vendor/github.com/lib/pq/conn.go": false, "README.md": false, ".git/HEAD": false,
	} {
		if _, err := ix.Abs(rel); (err == nil) != want {
			t.Errorf("%s in the index: %v, want %v", rel, err == nil, want)
		}
	}
	if ix.Partial != "" || ix.Len() != 18 {
		t.Errorf("%d files, partial %q", ix.Len(), ix.Partial)
	}
}

func TestScanIsBounded(t *testing.T) {
	files := map[string]string{}
	for i := range 40 {
		files[fmt.Sprintf("d%d/F%d.java", i%4, i)] = "class F {}\n"
	}
	root := tree(t, files)
	for _, tc := range []struct {
		name    string
		lim     Limits
		partial string
		atMost  int
	}{
		{"all of it", DefaultLimits, "", 40},
		{"files", Limits{Entries: 1000, Files: 7, Time: time.Minute}, "more than 7 source files", 7},
		{"entries", Limits{Entries: 10, Files: 1000, Time: time.Minute}, "more than 10 files and directories", 10},
	} {
		ix, err := Scan(context.Background(), root, tc.lim)
		if err != nil || ix.Partial != tc.partial || ix.Len() > tc.atMost || (tc.partial == "" && ix.Len() != tc.atMost) {
			t.Errorf("%s: %d files, partial %q, err %v", tc.name, ix.Len(), ix.Partial, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if ix, err := Scan(ctx, root, DefaultLimits); err != nil || ix.Partial != "the search was interrupted" || ix.Len() != 0 {
		t.Errorf("an interrupted walk: %+v, %v", ix, err)
	}
	if _, err := Scan(context.Background(), filepath.Join(root, "missing"), DefaultLimits); err == nil {
		t.Error("a directory that is not there was walked")
	}
}

func TestRoot(t *testing.T) {
	root := tree(t, map[string]string{".git/HEAD": "x", "a/b/c/f.go": "package c\n", "a/sub/.git": "gitdir: elsewhere\n", "a/sub/x/f.go": ""})
	plain := tree(t, map[string]string{"a/f.go": ""})
	for dir, want := range map[string]string{
		filepath.Join(root, "a/b/c"):   root,
		root:                           root,
		filepath.Join(root, "a/sub/x"): filepath.Join(root, "a/sub"),
	} {
		if got := Root(dir); got != want {
			t.Errorf("Root(%s) = %s, want %s", dir, got, want)
		}
	}
	// Without a repository at or above it there is no directory to look
	// in: a home directory is not walked for being where dtui was started.
	// (Unless a parent of the test's directory happens to be a repository.)
	if got := Root(filepath.Join(plain, "a")); got != "" && !strings.HasPrefix(plain, got) {
		t.Errorf("Root outside a repository = %q, want none", got)
	}
}
