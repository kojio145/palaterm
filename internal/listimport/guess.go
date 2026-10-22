package listimport

import (
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Env is what the guessing steps choose from: this PalaTerm's OS types and
// command sets. A guess depends on it, not only on the file — so a guessed
// command set is never written over an existing device's (see planCells).
type Env struct {
	Profiles []EnvProfile
	Sets     []EnvSet
}

// EnvProfile is an OS type (key and name).
type EnvProfile struct{ Key, Name string }

// EnvSet is a command set; Safe is false when it has a command that
// changes a device (guard.Check): such a set is never guessed.
type EnvSet struct {
	Name string
	Safe bool
}

// family is a vendor line a model belongs to: the standard OS type key
// for it, and the words a command set made for it is named with.
type family struct {
	key   string
	words []string
}

var (
	famIX       = family{"nec-ix", []string{"IX", "NEC", "UNIVERGE"}}
	famQX       = family{"nec-qx", []string{"QX"}}
	famWA       = family{"nec-wa", []string{"WA"}}
	famCisco    = family{"cisco-ios", []string{"CISCO", "CATALYST", "IOS"}}
	famASA      = family{"cisco-asa", []string{"ASA"}}
	famForti    = family{"fortinet-fortios", []string{"FORTIGATE", "FORTINET", "FORTI", "FG", "FGT"}}
	famFAZ      = family{"fortinet-analyzer", []string{"FORTIANALYZER", "FAZ"}}
	famJunos    = family{"juniper-junos", []string{"JUNIPER", "JUNOS", "SRX", "EX"}}
	famSSG      = family{"juniper-netscreen", []string{"SSG", "NETSCREEN", "SCREENOS"}}
	famAlaxala  = family{"alaxala-ax", []string{"ALAXALA", "AX"}}
	famYamaha   = family{"yamaha-rtx", []string{"YAMAHA", "RTX", "NVR"}}
	famAruba    = family{"aruba-os-switch", []string{"ARUBA"}}
	famComware  = family{"hpe-comware", []string{"HPE", "COMWARE", "H3C"}}
	famA10      = family{"a10-acos", []string{"A10", "THUNDER", "ACOS"}}
	reIX        = regexp.MustCompile(`(^|[^A-Z])IX-?\d{4}`)
	reCiscoNum  = regexp.MustCompile(`(^|[^A-Z])(WS-)?C(\d{4})`)
	reAlaxala   = regexp.MustCompile(`(^|[^A-Z])AX(\d)\d{3}`)
	reYamaha    = regexp.MustCompile(`(^|[^A-Z])(RTX|NVR)\d`)
	reJuniperEX = regexp.MustCompile(`(^|[^A-Z])(EX|QFX)\d`)
	reJuniperS  = regexp.MustCompile(`(^|[^A-Z])SRX\d`)
	reForti     = regexp.MustCompile(`(^|[^A-Z])(FG|FGT)-?\d`)
	reWA        = regexp.MustCompile(`(^|[^A-Z])WA\d{4}`)
	reDigits4   = regexp.MustCompile(`\d{4}`)
)

// kinds of device, with the words a command set for one is named with.
var kindWords = map[string][]string{
	"router": {"ルータ", "ルーター", "ROUTER", "RT"},
	"l2sw":   {"L2", "L2SW", "エッジ", "フロア"},
	"l3sw":   {"L3", "L3SW", "コア"},
	"fw":     {"FW", "ファイアウォール", "FIREWALL", "UTM"},
}

// classify tells a model's vendor line and kind ("" when unsure) from how
// it is written: "IX2215" → NEC IX router, "C2960" → Cisco L2 switch.
func classify(model string) (family, string, bool) {
	// Spaces stay as separators: "NEC IX2106" keeps IX a word of its own.
	m := strings.ToUpper(strings.Join(strings.Fields(norm.NFKC.String(model)), " "))
	if m == "" {
		return family{}, "", false
	}
	has := func(s ...string) bool {
		for _, x := range s {
			if strings.Contains(m, x) {
				return true
			}
		}
		return false
	}
	switch {
	case has("FORTIANALYZER") || strings.HasPrefix(m, "FAZ"):
		return famFAZ, "", true
	case has("FORTIGATE") || reForti.MatchString(m):
		return famForti, "fw", true
	// NEC writes UNIVERGE before QX switches and WA models as well as IX
	// routers: those two are told first.
	case has("QX-S", "QX-W") || strings.HasPrefix(m, "QX") || has(" QX"):
		return famQX, "l2sw", true
	case reWA.MatchString(m):
		return famWA, "", true
	case reIX.MatchString(m) || has("UNIVERGE") && has("IX"):
		return famIX, "router", true
	case has("ASA"):
		return famASA, "fw", true
	case has("ALAXALA") || reAlaxala.MatchString(m):
		kind := "l3sw"
		if g := reAlaxala.FindStringSubmatch(m); g != nil && g[2] < "3" {
			kind = "l2sw"
		}
		return famAlaxala, kind, true
	case has("YAMAHA") || reYamaha.MatchString(m):
		return famYamaha, "router", true
	case reJuniperS.MatchString(m):
		return famJunos, "fw", true
	case has("NETSCREEN", "SSG"):
		return famSSG, "fw", true
	case reJuniperEX.MatchString(m):
		kind := "l2sw"
		if strings.Contains(m, "QFX") {
			kind = "l3sw"
		}
		return famJunos, kind, true
	case has("JUNIPER"):
		return famJunos, "", true
	case has("ARUBA"):
		return famAruba, "l2sw", true
	case has("COMWARE", "FLEXNETWORK", "H3C"):
		return famComware, "", true
	case has("THUNDER", "A10"):
		return famA10, "", true
	case has("ISR", "ASR"):
		return famCisco, "router", true
	case has("CATALYST", "CISCO", "NEXUS") || reCiscoNum.MatchString(m):
		kind := ""
		if g := reCiscoNum.FindStringSubmatch(m); g != nil {
			kind = ciscoKind(g[3])
		} else if g := reDigits4.FindString(m); g != "" {
			kind = ciscoKind(g)
		}
		return famCisco, kind, true
	}
	return family{}, "", false
}

// ciscoKind tells a Cisco model's kind by its number: 29x1/39xx/43xx/
// 44xx/1100/1800/1900/8xxx are ISR routers; 1000/1300/29xx (but 29x1)/
// 9200 access switches; 35xx–38xx/45xx/65xx/68xx/9300–9500 layer 3 (a 9300
// serves as both; taken as L3). "" for the rest.
func ciscoKind(n string) string {
	if len(n) < 4 {
		return ""
	}
	p := n[:2]
	switch {
	case p == "29" && n[3] == '1':
		return "router"
	case p == "39" || p == "43" || p == "44" || p == "11" || p == "18" || p == "19" || n[0] == '8':
		return "router"
	case p == "29" || p == "10" || p == "13" || p == "92":
		return "l2sw"
	case p == "35" || p == "36" || p == "37" || p == "38" || p == "45" || p == "65" || p == "68" || p == "93" || p == "94" || p == "95":
		return "l3sw"
	}
	return ""
}

// upperFold makes names comparable: width and case folded, spaces kept as
// single separators ("Cisco L2SW" keeps CISCO a word of its own).
func upperFold(s string) string {
	return strings.ToUpper(strings.Join(strings.Fields(norm.NFKC.String(s)), " "))
}

// hasWord finds w in s (both upper-folded) as a word: when w starts with a
// Latin letter, no letter just before it; when it ends with one, none just
// after ("L2" ends in a digit: "L2SW" has it). So "AX" is not found in
// "MAX", nor "WA" in "WAN", nor "RT" in "RTX" — and a Japanese word
// ("ルータ" in "IXルータ") is found wherever it stands.
func hasWord(s, w string) bool {
	if w == "" {
		return false
	}
	for i := 0; ; {
		j := strings.Index(s[i:], w)
		if j < 0 {
			return false
		}
		j += i
		before := j == 0 || !isLetter(w[0]) || !isLetter(s[j-1])
		end := j + len(w)
		after := end >= len(s) || !isLetter(w[len(w)-1]) || !isLetter(s[end])
		if before && after {
			return true
		}
		i = j + 1
	}
}

func isLetter(b byte) bool { return b >= 'A' && b <= 'Z' }

// knownOS is v when it already names one of this PalaTerm's OS types (put
// there by a 置き換え表 before the guess): that OS type's key.
func knownOS(v string, env *Env) (string, bool) {
	t := strings.TrimSpace(v)
	if env == nil || t == "" {
		return "", false
	}
	for _, p := range env.Profiles {
		if strings.EqualFold(t, p.Key) || t == p.Name {
			return p.Key, true
		}
	}
	return "", false
}

// knownSet is v when it already is one of this PalaTerm's command sets —
// a safe one: a set whose commands change a device is never taken by a
// guessing step, not even when the value names it (it is guessed then,
// and so marked, and never written over an existing device's set).
func knownSet(v string, env *Env) (string, bool) {
	t := strings.TrimSpace(v)
	if env == nil || t == "" {
		return "", false
	}
	for _, s := range env.Sets {
		if t == s.Name && s.Safe {
			return s.Name, true
		}
	}
	return "", false
}

// GuessOS picks this PalaTerm's OS type for a model: the line's standard
// one when it is here, else the one OS type whose name or key names the
// line; "" when unsure (the default then fills it in). A value that
// already is an OS type (put there by a 置き換え表 before) is kept.
func GuessOS(v string, env *Env) string {
	if env == nil {
		return v
	}
	if k, ok := knownOS(v, env); ok {
		return k
	}
	fam, _, ok := classify(v)
	if !ok {
		return ""
	}
	for _, p := range env.Profiles {
		if p.Key == fam.key {
			return p.Key
		}
	}
	var hit []string
	for _, p := range env.Profiles {
		n := upperFold(p.Name + " " + p.Key)
		for _, w := range fam.words {
			if hasWord(n, upperFold(w)) {
				hit = append(hit, p.Key)
				break
			}
		}
	}
	if len(hit) == 1 {
		return hit[0]
	}
	return ""
}

// GuessSet picks a command set for a model: one that names the model's
// line and (when known) its kind, and only when exactly one does — and
// never one that changes a device. "" when unsure. A value that already is
// a command set is kept.
func GuessSet(v string, env *Env) string {
	if env == nil {
		return v
	}
	if n, ok := knownSet(v, env); ok {
		return n
	}
	fam, kind, ok := classify(v)
	if !ok {
		return ""
	}
	var hit []string
	for _, s := range env.Sets {
		if !s.Safe {
			continue
		}
		n := upperFold(s.Name)
		famHit := false
		for _, w := range fam.words {
			famHit = famHit || hasWord(n, upperFold(w))
		}
		if !famHit {
			continue
		}
		if kind != "" {
			kindHit := false
			for _, w := range kindWords[kind] {
				kindHit = kindHit || hasWord(n, upperFold(w))
			}
			if !kindHit {
				continue
			}
		}
		hit = append(hit, s.Name)
	}
	if len(hit) == 1 {
		return hit[0]
	}
	return ""
}
