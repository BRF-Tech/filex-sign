package i18n

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// ⚠⚠ The owner, 2026-09-22: "translate every text the signing app
// carries" — into es, de and fr. This file is what keeps "every" true
// after the next string is added. It reads the module's own source and:
//
//   - finds every function that takes an English–Turkish pair (a
//     parameter `en` followed by `tr`) — T, Tf, S, Sf, and every helper
//     built on them — and collects the English written at each call;
//   - refuses a call whose words are not written right there (a variable,
//     a concatenation): no catalogue can hold a sentence built at run time,
//     so that sentence would silently stay English in three languages;
//   - refuses a hand-made wire.Text map and any `"tr"` literal: those are
//     texts that go around the catalogue;
//   - and holds every catalogue to exactly those keys: nothing missing,
//     nothing stale, no empty value, the same format verbs for the same
//     arguments, the same leading and trailing whitespace.
//
// SIGN_I18N_DUMP=<file> writes what it collected (English, Turkish, where)
// for a translator.

// root is the module's root, from internal/i18n.
const root = "../.."

type found struct {
	EN, TR string
	Where  []string
}

type textFunc struct {
	en  int
	ctx bool
}

type scan struct {
	fset  *token.FileSet
	files map[string]*ast.File
	// Text functions, by where a call can see them: a closure only in its
	// own file, a function only in its own package when called by bare
	// name, and anything by name through a selector (views.T, l.S, a.say).
	closures  map[string]map[string]textFunc // file → name
	pkgFuncs  map[string]map[string]textFunc // package dir → name
	textFuncs map[string]textFunc            // name, for selector calls
	keys      map[string]*found
	problems  []string
}

func (s *scan) complain(pos token.Pos, format string, args ...any) {
	p := s.fset.Position(pos)
	rel, _ := filepath.Rel(root, p.Filename)
	s.problems = append(s.problems, fmt.Sprintf("%s:%d: %s", filepath.ToSlash(rel), p.Line, fmt.Sprintf(format, args...)))
}

func readModule(t *testing.T) *scan {
	t.Helper()
	s := &scan{fset: token.NewFileSet(), files: map[string]*ast.File{}, textFuncs: map[string]textFunc{}, closures: map[string]map[string]textFunc{}, pkgFuncs: map[string]map[string]textFunc{}, keys: map[string]*found{}}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "testdata", "node_modules", "dist", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(s.fset, path, nil, 0)
		if err != nil {
			return err
		}
		s.files[path] = f
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Pass 1: which functions take an English–Turkish pair.
	for path, f := range s.files {
		dir := filepath.Dir(path)
		ast.Inspect(f, func(n ast.Node) bool {
			switch fn := n.(type) {
			case *ast.FuncDecl:
				if tf, ok := enParam(fn.Type); ok {
					s.textFuncs[fn.Name.Name] = tf
					if fn.Recv == nil {
						if s.pkgFuncs[dir] == nil {
							s.pkgFuncs[dir] = map[string]textFunc{}
						}
						s.pkgFuncs[dir][fn.Name.Name] = tf
					}
				}
			case *ast.AssignStmt:
				for k, rhs := range fn.Rhs {
					if lit, ok := rhs.(*ast.FuncLit); ok && k < len(fn.Lhs) {
						if id, ok := fn.Lhs[k].(*ast.Ident); ok {
							if tf, ok := enParam(lit.Type); ok {
								if s.closures[path] == nil {
									s.closures[path] = map[string]textFunc{}
								}
								s.closures[path][id.Name] = tf
							}
						}
					}
				}
			}
			return true
		})
	}
	// Pass 2: every call to one of them, and everything that goes around.
	for path, f := range s.files {
		inI18n := filepath.ToSlash(filepath.Dir(path)) == filepath.ToSlash(filepath.Join(root, "internal", "i18n"))
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				s.call(path, x)
			case *ast.CompositeLit:
				s.composite(x, inI18n)
			case *ast.BasicLit:
				if !inI18n && x.Kind == token.STRING && x.Value == `"tr"` {
					s.complain(x.Pos(), `a "tr" literal: a text that picks Turkish by hand goes around the catalogue — use T/Tf/S/Sf/Each`)
				}
			}
			return true
		})
	}
	return s
}

