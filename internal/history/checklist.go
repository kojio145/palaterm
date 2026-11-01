package history

import (
	"fmt"
	"strings"
)

// Expected lines (a checklist) against an after log: each line of the
// list is looked up the way a typed setting is looked up by the work
// verdict (abbreviations, encrypted secrets, merged VLAN lists, the block
// it must be in), so the list can be written as the device prints its
// configuration. A line that is there is PASS; one whose setting exists
// with another value is FAIL (the value found is shown); one that is not
// there at all is MISSING; one the log cannot tell is "?".

// CheckLine is one expected line (see model.CheckLine; kept apart so this
// package does not import the vault's model).
type CheckLine struct {
	Text  string `json:"text"`
	Block string `json:"block,omitempty"`
}

// CheckResult is the fate of one expected line.
type CheckResult struct {
	Text   string `json:"text"`
	Block  string `json:"block,omitempty"`
	Status string `json:"status"` // pass / fail / missing / unverified / skip
	Found  string `json:"found,omitempty"`
	Note   string `json:"note,omitempty"`
}

// ChecklistResult is the outcome for one device.
type ChecklistResult struct {
	Results    []CheckResult `json:"results"`
	Pass       int           `json:"pass"`
	Fail       int           `json:"fail"`
	Missing    int           `json:"missing"`
	Unverified int           `json:"unverified"`
	NoConfig   bool          `json:"noConfig,omitempty"`
}

// VerifyChecklist looks every expected line up in the after log.
func VerifyChecklist(lines []CheckLine, afterLog string) ChecklistResult {
	res := ChecklistResult{Results: []CheckResult{}}
	after := indexConfig(afterLog)
	if !after.config {
		res.NoConfig = true
		for _, l := range lines {
			res.Results = append(res.Results, CheckResult{Text: l.Text, Block: l.Block, Status: "skip", Note: "noConfig"})
		}
		return res
	}
	cfg := configLines(afterLog)
	for _, l := range lines {
		text := strings.TrimSpace(l.Text)
		if text == "" {
			continue
		}
		cr := CheckResult{Text: text, Block: strings.TrimSpace(l.Block)}
		// A block the log does not list cannot hold the line, and the same
		// text in some other block says nothing: "?" rather than PASS
		// (review 2026-11-01, A-8).
		if cr.Block != "" {
			if _, b := after.block(cr.Block); b == nil {
				cr.Status, cr.Note = "unverified", "noBlock"
				res.Unverified++
				res.Results = append(res.Results, cr)
				continue
			}
		}
		// The line as if it had been typed during the work: the verdict's
		// matcher then does the looking up. A FortiOS block ("config system
		// ntp / config ntpserver / edit 1") is typed the way FortiOS answers
		// it, so the nested entry is the line's context.
		sr := StrictVerifyLogs([]string{checkTranscript(text, cr.Block)}, "", afterLog)
		var cc *CommandCheck
		want := normKey(text)
		for i := range sr.Commands {
			if normKey(sr.Commands[i].Line) == want {
				cc = &sr.Commands[i]
			}
		}
		if cc == nil && len(sr.Commands) > 0 {
			cc = &sr.Commands[len(sr.Commands)-1]
		}
		switch {
		case cc == nil:
			cr.Status = "unverified"
			cr.Note = "notSetting"
		case cc.Status == "reflected" || cc.Status == "already":
			cr.Status = "pass"
			cr.Found = cc.Match
			cr.Note = cc.Note
		case cc.Status == "unverified":
			cr.Status = "unverified"
			cr.Note = cc.Note
		default:
			// Not found as typed: the same setting with another value is a
			// FAIL, nothing of it a MISSING.
			cr.Status = "missing"
			if found := sameSettingLine(cfg, text, cr.Block); found != "" {
				cr.Status = "fail"
				cr.Found = found
			}
		}
		switch cr.Status {
		case "pass":
			res.Pass++
		case "fail":
			res.Fail++
		case "missing":
			res.Missing++
		case "unverified":
			res.Unverified++
		}
		res.Results = append(res.Results, cr)
	}
	return res
}

// checkTranscript writes an expected line as a work log that typed it:
// Cisco-style prompts, or FortiOS's for a "config … / edit …" block.
func checkTranscript(text, block string) string {
	var sb strings.Builder
	if strings.HasPrefix(strings.ToLower(block), "config ") {
		// FortiOS answers each head with its last word as the prompt's
		// context ("config system ntp" → "chk (ntp) #").
		prompt := "chk # "
		for _, part := range strings.Split(block, nestSep) {
			part = strings.TrimSpace(part)
			sb.WriteString(prompt + part + "\n")
			f := strings.Fields(normKey(part))
			prompt = "chk (" + f[len(f)-1] + ") # "
		}
		sb.WriteString(prompt + text + "\n" + prompt + "end\nchk # \n")
		return sb.String()
	}
	sb.WriteString("chk#configure terminal\n")
	if block != "" {
		sb.WriteString("chk(config)#" + block + "\n")
		sb.WriteString("chk(config-sub)#" + text + "\n")
	} else {
		sb.WriteString("chk(config)#" + text + "\n")
	}
	sb.WriteString("chk(config)#end\nchk#\n")
	return sb.String()
}

