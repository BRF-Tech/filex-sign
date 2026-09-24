//go:build ignore

// notogen writes internal/fontkit/noto_table.go: for every Unicode script,
// the Noto font that draws it, as a pinned URL + sha256 + size.
//
//	go run scripts/notogen/main.go            # regenerate against today's Google Fonts
//	go run scripts/notogen/main.go -check     # only report; write nothing
//
// ⚠⚠ Why these fonts, from there. The owner (2026-09-21): non-Latin text must
// print — any script the library can draw ("hepsini de destekleyebildiği
// kadar destekleyen bir kütüphane olsun") — and the fonts must be FETCHED on
// demand, adding 0 MB to the module ("online çekebilirsek 0 MB"). Noto is
// the family that exists for exactly this ("no tofu"), under the OFL.
//
// The files come from fonts.gstatic.com — Google Fonts' static, VERSIONED,
// immutable URLs (…/notosansjp/v56/….ttf), discovered through the CSS2 API
// with a client that asks for plain TrueType. Chosen over the Noto GitHub
// repositories because:
//
//   - they are STATIC Regular (wght 400) instances. The CJK fonts on GitHub
//     are variable fonts whose default instance is Thin (wght 100); using
//     them would mean instancing a variable font (gvar, HVAR, avar) inside
//     the app on every call, over an 18 MB file;
//   - one host serves every Noto family (all but the -UI and test builds),
//     so the app asks the administrator for ONE `http:` permission;
//   - they are smaller (Noto Sans JP: 5.3 MB static vs 9.6 MB variable).
//
// The URL alone is not trusted: the app pins every file's sha256, and the
// host refuses bytes that do not match (asset_fetch). A new Noto release is
// a regeneration and a review of this table's diff, never a hand edit.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"golang.org/x/image/font/sfnt"
)

const out = "internal/fontkit/noto_table.go"

// legacyUA makes the CSS2 API answer with ONE plain TrueType file per
// family instead of unicode-range slices of WOFF2.
const legacyUA = "Wget/1.12"

// Font choices that do not follow from the script's name.
var explicit = map[string][]string{
	// The Latin-family scripts and the shared characters.
	"Latin":    {"Noto Sans"},
	"Greek":    {"Noto Sans"},
	"Cyrillic": {"Noto Sans"},
	// Common is tried as a chain: most of it (punctuation, digits, currency)
	// is in Noto Sans; the rest of the symbols are spread over three faces.
	"Common":    {"Noto Sans", "Noto Sans Symbols", "Noto Sans Symbols 2", "Noto Sans Math"},
	"Inherited": {"Noto Sans"},
	// Han is drawn in the regional face the text itself asks for (kana →
	// Japanese, hangul → Korean, bopomofo → Traditional Chinese; otherwise
	// Simplified). See fontkit.hanFamily.
	"Han":      {"Noto Sans SC"},
	"Hiragana": {"Noto Sans JP"},
	"Katakana": {"Noto Sans JP"},
	"Hangul":   {"Noto Sans KR"},
	"Bopomofo": {"Noto Sans TC"},
	// Names Google Fonts spells differently from the Unicode script.
	"Nyiakeng_Puachue_Hmong": {"Noto Serif NP Hmong"},
	"Meroitic_Cursive":       {"Noto Sans Meroitic"},
	"Meroitic_Hieroglyphs":   {"Noto Sans Meroitic"},
	"Braille":                {"Noto Sans Symbols 2"},
}

// Regional Han faces, keyed the way fontkit asks for them.
var hanRegions = map[string]string{"Han/ja": "Noto Sans JP", "Han/ko": "Noto Sans KR", "Han/zh-Hant": "Noto Sans TC"}

type font struct {
	Family string
	URL    string
	SHA256 string
	Size   int64
	data   []byte
}