// enParam finds a parameter named `en` directly followed by one named
// `tr` (and a `ctx` right before them).
func enParam(ft *ast.FuncType) (textFunc, bool) {
	var names []string
	for _, f := range ft.Params.List {
		if len(f.Names) == 0 {
			names = append(names, "_")
		}
		for _, n := range f.Names {
			names = append(names, n.Name)
		}
	}
	for i := 0; i+1 < len(names); i++ {
		if names[i] == "en" && names[i+1] == "tr" {
			return textFunc{en: i, ctx: i > 0 && names[i-1] == "ctx"}, true
		}
	}
	return textFunc{}, false
}

func calleeName(e ast.Expr) string {
	switch f := e.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

func (s *scan) call(path string, c *ast.CallExpr) {
	name := calleeName(c.Fun)
	var tf textFunc
	var ok bool
	if _, bare := c.Fun.(*ast.Ident); bare {
		if tf, ok = s.closures[path][name]; !ok {
			tf, ok = s.pkgFuncs[filepath.Dir(path)][name]
		}
	} else {
		tf, ok = s.textFuncs[name]
	}
	if !ok {
		return
	}
	i := tf.en
	if len(c.Args) < i+2 {
		// fail(intakeWords(err)) — a pair made somewhere else and spread
		// in: its words were never written at a text call.
		s.complain(c.Pos(), "%s(…) is given a pair built elsewhere — build a wire.Text there with T/Tf and hand that on", name)
		return
	}
	en, tr := c.Args[i], c.Args[i+1]
	if forwarded(en, "en") && forwarded(tr, "tr") && (!tf.ctx || forwarded(c.Args[i-1], "ctx")) {
		return
	}
	ev, eok := literal(en)
	tv, tok := literal(tr)
	if !eok || !tok {
		s.complain(c.Pos(), "%s(…) is given words that are not written here — the catalogue can only hold a literal (format the arguments in with %%s instead)", name)
		return
	}
	if tf.ctx {
		cv, cok := literal(c.Args[i-1])
		if !cok {
			s.complain(c.Pos(), "%s(…) is given a context that is not written here", name)
			return
		}
		ev = Key(cv, ev)
	}
	s.add(c.Pos(), ev, tv)
}

// forwarded: the caller hands on its OWN pair — parameters named en/tr, or
// the en/tr fields of a table written with literals (collected there).
// ⚠ A local variable named en is not forwarding: `en, tr := intakeWords(…)`
// is a pair built somewhere the scan never saw.
func forwarded(e ast.Expr, want string) bool {
	switch x := e.(type) {
	case *ast.Ident:
		if x.Name != want || x.Obj == nil {
			return false
		}
		_, param := x.Obj.Decl.(*ast.Field)
		return param
	case *ast.SelectorExpr:
		// p.en of a table, p.EN of a fields.Problem (collected where written).
		return x.Sel.Name == want || x.Sel.Name == strings.ToUpper(want)
	}
	return false
}

// literal is a string literal, or a constant sum of them.
func literal(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return "", false
		}
		v, err := strconv.Unquote(x.Value)
		return v, err == nil
	case *ast.BinaryExpr:
		if x.Op != token.ADD {
			return "", false
		}
		a, ok1 := literal(x.X)
		b, ok2 := literal(x.Y)
		return a + b, ok1 && ok2
	case *ast.ParenExpr:
		return literal(x.X)
	}
	return "", false
}

// tableFields are the positions of `en` and `tr` in a table's element
// struct (`[]struct{ en, tr, at string }`, `map[string]struct{ en, tr string }`).
func tableFields(t ast.Expr) (en, tr int, ok bool) {
	var elt ast.Expr
	switch x := t.(type) {
	case *ast.ArrayType:
		elt = x.Elt
	case *ast.MapType:
		elt = x.Value
	default:
		return 0, 0, false
	}
	st, isStruct := elt.(*ast.StructType)
	if !isStruct {
		return 0, 0, false
	}
	en, tr = -1, -1
	i := 0
	for _, f := range st.Fields.List {
		for _, n := range f.Names {
			switch n.Name {
			case "en":
				en = i
			case "tr":
				tr = i
			}
			i++
		}
	}
	return en, tr, en >= 0 && tr >= 0
}