// sameSettingLine finds a line of the listing that makes the same setting
// as text (same leading words, another value), in the same block when one
// is named: the block itself or a block nested in it ("interface
// gigabitethernet0/1" is not "…0/10").
func sameSettingLine(cfg []cfgLine, text, block string) string {
	key := leafKey2(normKey(text))
	if key == "" {
		return ""
	}
	bk := normKey(block)
	for _, l := range cfg {
		if bk != "" && l.block != bk && l.nest != bk && !strings.HasPrefix(l.nest, bk+nestSep) {
			continue
		}
		if bk == "" && l.block != "" {
			continue
		}
		if leafKey2(l.key) == key && l.key != normKey(text) {
			return l.orig
		}
	}
	return ""
}

// ChecklistTemplate turns a few values into the lines a family of device
// prints for a common setting (hostname, NTP, DNS, syslog, SNMP community,
// default gateway), so a checklist can be started without copying from a
// device. Unknown kinds give nothing.
func ChecklistTemplate(family, kind string, values []string) []CheckLine {
	var vals []string
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			vals = append(vals, v)
		}
	}
	if len(vals) == 0 {
		return nil
	}
	var out []CheckLine
	add := func(text, block string) { out = append(out, CheckLine{Text: text, Block: block}) }
	q := func(s string) string { return `"` + s + `"` }
	switch family {
	case "fortigate":
		switch kind {
		case "hostname":
			add("set hostname "+q(vals[0]), "config system global")
		case "ntp":
			for i, v := range vals {
				add("set server "+q(v), fmt.Sprintf("config system ntp / config ntpserver / edit %d", i+1))
			}
		case "dns":
			add("set primary "+vals[0], "config system dns")
			if len(vals) > 1 {
				add("set secondary "+vals[1], "config system dns")
			}
		case "syslog":
			add("set server "+q(vals[0]), "config log syslogd setting")
		case "snmp":
			add("set name "+q(vals[0]), "config system snmp community")
		case "gateway":
			add("set gateway "+vals[0], "config router static")
		}
	case "junos":
		switch kind {
		case "hostname":
			add("set system host-name "+vals[0], "")
		case "ntp":
			for _, v := range vals {
				add("set system ntp server "+v, "")
			}
		case "dns":
			for _, v := range vals {
				add("set system name-server "+v, "")
			}
		case "syslog":
			for _, v := range vals {
				add("set system syslog host "+v+" any any", "")
			}
		case "snmp":
			add("set snmp community "+vals[0]+" authorization read-only", "")
		case "gateway":
			add("set routing-options static route 0.0.0.0/0 next-hop "+vals[0], "")
		}
	case "yamaha":
		switch kind {
		case "hostname":
			add("hostname "+vals[0], "")
		case "ntp":
			for _, v := range vals {
				add("ntpdate "+v, "")
			}
		case "dns":
			add("dns server "+strings.Join(vals, " "), "")
		case "syslog":
			add("syslog host "+vals[0], "")
		case "snmp":
			add("snmp community read-only "+vals[0], "")
		case "gateway":
			add("ip route default gateway "+vals[0], "")
		}
	default: // cisco and IOS-like
		switch kind {
		case "hostname":
			add("hostname "+vals[0], "")
		case "ntp":
			for _, v := range vals {
				add("ntp server "+v, "")
			}
		case "dns":
			add("ip name-server "+strings.Join(vals, " "), "")
		case "syslog":
			for _, v := range vals {
				add("logging host "+v, "")
			}
		case "snmp":
			add("snmp-server community "+vals[0]+" RO", "")
		case "gateway":
			add("ip route 0.0.0.0 0.0.0.0 "+vals[0], "")
		}
	}
	return out
}

// ParseChecklist reads a checklist written like a configuration excerpt:
// a line at the left margin is a global line, or the block of the indented
// lines under it; blank lines and comments ("!" / "#") are skipped.
func ParseChecklist(text string) []CheckLine {
	var out []CheckLine
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	block := ""
	for i, raw := range lines {
		t := strings.TrimSpace(raw)
		if t == "" || strings.HasPrefix(t, "!") || strings.HasPrefix(t, "#") {
			continue
		}
		indented := raw != strings.TrimLeft(raw, " \t")
		if !indented {
			// A margin line followed by indented lines is their block, not
			// a line to check by itself.
			next := ""
			for j := i + 1; j < len(lines); j++ {
				if strings.TrimSpace(lines[j]) != "" {
					next = lines[j]
					break
				}
			}
			if next != "" && next != strings.TrimLeft(next, " \t") {
				block = t
				continue
			}
			block = ""
			out = append(out, CheckLine{Text: t})
			continue
		}
		out = append(out, CheckLine{Text: t, Block: block})
	}
	return out
}

// FormatChecklist is the inverse of ParseChecklist.
func FormatChecklist(lines []CheckLine) string {
	var sb strings.Builder
	block := ""
	for _, l := range lines {
		if l.Block != "" {
			if l.Block != block {
				sb.WriteString(l.Block + "\n")
				block = l.Block
			}
			sb.WriteString(" " + l.Text + "\n")
			continue
		}
		block = ""
		sb.WriteString(l.Text + "\n")
	}
	return sb.String()
}
