package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var updateExceptionKeys = flag.Bool("update", false,
	"add the keys of new corpus cases to testdata/exceptions/<version>-keys.golden")

const cartStack = `java.lang.IllegalStateException: Cart 7 is locked
	at com.acme.shop.CartService.addItem(CartService.java:42)
	at org.springframework.web.Dispatcher.dispatch(Dispatcher.java:9)
	at com.acme.shop.CartController.post(CartController.java:17)`

func mustFingerprint(t *testing.T, excs ...SpanException) ExceptionFingerprint {
	t.Helper()
	fp, ok := FingerprintExceptions(excs, nil)
	if !ok {
		t.Fatal("no fingerprint")
	}
	return fp
}

func TestFingerprintExceptionsLadder(t *testing.T) {
	const typ = "java.lang.IllegalStateException"
	runtimeStack := "\tat java.base/java.util.ArrayList.get(ArrayList.java:427)\n\tat java.base/java.lang.Thread.run(Thread.java:833)"

	t.Run("in-app frames", func(t *testing.T) {
		fp := mustFingerprint(t, SpanException{Type: " " + typ + "\n", Message: "Cart 7 is locked", StackTrace: cartStack})
		want := frames(StackJava, "com.acme.shop.CartService", "addItem", "com.acme.shop.CartController", "post")
		if fp.Rung != RungAppFrames || !reflect.DeepEqual(fp.Frames, want) {
			t.Errorf("rung %v frames %+v", fp.Rung, fp.Frames)
		}
		if fp.Type != typ || fp.Message != "Cart <n> is locked" {
			t.Errorf("type %q message %q", fp.Type, fp.Message)
		}
	})
	t.Run("all frames when none is in-app", func(t *testing.T) {
		fp := mustFingerprint(t, SpanException{Type: typ, Message: "Cart 7 is locked", StackTrace: runtimeStack})
		want := frames(StackJava, "java.util.ArrayList", "get", "java.lang.Thread", "run")
		if fp.Rung != RungFrames || !reflect.DeepEqual(fp.Frames, want) {
			t.Errorf("rung %v frames %+v", fp.Rung, fp.Frames)
		}
	})
	t.Run("message when no frame parses", func(t *testing.T) {
		fp := mustFingerprint(t, SpanException{Type: typ, Message: "Cart 7 is locked", StackTrace: "no frames here"})
		if fp.Rung != RungMessage || fp.Frames != nil || fp.Message != "Cart <n> is locked" {
			t.Errorf("rung %v frames %+v message %q", fp.Rung, fp.Frames, fp.Message)
		}
	})
	t.Run("type alone", func(t *testing.T) {
		fp := mustFingerprint(t, SpanException{Type: typ, Message: " \n "})
		if fp.Rung != RungType || fp.Frames != nil || fp.Message != "" {
			t.Errorf("rung %v frames %+v message %q", fp.Rung, fp.Frames, fp.Message)
		}
	})
	t.Run("each rung keys differently", func(t *testing.T) {
		keys := map[string]string{}
		for name, ex := range map[string]SpanException{
			"app frames": {Type: typ, Message: "Cart 7 is locked", StackTrace: cartStack},
			"frames":     {Type: typ, Message: "Cart 7 is locked", StackTrace: runtimeStack},
			"message":    {Type: typ, Message: "Cart 7 is locked"},
			"type":       {Type: typ},
			"empty":      {},
		} {
			key := mustFingerprint(t, ex).Key
			if other, dup := keys[key]; dup {
				t.Errorf("%s and %s share key %s", name, other, key)
			}
			keys[key] = name
		}
	})
	t.Run("no events", func(t *testing.T) {
		if fp, ok := FingerprintExceptions(nil, nil); ok || !reflect.DeepEqual(fp, ExceptionFingerprint{}) {
			t.Errorf("got %+v, %v", fp, ok)
		}
	})
}