func main() {
	check := flag.Bool("check", false, "report only; do not write the table")
	flag.Parse()

	families := googleNotoFamilies()
	norm := func(s string) string { return strings.ToLower(strings.NewReplacer(" ", "", "_", "").Replace(s)) }
	byNorm := map[string]string{}
	for _, f := range families {
		byNorm[norm(f)] = f
	}

	var names []string
	for name := range unicode.Scripts {
		names = append(names, name)
	}
	sort.Strings(names)

	scripts := map[string][]string{}
	var none []string
	for _, name := range names {
		if fams, ok := explicit[name]; ok {
			scripts[name] = fams
			continue
		}
		var pick string
		for _, cand := range []string{"Noto Sans " + name, "Noto Serif " + name, "Noto " + name} {
			if f, ok := byNorm[norm(cand)]; ok {
				pick = f
				break
			}
		}
		if pick == "" {
			none = append(none, name)
			continue
		}
		scripts[name] = []string{pick}
	}
	for k, v := range hanRegions {
		scripts[k] = []string{v}
	}

	// Every family the table names, fetched once, hashed, and checked for
	// how much of its script it actually draws.
	need := map[string]bool{}
	for _, fams := range scripts {
		for _, f := range fams {
			need[f] = true
		}
	}
	fonts := map[string]*font{}
	var famList []string
	for f := range need {
		famList = append(famList, f)
	}
	sort.Strings(famList)
	for i, fam := range famList {
		ft, err := fetchFamily(fam)
		if err != nil {
			fmt.Fprintf(os.Stderr, "!! %s: %v\n", fam, err)
			os.Exit(1)
		}
		fonts[fam] = ft
		fmt.Fprintf(os.Stderr, "[%d/%d] %-40s %9d bytes %s\n", i+1, len(famList), fam, ft.Size, ft.SHA256[:12])
	}

	// A script whose only candidate draws little of it is not "supported":
	// say so rather than promise a face that prints holes.
	//
	// ⚠ Measured over the WHOLE script, so the bar is low (20%) and the
	// explicit choices are not measured at all: Unicode's Han is ~100,000
	// characters (every extension block), of which a regional face draws the
	// ~30,000 in use — 27% — and Egyptian hieroglyphs gained 4,000 extended
	// signs in Unicode 16 beside the 1,071 the face draws. A name-matched face
	// that draws less than a fifth of its script is a coincidence of names.
	for _, name := range names {
		fams, ok := scripts[name]
		if _, chosen := explicit[name]; !ok || chosen {
			continue
		}
		cov := coverage(fonts[fams[0]].data, unicode.Scripts[name])
		if cov < 0.2 {
			fmt.Fprintf(os.Stderr, "-- %s: %s draws only %.0f%% of it; left out\n", name, fams[0], cov*100)
			delete(scripts, name)
			none = append(none, name)
		}
	}
	sort.Strings(none)
	fmt.Fprintf(os.Stderr, "%d scripts have a font, %d have none: %s\n", len(scripts), len(none), strings.Join(none, ", "))
	if *check {
		return
	}
	writeTable(scripts, fonts, none)
}

// googleNotoFamilies lists the Noto families Google Fonts serves.
func googleNotoFamilies() []string {
	body := get("https://fonts.google.com/metadata/fonts", "")
	body = bytes.TrimPrefix(body, []byte(")]}'"))
	var meta struct {
		FamilyMetadataList []struct {
			Family string `json:"family"`
		} `json:"familyMetadataList"`
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		panic(err)
	}
	var out []string
	for _, f := range meta.FamilyMetadataList {
		if strings.HasPrefix(f.Family, "Noto ") {
			out = append(out, f.Family)
		}
	}
	return out
}

var srcRe = regexp.MustCompile(`url\((https://fonts\.gstatic\.com/[^)]+\.ttf)\)`)