func (s *scan) composite(c *ast.CompositeLit, inI18n bool) {
	// A table of English–Turkish rows written positionally.
	if ei, ti, ok := tableFields(c.Type); ok {
		for _, el := range c.Elts {
			if kv, isKV := el.(*ast.KeyValueExpr); isKV {
				el = kv.Value
			}
			row, isLit := el.(*ast.CompositeLit)
			if !isLit || len(row.Elts) == 0 {
				continue
			}
			if _, keyed := row.Elts[0].(*ast.KeyValueExpr); keyed {
				continue // read as its own composite below
			}
			if ei >= len(row.Elts) || ti >= len(row.Elts) {
				continue
			}
			ev, eok := literal(row.Elts[ei])
			tv, tok := literal(row.Elts[ti])
			if !eok || !tok {
				s.complain(row.Pos(), "a table row whose English–Turkish pair is not written here")
				continue
			}
			s.add(row.Pos(), ev, tv)
		}
	}
	if sel, ok := c.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == "Text" && !inI18n {
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == "wire" && len(c.Elts) > 0 {
			s.complain(c.Pos(), "a hand-made wire.Text: it carries only the languages written into it — use T/Tf/Plain/Each")
		}
	}
	// A struct written with an English–Turkish pair of fields
	// (fields.Problem{EN, TR}, the audit's {en, tr}).
	var en, tr ast.Expr
	for _, el := range c.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		k, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch k.Name {
		case "EN", "en":
			en = kv.Value
		case "TR", "tr":
			tr = kv.Value
		}
	}
	if en == nil || tr == nil {
		return
	}
	if forwarded(en, "en") && forwarded(tr, "tr") {
		return
	}
	ev, eok := literal(en)
	tv, tok := literal(tr)
	if !eok || !tok {
		s.complain(c.Pos(), "an English–Turkish pair of fields that is not written here — the catalogue can only hold a literal")
		return
	}
	s.add(c.Pos(), ev, tv)
}

func (s *scan) add(pos token.Pos, en, tr string) {
	p := s.fset.Position(pos)
	rel, _ := filepath.Rel(root, p.Filename)
	where := fmt.Sprintf("%s:%d", filepath.ToSlash(rel), p.Line)
	if k, ok := s.keys[en]; ok {
		k.Where = append(k.Where, where)
		if k.TR != tr {
			// Two meanings behind one English: a catalogue keyed by the
			// English can hold only one of them.
			s.problems = append(s.problems, fmt.Sprintf("%s: %q is also written at %s with another Turkish (%q vs %q) — the English must differ where the meaning does",
				where, en, k.Where[0], tr, k.TR))
		}
		return
	}
	s.keys[en] = &found{EN: en, TR: tr, Where: []string{where}}
}

var verbRE = regexp.MustCompile(`%(\[\d+\])?[-+# 0]*(\d+|\*)?(\.(\d+|\*)?)?(\[\d+\])?[a-zA-Z%]`)

// verbs is a format string's arguments as "index:verb", sorted — so a
// translation may reorder them with explicit indexes (%[2]s) but may not
// drop, add or retype one.
func verbs(s string) []string {
	var out []string
	next := 1
	for _, m := range verbRE.FindAllStringSubmatch(s, -1) {
		v := m[0]
		if strings.HasSuffix(v, "%") && len(v) == 2 {
			continue
		}
		idx := next
		for _, g := range []string{m[1], m[5]} {
			if g != "" {
				idx, _ = strconv.Atoi(strings.Trim(g, "[]"))
			}
		}
		out = append(out, fmt.Sprintf("%d:%c", idx, v[len(v)-1]))
		next = idx + 1
	}
	sort.Strings(out)
	return out
}

// edges are a string's leading and trailing whitespace.
//
// ⚠ Trimmed, not sliced at LastIndexFunc+1: that index is where the last
// rune STARTS, and "Rechazó" or "…”" end in a rune of two or three bytes —
// slicing one byte on reported a mismatch that was not there.
func edges(s string) (lead, trail string) {
	body := strings.TrimLeftFunc(s, unicode.IsSpace)
	if body == "" {
		return s, ""
	}
	trimmed := strings.TrimRightFunc(body, unicode.IsSpace)
	return s[:len(s)-len(body)], body[len(trimmed):]
}