func TestFingerprintKeyShape(t *testing.T) {
	fp := mustFingerprint(t, SpanException{Type: "java.lang.IllegalStateException", StackTrace: cartStack})
	if !regexp.MustCompile(`^e1:[0-9a-f]{12}$`).MatchString(fp.Key) {
		t.Errorf("key = %q, want e1: and 12 hex characters", fp.Key)
	}
	// The hash input: rung number, classifier (none for the built-in one),
	// type, then module and function per frame, each field behind its length.
	sum := sha256.Sum256([]byte("1:1" + "0:" + "31:java.lang.IllegalStateException" +
		"25:com.acme.shop.CartService" + "7:addItem" + "28:com.acme.shop.CartController" + "4:post"))
	if want := "e1:" + hex.EncodeToString(sum[:])[:12]; fp.Key != want {
		t.Errorf("key = %s, want %s", fp.Key, want)
	}
	// Evidence is positional and ordered.
	a := fingerprintKey(RungMessage, "", "a", []string{"b"})
	for name, other := range map[string]string{
		"type and message swapped": fingerprintKey(RungMessage, "", "b", []string{"a"}),
		"another rung":             fingerprintKey(RungType, "", "a", []string{"b"}),
		"evidence split in two":    fingerprintKey(RungMessage, "", "a", []string{"", "b"}),
		"more evidence":            fingerprintKey(RungMessage, "", "a", []string{"b", "c"}),
	} {
		if other == a {
			t.Errorf("%s: same key %s", name, a)
		}
	}
	if fingerprintKey(RungFrames, "", "a", []string{"b", "c"}) == fingerprintKey(RungFrames, "", "a", []string{"c", "b"}) {
		t.Error("frame order must matter")
	}
}

// No two different inputs may write the same canonical string: a separator
// that can also occur inside a field lets one field run into the next.
func TestFingerprintKeyFieldsCannotRunTogether(t *testing.T) {
	a := mustFingerprint(t, SpanException{Type: "T\x00a.b\x1fc", StackTrace: javaStack("d.e.f")})
	b := mustFingerprint(t, SpanException{Type: "T", StackTrace: javaStack("a.b.c", "d.e.f")})
	if a.Rung != RungAppFrames || b.Rung != RungAppFrames || len(a.Frames) != 1 || len(b.Frames) != 2 {
		t.Fatalf("frames %+v and %+v", a.Frames, b.Frames)
	}
	if a.Key == b.Key {
		t.Errorf("a type that spells out a frame shares key %s with the frame", a.Key)
	}
	keys := map[string][]string{}
	for _, fields := range [][]string{
		{"ab", "c"}, {"a", "bc"}, {"abc"}, {"abc", ""}, {"", "abc"}, {"a", "b", "c"},
		{"1:a"}, {"a", "1:b"}, {"a1:b"}, {"a\x00b"}, {"a", "\x00b"}, {"2:ab", "c"}, {"12:ab"},
	} {
		key := fingerprintKey(RungFrames, "", "T", fields)
		if other, dup := keys[key]; dup {
			t.Errorf("%q and %q share key %s", fields, other, key)
		}
		keys[key] = fields
	}
	if fingerprintKey(RungFrames, "", "T1", []string{"a"}) == fingerprintKey(RungFrames, "", "T", []string{"1a"}) {
		t.Error("the type runs into the evidence")
	}
}

func TestFingerprintRungLabels(t *testing.T) {
	want := map[FingerprintRung]string{
		RungAppFrames: "app frames", RungFrames: "frames", RungMessage: "message", RungType: "type", 0: "",
	}
	for rung, label := range want {
		if got := rung.String(); got != label {
			t.Errorf("rung %d = %q, want %q", int(rung), got, label)
		}
	}
	if RungAppFrames != 1 || RungFrames != 2 || RungMessage != 3 || RungType != 4 {
		t.Error("rung numbers are hashed into keys and must not move")
	}
}

// javaStack builds a trace of the given methods, one frame each.
func javaStack(methods ...string) string {
	var b strings.Builder
	for i, m := range methods {
		fmt.Fprintf(&b, "\tat %s(File.java:%d)\n", m, i+1)
	}
	return b.String()
}