// fetchFamily asks the CSS2 API for the family's Regular as one TrueType
// file (its only weight when it has no 400), downloads and hashes it.
func fetchFamily(fam string) (*font, error) {
	q := strings.ReplaceAll(fam, " ", "+")
	css := get("https://fonts.googleapis.com/css2?family="+q+":wght@400", legacyUA)
	m := srcRe.FindAllSubmatch(css, -1)
	if len(m) == 0 {
		css = get("https://fonts.googleapis.com/css2?family="+q, legacyUA)
		m = srcRe.FindAllSubmatch(css, -1)
	}
	if len(m) != 1 {
		return nil, fmt.Errorf("expected one TrueType file, the API answered %d", len(m))
	}
	url := string(m[0][1])
	data := get(url, "")
	if _, err := sfnt.Parse(data); err != nil {
		return nil, fmt.Errorf("%s is not a font: %v", url, err)
	}
	sum := sha256.Sum256(data)
	return &font{Family: fam, URL: url, SHA256: fmt.Sprintf("%x", sum), Size: int64(len(data)), data: data}, nil
}

// coverage is the share of a script's characters the font has a glyph for.
func coverage(data []byte, table *unicode.RangeTable) float64 {
	f, err := sfnt.Parse(data)
	if err != nil {
		return 0
	}
	var b sfnt.Buffer
	total, have := 0, 0
	visit := func(lo, hi, stride rune) {
		for r := lo; r <= hi; r += stride {
			total++
			if gid, err := f.GlyphIndex(&b, r); err == nil && gid != 0 {
				have++
			}
		}
	}
	for _, r := range table.R16 {
		visit(rune(r.Lo), rune(r.Hi), rune(r.Stride))
	}
	for _, r := range table.R32 {
		visit(rune(r.Lo), rune(r.Hi), rune(r.Stride))
	}
	if total == 0 {
		return 0
	}
	return float64(have) / float64(total)
}

func get(url, ua string) []byte {
	for attempt := 0; ; attempt++ {
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		if ua != "" {
			req.Header.Set("User-Agent", ua)
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			b, rerr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if rerr == nil {
				return b
			}
			err = rerr
		} else if err == nil {
			resp.Body.Close()
			err = fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		if attempt == 3 {
			panic(fmt.Sprintf("%s: %v", url, err))
		}
		time.Sleep(time.Duration(attempt+1) * 2 * time.Second)
	}
}

func writeTable(scripts map[string][]string, fonts map[string]*font, none []string) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "// Code generated by go run scripts/notogen/main.go on %s; DO NOT EDIT.\n\n", time.Now().UTC().Format("2006-01-02"))
	b.WriteString("package fontkit\n\n")
	b.WriteString("// notoFonts are the Noto faces this app may fetch: Google Fonts' static\n")
	b.WriteString("// Regular TrueType instances, each pinned by sha256 (see scripts/notogen).\n")
	b.WriteString("var notoFonts = map[string]NotoFont{\n")
	var fams []string
	for f := range fonts {
		fams = append(fams, f)
	}
	sort.Strings(fams)
	for _, f := range fams {
		ft := fonts[f]
		fmt.Fprintf(&b, "\t%q: {Family: %q, URL: %q, SHA256: %q, Size: %d},\n", f, f, ft.URL, ft.SHA256, ft.Size)
	}
	b.WriteString("}\n\n")
	b.WriteString("// notoScripts maps a Unicode script (Go's unicode.Scripts name) to the\n")
	b.WriteString("// faces that draw it, in the order they are tried.\n")
	b.WriteString("var notoScripts = map[string][]string{\n")
	var names []string
	for n := range scripts {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(&b, "\t%q: {%s},\n", n, quoteAll(scripts[n]))
	}
	b.WriteString("}\n\n")
	b.WriteString("// notoNone are the scripts no Noto face draws: text in them cannot be\n")
	b.WriteString("// printed, and the screens say so.\n")
	fmt.Fprintf(&b, "var notoNone = []string{%s}\n", quoteAll(none))
	src, err := format.Source(b.Bytes())
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(out, src, 0o644); err != nil {
		panic(err)
	}
	fmt.Fprintf(os.Stderr, "wrote %s\n", out)
}

func quoteAll(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(q, ", ")
}