func TestCatalogue_EveryTextIsWrittenWhereTheCatalogueCanReachIt(t *testing.T) {
	s := readModule(t)
	if len(s.keys) < 100 {
		t.Fatalf("only %d texts found — the scan is not reading the module", len(s.keys))
	}
	for _, p := range s.problems {
		t.Error(p)
	}
	if path := os.Getenv("SIGN_I18N_DUMP"); path != "" {
		var all []*found
		for _, k := range s.keys {
			all = append(all, k)
		}
		sort.Slice(all, func(i, j int) bool { return all[i].Where[0] < all[j].Where[0] })
		b, _ := json.MarshalIndent(all, "", "  ")
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCatalogue_EveryLanguageSaysEverythingAndNothingElse(t *testing.T) {
	s := readModule(t)
	for _, l := range Catalogued() {
		cat := Entries(l)
		var missing, stale []string
		for en := range s.keys {
			if _, ok := cat[en]; !ok {
				missing = append(missing, en)
			}
		}
		for en := range cat {
			if _, ok := s.keys[en]; !ok {
				stale = append(stale, en)
			}
		}
		sort.Strings(missing)
		sort.Strings(stale)
		for _, en := range missing {
			t.Errorf("%s: no translation for %q (%s)", l, en, s.keys[en].Where[0])
		}
		for _, en := range stale {
			t.Errorf("%s: %q is translated but no code says it any more", l, en)
		}
		for en, v := range cat {
			if strings.TrimSpace(v) == "" && strings.TrimSpace(en) != "" {
				t.Errorf("%s: %q is empty", l, en)
				continue
			}
			if a, b := strings.Join(verbs(en), ","), strings.Join(verbs(v), ","); a != b {
				t.Errorf("%s: %q has the format verbs [%s], the English [%s]:\n  %q", l, v, b, a, en)
			}
			el, et := edges(en)
			vl, vt := edges(v)
			if el != vl || et != vt {
				t.Errorf("%s: %q does not begin and end with the English's whitespace (%q…%q):\n  %q", l, v, el, et, en)
			}
		}
	}
}

// The manifest's words, in every language it declares.
func TestCatalogue_TheManifestSpeaksEveryLanguage(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(root, "filex-app.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	var declared []string
	for _, l := range m["languages"].([]any) {
		declared = append(declared, l.(string))
	}
	if strings.Join(declared, ",") != strings.Join(Languages(), ",") {
		t.Fatalf("filex-app.json declares %v, the code speaks %v", declared, Languages())
	}
	maps := 0
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case map[string]any:
			if s, ok := x["en"].(string); ok && s != "" {
				maps++
				for _, l := range declared {
					w, ok := x[l].(string)
					if !ok || strings.TrimSpace(w) == "" {
						t.Errorf("filex-app.json%s: no %s", path, l)
						continue
					}
					if a, b := strings.Join(verbs(s), ","), strings.Join(verbs(w), ","); a != b {
						t.Errorf("filex-app.json%s: %s has the format verbs [%s], the English [%s]", path, l, b, a)
					}
				}
				for k := range x {
					if !contains(declared, k) {
						t.Errorf("filex-app.json%s: %q is not a declared language", path, k)
					}
				}
				return
			}
			for k, e := range x {
				walk(path+"."+k, e)
			}
		case []any:
			for i, e := range x {
				walk(fmt.Sprintf("%s[%d]", path, i), e)
			}
		}
	}
	walk("", m)
	if maps < 30 {
		t.Fatalf("only %d texts found in the manifest", maps)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// The validator itself must fail on what it exists to catch.
func TestCatalogue_TheCheckerCatchesWhatItIsFor(t *testing.T) {
	if a, b := strings.Join(verbs("%s signed (%d/%d)"), ","), strings.Join(verbs("%s hat unterschrieben (%d/%d)"), ","); a != b {
		t.Fatalf("the same verbs read differently: %s vs %s", a, b)
	}
	if strings.Join(verbs("%s of %s"), ",") != strings.Join(verbs("%[2]s de %[1]s"), ",") {
		t.Fatal("an explicit reordering must be allowed")
	}
	if strings.Join(verbs("%d/%d"), ",") == strings.Join(verbs("%d"), ",") {
		t.Fatal("a dropped verb must be caught")
	}
	if strings.Join(verbs("%d"), ",") == strings.Join(verbs("%s"), ",") {
		t.Fatal("a retyped verb must be caught")
	}
	if len(verbs("100%% sure")) != 0 {
		t.Fatal("%% is not an argument")
	}
	if l, tr := edges("\n\nHello \n"); l != "\n\n" || tr != " \n" {
		t.Fatalf("edges: %q %q", l, tr)
	}
	if l, tr := edges("Rechazó"); l != "" || tr != "" {
		t.Fatalf("a multi-byte last rune is not whitespace: %q %q", l, tr)
	}
}