func TestFingerprintUsesTopFiveFrames(t *testing.T) {
	key := func(methods ...string) string {
		t.Helper()
		return mustFingerprint(t, SpanException{Type: "T", StackTrace: javaStack(methods...)}).Key
	}
	base := []string{"com.acme.A.a", "com.acme.B.b", "com.acme.C.c", "com.acme.D.d", "com.acme.E.e", "com.acme.F.f"}
	fp := mustFingerprint(t, SpanException{Type: "T", StackTrace: javaStack(base...)})
	if len(fp.Frames) != FingerprintFrames || fp.Frames[4].Function != "e" {
		t.Fatalf("frames = %+v, want the top %d", fp.Frames, FingerprintFrames)
	}
	if key(base...) != key("com.acme.A.a", "com.acme.B.b", "com.acme.C.c", "com.acme.D.d", "com.acme.E.e", "com.acme.Z.z") {
		t.Error("the sixth frame must not change the key")
	}
	if key(base...) == key("com.acme.A.a", "com.acme.B.b", "com.acme.C.c", "com.acme.D.d", "com.acme.Z.z", "com.acme.F.f") {
		t.Error("the fifth frame must change the key")
	}
	if key(base...) == key("com.acme.Z.z", "com.acme.B.b", "com.acme.C.c", "com.acme.D.d", "com.acme.E.e") {
		t.Error("the top frame must change the key")
	}
	if key("com.acme.A.a") != key("com.acme.A.a", "java.lang.Thread.run") {
		t.Error("a runtime frame below must not change an in-app key")
	}
	// Five runtime frames on top do not push the in-app frame out.
	deep := []string{"java.util.A.a", "java.util.B.b", "java.util.C.c", "java.util.D.d", "java.util.E.e", "com.acme.A.a"}
	if key(deep...) != key("com.acme.A.a") {
		t.Error("an in-app frame below five runtime frames must still key the exception")
	}
	// The same holds on the all-frames rung: top five, in order.
	runtime := []string{"java.util.A.a", "java.util.B.b", "java.util.C.c", "java.util.D.d", "java.util.E.e", "java.util.F.f"}
	fp = mustFingerprint(t, SpanException{Type: "T", StackTrace: javaStack(runtime...)})
	if fp.Rung != RungFrames || len(fp.Frames) != FingerprintFrames || fp.Frames[4].Function != "e" {
		t.Errorf("rung %v frames %+v", fp.Rung, fp.Frames)
	}
}

func TestFingerprintCollapsesRecursionAcrossFrameworkFrames(t *testing.T) {
	direct := javaStack("com.acme.Tree.walk", "com.acme.Tree.start")
	viaProxy := javaStack("com.acme.Tree.walk", "org.springframework.aop.Proxy.invoke",
		"com.acme.Tree.walk", "org.springframework.aop.Proxy.invoke", "com.acme.Tree.walk", "com.acme.Tree.start")
	a := mustFingerprint(t, SpanException{Type: "T", StackTrace: direct})
	b := mustFingerprint(t, SpanException{Type: "T", StackTrace: viaProxy})
	if a.Key != b.Key || len(b.Frames) != 2 {
		t.Errorf("recursion through a proxy split the group: %+v vs %+v", a.Frames, b.Frames)
	}
}

// A panic in a handler of a Go service: the application's module has no host
// in its path, the router is a dependency from the module cache.
func TestFingerprintGoApplicationFrames(t *testing.T) {
	read := func(name string) SpanException {
		t.Helper()
		stack, err := os.ReadFile(filepath.Join(exceptionCorpusDir, name))
		if err != nil {
			t.Fatal(err)
		}
		return SpanException{Type: "runtime.Error", StackTrace: string(stack)}
	}
	add, addAgain, remove := mustFingerprint(t, read("go-handler-add.txt")),
		mustFingerprint(t, read("go-handler-add-b.txt")), mustFingerprint(t, read("go-handler-remove.txt"))
	want := frames(StackGo, "shop/internal/cart", "Service.AddItem", "shop/internal/api", "Handler.PostItem")
	if add.Rung != RungAppFrames || !reflect.DeepEqual(add.Frames, want) {
		t.Errorf("rung %v frames %+v, want the application's two", add.Rung, add.Frames)
	}
	if add.Key != addAgain.Key {
		t.Error("the same panic under another router version must keep its key")
	}
	if add.Key == remove.Key {
		t.Error("two panics in one binary must not share a key")
	}
}

// The type and the frames of a fingerprint describe the same exception. Where
// the stack text of one event holds a chain, that is the root cause: its
// type is read from the text, and the event's own type is kept for display.
func TestFingerprintTypeIsTheRootCauses(t *testing.T) {
	cases := []struct {
		name, eventType, stack string
		wantType               string
		wantTop                string // function of the first frame
	}{
		{"java: the last cause", "com.example.shop.CheckoutException", `com.example.shop.CheckoutException: checkout failed
	at com.example.shop.Checkout.run(Checkout.java:30)
Caused by: com.example.shop.CartException: cart failed
	at com.example.shop.Cart.add(Cart.java:12)
Caused by: java.lang.IllegalStateException: Cart 7 is locked
	at com.example.shop.CartService.addItem(CartService.java:42)`,
			"java.lang.IllegalStateException", "addItem"},
		{"java: a cause without a message", "com.example.shop.CheckoutException", `	at com.example.shop.Checkout.run(Checkout.java:30)
Caused by: java.lang.NullPointerException
	at com.example.shop.CartService.addItem(CartService.java:42)`,
			"java.lang.NullPointerException", "addItem"},
		{"java: a last cause without frames is not the root", "com.example.shop.CheckoutException", `	at com.example.shop.Checkout.run(Checkout.java:30)
Caused by: com.example.shop.CartException: cart failed
	at com.example.shop.Cart.add(Cart.java:12)
Caused by: java.lang.IllegalStateException: Cart 7 is locked`,
			"com.example.shop.CartException", "add"},
		{"java: no chain", "com.example.shop.CheckoutException", `java.lang.Error: the text names another type
	at com.example.shop.Checkout.run(Checkout.java:30)`,
			"com.example.shop.CheckoutException", "run"},
		{"java: a cause whose type cannot be read", "com.example.shop.CheckoutException", `	at com.example.shop.Checkout.run(Checkout.java:30)
Caused by: <unknown>
	at com.example.shop.CartService.addItem(CartService.java:42)`,
			"com.example.shop.CheckoutException", "addItem"},
		{"java: the cause of a suppressed exception is nobody's root", "com.example.shop.CheckoutException", `	at com.example.shop.Checkout.run(Checkout.java:30)
	Suppressed: java.io.IOException: close failed
		at com.example.shop.Conn.close(Conn.java:5)
	Caused by: java.net.SocketException: broken pipe
		at com.example.shop.Socket.write(Socket.java:9)`,
			"com.example.shop.CheckoutException", "run"},
		{"dotnet: the innermost exception", "Example.Shop.CheckoutException", `Example.Shop.CheckoutException: checkout failed
 ---> Example.Shop.CartException: cart failed
 ---> System.InvalidOperationException: Cart 7 is locked
   at Example.Shop.CartService.AddItem(String sku)
   --- End of inner exception stack trace ---
   at Example.Shop.Cart.Add(String sku)
   --- End of inner exception stack trace ---
   at Example.Shop.Checkout.Run()`,
			"System.InvalidOperationException", "AddItem"},
		{"dotnet: inner exceptions on the first line", "System.AggregateException",
			"System.AggregateException: One or more errors occurred. ---> (Inner Exception #0) System.TimeoutException: timed out\n" +
				"   at Example.Shop.Stock.Reserve(String sku)\n   --- End of inner exception stack trace ---\n   at Example.Shop.Checkout.Run()",
			"System.TimeoutException", "Reserve"},
		{"dotnet: no chain", "Example.Shop.CheckoutException", `System.Exception: the text names another type
   at Example.Shop.Checkout.Run()`,
			"Example.Shop.CheckoutException", "Run"},
		{"python: the first exception raised", "RuntimeError", `Traceback (most recent call last):
  File "/app/shop/cart.py", line 42, in add_item
    raise ValueError("locked")
ValueError: locked

During handling of the above exception, another exception occurred:

Traceback (most recent call last):
  File "/app/shop/api.py", line 17, in post
    cart.add_item(sku)
RuntimeError: failed`,
			"ValueError", "add_item"},
		{"python: a qualified type", "RuntimeError", `Traceback (most recent call last):
  File "/app/shop/cart.py", line 42, in add_item
    self.session.commit()
sqlalchemy.exc.OperationalError: (psycopg2.OperationalError) server closed the connection

The above exception was the direct cause of the following exception:

Traceback (most recent call last):
  File "/app/shop/api.py", line 17, in post
    cart.add_item(sku)
RuntimeError: failed`,
			"sqlalchemy.exc.OperationalError", "add_item"},
		{"python: the first member of a group", "ExceptionGroup", `  + Exception Group Traceback (most recent call last):
  |   File "/app/shop/api.py", line 17, in post
  |     async with asyncio.TaskGroup() as tg:
  | ExceptionGroup: unhandled errors in a TaskGroup (2 sub-exceptions)
  +-+---------------- 1 ----------------
    | Traceback (most recent call last):
    |   File "/app/shop/cart.py", line 42, in add_item
    |     raise ValueError("locked")
    | ValueError: locked
    +---------------- 2 ----------------
    | Traceback (most recent call last):
    |   File "/app/shop/stock.py", line 9, in reserve
    |     raise KeyError(sku)
    | KeyError: 'kettle'
    +------------------------------------`,
			"ValueError", "add_item"},
		{"python: no chain", "RuntimeError", `Traceback (most recent call last):
  File "/app/shop/cart.py", line 42, in add_item
    raise ValueError("locked")
ValueError: the text names another type`,
			"RuntimeError", "add_item"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fp := mustFingerprint(t, SpanException{Type: " " + tc.eventType + " ", Message: "checkout failed", StackTrace: tc.stack})
			if fp.Type != tc.wantType || fp.EventType != tc.eventType {
				t.Errorf("type %q of event type %q, want %q of %q", fp.Type, fp.EventType, tc.wantType, tc.eventType)
			}
			if len(fp.Frames) == 0 || fp.Frames[0].Function != tc.wantTop {
				t.Errorf("frames %+v, want %s on top", fp.Frames, tc.wantTop)
			}
			// The key is the one of the root cause recorded on its own.
			alone := mustFingerprint(t, SpanException{Type: tc.wantType, StackTrace: stackOf(fp.Frames)})
			if tc.wantType != tc.eventType && alone.Key != fp.Key {
				t.Errorf("key %s, want that of the root cause alone, %s (%+v)", fp.Key, alone.Key, alone.Frames)
			}
		})
	}
	// Below the frame rungs there is no root cause to read: the type is the
	// event's, like the message.
	fp := mustFingerprint(t, SpanException{Type: "A", Message: "m", StackTrace: "Caused by: B: no frames anywhere"})
	if fp.Rung != RungMessage || fp.Type != "A" || fp.EventType != "A" {
		t.Errorf("rung %v type %q event type %q", fp.Rung, fp.Type, fp.EventType)
	}
}

// stackOf writes frames back as a trace in their notation, one exception.
func stackOf(frames []StackFrame) string {
	var b strings.Builder
	for i := len(frames) - 1; i >= 0 && frames[0].Syntax == StackPython; i-- {
		fmt.Fprintf(&b, "  File \"/app/%s\", line 1, in %s\n    pass\n", frames[i].Module, frames[i].Function)
	}
	for _, f := range frames {
		switch f.Syntax {
		case StackJava:
			fmt.Fprintf(&b, "\tat %s.%s(File.java:1)\n", f.Module, f.Function)
		case StackDotNet:
			fmt.Fprintf(&b, "   at %s.%s()\n", f.Module, f.Function)
		}
	}
	return b.String()
}

// onlyModule treats one module as the application.
type onlyModule string

func (m onlyModule) InApp(f StackFrame) bool { return f.Module == string(m) }
func (m onlyModule) ID() string              { return "only:" + string(m) }

func TestFingerprintUsesTheGivenClassifier(t *testing.T) {
	ex := SpanException{Type: "T", StackTrace: cartStack}
	fp, _ := FingerprintExceptions([]SpanException{ex}, onlyModule("org.springframework.web.Dispatcher"))
	want := frames(StackJava, "org.springframework.web.Dispatcher", "dispatch")
	if fp.Rung != RungAppFrames || !reflect.DeepEqual(fp.Frames, want) {
		t.Errorf("rung %v frames %+v", fp.Rung, fp.Frames)
	}
	fp, _ = FingerprintExceptions([]SpanException{ex}, onlyModule("nothing"))
	if fp.Rung != RungFrames || len(fp.Frames) != 3 {
		t.Errorf("rung %v frames %+v", fp.Rung, fp.Frames)
	}
}

// namedBuiltin decides exactly as the built-in classifier does, under a name.
type namedBuiltin string

func (namedBuiltin) InApp(f StackFrame) bool { return builtinInApp(f) }
func (n namedBuiltin) ID() string            { return string(n) }

// A classifier other than the built-in one is not part of the key version,
// so its keys must be a family of their own: never equal to a built-in key
// or to the key of a classifier with another id, on any rung, even where it
// picks the same frames.
func TestFingerprintKeysCarryTheClassifier(t *testing.T) {
	for rung, ex := range map[FingerprintRung]SpanException{
		RungAppFrames: {Type: "T", Message: "Cart 7 is locked", StackTrace: cartStack},
		RungFrames:    {Type: "T", StackTrace: javaStack("java.util.ArrayList.get")},
		RungMessage:   {Type: "T", Message: "Cart 7 is locked"},
		RungType:      {Type: "T"},
	} {
		builtin := mustFingerprint(t, ex)
		keys := map[string]string{builtin.Key: "the built-in classifier"}
		for _, id := range []string{"", "team-a", "team-b"} {
			fp, _ := FingerprintExceptions([]SpanException{ex}, namedBuiltin(id))
			if fp.Rung != rung || !reflect.DeepEqual(fp.Frames, builtin.Frames) {
				t.Fatalf("%v: classifier %q chose rung %v frames %+v", rung, id, fp.Rung, fp.Frames)
			}
			if other, dup := keys[fp.Key]; dup {
				t.Errorf("%v: classifier %q shares key %s with %s", rung, id, fp.Key, other)
			}
			keys[fp.Key] = "classifier " + id
			if again, _ := FingerprintExceptions([]SpanException{ex}, namedBuiltin(id)); again.Key != fp.Key {
				t.Errorf("%v: classifier %q is not stable: %s, %s", rung, id, fp.Key, again.Key)
			}
		}
	}
	// The id is a field of its own: it cannot run into the type.
	a, _ := FingerprintExceptions([]SpanException{{Type: "bT"}}, namedBuiltin("a"))
	b, _ := FingerprintExceptions([]SpanException{{Type: "T"}}, namedBuiltin("ab"))
	if a.Key == b.Key {
		t.Error("the classifier id runs into the type")
	}
}

func TestRootException(t *testing.T) {
	cases := []struct {
		name string
		excs []SpanException
		want string
	}{
		{"a single event", []SpanException{{Type: "A"}}, "A"},
		{"the last event without a chain", []SpanException{{Type: "A"}, {Type: "B"}, {Type: "C"}}, "C"},
		{"the flagged root", []SpanException{{Type: "A"}, {Type: "B", CauseRoot: true}, {Type: "C"}}, "B"},
		{"the flag wins over ids", []SpanException{
			{Type: "A", ID: "1", CausedByID: "2"}, {Type: "B", ID: "2"}, {Type: "C", CauseRoot: true}}, "C"},
		{"the end of an id chain", []SpanException{
			{Type: "A", ID: "1", CausedByID: "2"}, {Type: "B", ID: "2", CausedByID: "3"},
			{Type: "C", ID: "3"}, {Type: "D", ID: "4"}}, "C"},
		{"the chain's end may name a cause outside the span", []SpanException{
			{Type: "A", ID: "2", CausedByID: "9"}, {Type: "B", ID: "1", CausedByID: "2"}, {Type: "C"}}, "A"},
		{"ids nobody refers to are no chain", []SpanException{{Type: "A", ID: "1"}, {Type: "B", ID: "2"}}, "B"},
		{"events without ids are no chain", []SpanException{{Type: "A"}, {Type: "B", CausedByID: ""}}, "B"},
		{"a missing id is nobody's cause", []SpanException{
			{Type: "A", ID: "1"}, {Type: "B", CausedByID: "1"}, {Type: "C"}}, "A"},
		{"a cycle has no end", []SpanException{
			{Type: "A", ID: "1", CausedByID: "2"}, {Type: "B", ID: "2", CausedByID: "1"}}, "B"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rootException(tc.excs).Type; got != tc.want {
				t.Errorf("root = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestSpanExceptionCarriesCauseChainFields(t *testing.T) {
	rec := map[string]any{"span.events": []any{
		map[string]any{"span_event.name": "exception", "exception.type": "A",
			"exception.id": "7", "exception.caused_by_id": "8", "exception.is_caused_by_root": false},
		map[string]any{"span_event.name": "exception", "exception.type": "B",
			"exception.id": float64(8), "exception.is_caused_by_root": true},
		map[string]any{"span_event.name": "exception", "exception.type": "C",
			"exception.is_caused_by_root": "true"},
	}}
	excs := SpanExceptions(rec)
	want := []SpanException{
		{Type: "A", ID: "7", CausedByID: "8"},
		{Type: "B", ID: "8", CauseRoot: true},
		{Type: "C", CauseRoot: true},
	}
	if !reflect.DeepEqual(excs, want) {
		t.Errorf("exceptions = %+v\nwant %+v", excs, want)
	}
	if fp := mustFingerprint(t, excs...); fp.Type != "B" {
		t.Errorf("fingerprinted %s, want the flagged root B", fp.Type)
	}
}

// --- corpus -------------------------------------------------------------------

const exceptionCorpusDir = "testdata/exceptions"

// corpusCase is one span's exception events and the group it must land in:
// cases that share a group must share a key, and no two groups may.
type corpusCase struct {
	Name   string `json:"name"`
	Group  string `json:"group"`
	Rung   string `json:"rung"`
	Events []struct {
		Type       string `json:"type"`
		Message    string `json:"message"`
		Stack      string `json:"stack"` // file in the corpus directory
		ID         string `json:"id"`
		CausedByID string `json:"caused_by_id"`
		Root       bool   `json:"root"`
	} `json:"events"`
}

func loadExceptionCorpus(t *testing.T) []corpusCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(exceptionCorpusDir, "corpus.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []corpusCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	return cases
}

func (c corpusCase) fingerprint(t *testing.T) ExceptionFingerprint {
	t.Helper()
	var excs []SpanException
	for _, ev := range c.Events {
		ex := SpanException{Type: ev.Type, Message: ev.Message, ID: ev.ID, CausedByID: ev.CausedByID, CauseRoot: ev.Root}
		if ev.Stack != "" {
			stack, err := os.ReadFile(filepath.Join(exceptionCorpusDir, ev.Stack))
			if err != nil {
				t.Fatal(err)
			}
			ex.StackTrace = string(stack)
		}
		excs = append(excs, ex)
	}
	return mustFingerprint(t, excs...)
}

func TestExceptionCorpusGroups(t *testing.T) {
	cases := loadExceptionCorpus(t)
	if len(cases) == 0 {
		t.Fatal("empty corpus")
	}
	keyOfGroup := map[string]string{}
	groupOfKey := map[string]string{}
	names := map[string]bool{}
	for _, c := range cases {
		if names[c.Name] {
			t.Errorf("duplicate case name %q", c.Name)
		}
		names[c.Name] = true
		fp := c.fingerprint(t)
		if fp.Rung.String() != c.Rung {
			t.Errorf("%s: rung %q, want %q", c.Name, fp.Rung, c.Rung)
		}
		if key, seen := keyOfGroup[c.Group]; seen && key != fp.Key {
			t.Errorf("%s: key %s splits group %s (%s)", c.Name, fp.Key, c.Group, key)
		}
		if group, seen := groupOfKey[fp.Key]; seen && group != c.Group {
			t.Errorf("%s: key %s merges groups %s and %s", c.Name, fp.Key, c.Group, group)
		}
		keyOfGroup[c.Group] = fp.Key
		groupOfKey[fp.Key] = c.Group
	}
}

// TestExceptionKeysAreStable pins the key of every corpus case. A key that
// moves means exceptions already grouped under it would regroup, so the
// fix is never to rewrite the golden line: raise ExceptionKeyVersion, which
// starts a new golden file, and say so in the change. -update only adds
// lines for new cases.
func TestExceptionKeysAreStable(t *testing.T) {
	golden := filepath.Join(exceptionCorpusDir, ExceptionKeyVersion+"-keys.golden")
	pinned := map[string]string{}
	raw, err := os.ReadFile(golden)
	if err != nil && !*updateExceptionKeys {
		t.Fatalf("%v (new key version? run with -update to create it)", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if key, name, ok := strings.Cut(line, "\t"); ok {
			pinned[name] = key
		}
	}

	var lines []string
	for _, c := range loadExceptionCorpus(t) {
		key := c.fingerprint(t).Key
		want, ok := pinned[c.Name]
		switch {
		case ok && want != key:
			t.Errorf("%s: key %s, pinned %s — the %s algorithm changed; raise ExceptionKeyVersion instead of repinning",
				c.Name, key, want, ExceptionKeyVersion)
		case !ok && !*updateExceptionKeys:
			t.Errorf("%s: no pinned key; run with -update to add %s", c.Name, key)
		}
		delete(pinned, c.Name)
		lines = append(lines, key+"\t"+c.Name)
	}
	for name := range pinned {
		t.Errorf("%s is pinned but no longer in the corpus", name)
	}
	if *updateExceptionKeys && !t.Failed() {
		sort.Slice(lines, func(i, j int) bool {
			return strings.SplitN(lines[i], "\t", 2)[1] < strings.SplitN(lines[j], "\t", 2)[1]
		})
		if err := os.WriteFile(golden, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
