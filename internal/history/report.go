package history

import (
	"fmt"
	"html"
	"strings"
	"time"
)

// A single-file HTML report of a comparison: the devices in a table, then
// each device's configuration differences side by side with the changed
// part of a line lit, equal runs folded to a few lines of context. CSS is
// inline, there is no script and nothing is fetched, so the file can go
// into a work report or be opened on a machine without PalaTerm.

// ReportDevice is one device of the report.
type ReportDevice struct {
	Name    string `json:"name"`
	LogA    string `json:"logA"`    // contents of the before log
	LogB    string `json:"logB"`    // contents of the after log
	Verdict string `json:"verdict"` // the comparison tab's verdict column, as text
	Check   string `json:"check"`   // the checklist column, as text
	Error   string `json:"error"`
}

// ReportInput is what the comparison tab hands over.
type ReportInput struct {
	Title    string         `json:"title"`
	RunA     string         `json:"runA"`
	RunB     string         `json:"runB"`
	Work     string         `json:"work"`
	Headline string         `json:"headline"`
	Devices  []ReportDevice `json:"devices"`
	Lang     string         `json:"lang"` // "en" for English labels
}

type reportLabels struct {
	title, made, before, after, work, device, diff, config, other, impact, verdict, check, noDiff, noConfig, whole, foldFmt, legendChanged, legendA, legendB, legendMoved, levelHigh, levelMid, levelLow, levelNone, stateOnly, configChanged, noListing, lineA, lineB string
}

func labelsFor(lang string) reportLabels {
	if lang == "en" {
		return reportLabels{"Comparison report", "Made by PalaTerm on", "Before (A)", "After (B)", "Work", "Device", "Result", "Config (+/-/~/moved)", "Other (+/-)", "Impact", "Verdict", "Checklist", "No differences", "No configuration listing on one side: the whole logs were compared", "whole log", "… %d unchanged lines …", "changed", "only in A", "only in B", "moved", "high", "mid", "low", "—", "state only", "configuration changed", "no configuration listing", "A", "B"}
	}
	return reportLabels{"比較レポート", "PalaTerm で作成", "作業前（A）", "作業後（B）", "作業中", "機器名", "差分", "設定（+/-/~/順序）", "その他（+/-）", "影響", "結論", "期待値", "差分なし", "片側に設定の表示が無いため、ログ全体を比べました", "ログ全体", "… %d 行同じ …", "変更", "A にだけ", "B にだけ", "順序", "高", "中", "低", "—", "状態のみ変化", "設定の差分あり", "設定が見当たらない", "A", "B"}
}

// ReportHTML renders the report.
func ReportHTML(in ReportInput) string {
	L := labelsFor(in.Lang)
	e := html.EscapeString
	var sb strings.Builder
	title := in.Title
	if title == "" {
		title = L.title
	}
	sb.WriteString("<!doctype html><html><head><meta charset=\"utf-8\"><title>" + e(title) + "</title><style>")
	sb.WriteString(`body{font-family:"Segoe UI",Meiryo,sans-serif;font-size:13px;color:#1f2937;margin:24px;background:#fff}
h1{font-size:20px;margin:0 0 4px}h2{font-size:16px;margin:28px 0 8px;padding-top:12px;border-top:2px solid #e5e7eb}
.meta{color:#6b7280;font-size:12px;margin-bottom:14px}.head{padding:8px 12px;border:1px solid #d1d5db;border-radius:6px;background:#f9fafb;margin-bottom:14px;font-weight:600}
table{border-collapse:collapse;width:100%}th,td{border:1px solid #e5e7eb;padding:4px 8px;text-align:left;vertical-align:top}th{background:#f3f4f6;font-weight:600}
.mono{font-family:Consolas,"Courier New",monospace;font-size:12px;white-space:pre-wrap;word-break:break-all}
.d{table-layout:fixed}.d td.ln{width:46px;color:#9ca3af;text-align:right;font-family:Consolas,monospace;font-size:11px;background:#fafafa}
.d td.tx{font-family:Consolas,"Courier New",monospace;font-size:12px;white-space:pre-wrap;word-break:break-all}
tr.chg td.tx{background:#fef9c3}tr.del td.a{background:#fee2e2}tr.add td.b{background:#dcfce7}tr.mv td.tx{background:#ede9fe}tr.noise td.tx{color:#9ca3af;font-style:italic}
tr.fold td{color:#6b7280;text-align:center;background:#f9fafb;font-size:11px}
.sd{background:#fca5a5;border-radius:2px}.sa{background:#86efac;border-radius:2px;font-weight:600}
.ph{background:repeating-linear-gradient(135deg,#f3f4f6 0 6px,#fff 6px 12px)}
.badge{display:inline-block;padding:0 6px;border-radius:999px;font-size:11px;font-weight:600}.hi{background:#fee2e2;color:#991b1b}.mi{background:#fef3c7;color:#92400e}.lo{background:#e5e7eb;color:#374151}.ok{background:#dcfce7;color:#166534}.wn{background:#fef3c7;color:#92400e}
.legend{font-size:11px;color:#6b7280;margin:6px 0 10px}.legend i{display:inline-block;width:14px;height:10px;border-radius:2px;vertical-align:middle;margin:0 4px 0 10px}
@media print{body{margin:10mm}h2{page-break-before:auto}}`)
	sb.WriteString("</style></head><body>")
	sb.WriteString("<h1>" + e(title) + "</h1>")
	sb.WriteString("<div class=\"meta\">" + e(L.made) + " " + time.Now().Format("2006-01-02 15:04") + "　" + e(L.before) + ": " + e(in.RunA) + "　" + e(L.after) + ": " + e(in.RunB))
	if in.Work != "" {
		sb.WriteString("　" + e(L.work) + ": " + e(in.Work))
	}
	sb.WriteString("</div>")
	if in.Headline != "" {
		sb.WriteString("<div class=\"head\">" + e(in.Headline) + "</div>")
	}
	type devOut struct {
		dev   ReportDevice
		res   DiffResult
		sum   DiffSummary
		imp   ImpactResult
		state string
	}
	var devs []devOut
	for _, d := range in.Devices {
		o := devOut{dev: d}
		if d.Error == "" {
			opts := DiffOpts{IgnoreNoise: true, IgnoreSecrets: true}
			o.sum = Summarize(d.LogA, d.LogB, opts)
			opts.ConfigOnly = true
			o.res = DiffWith(d.LogA, d.LogB, opts)
			if o.res.Mode == "config" {
				o.imp = Impact(o.res)
			}
			switch {
			case o.sum.ConfigA && o.sum.ConfigB && o.sum.Config.Any():
				o.state = L.configChanged
			case o.sum.ConfigA && o.sum.ConfigB && o.sum.Whole.Any():
				o.state = L.stateOnly
			case o.sum.ConfigA && o.sum.ConfigB:
				o.state = L.noDiff
			case o.sum.Whole.Any():
				o.state = L.noListing
			default:
				o.state = L.noDiff + " (" + L.noListing + ")"
			}
		}
		devs = append(devs, o)
	}
	hasVerdict, hasCheck := false, false
	for _, d := range in.Devices {
		if d.Verdict != "" {
			hasVerdict = true
		}
		if d.Check != "" {
			hasCheck = true
		}
	}
	sb.WriteString("<table><tr><th>" + e(L.device) + "</th><th>" + e(L.diff) + "</th><th>" + e(L.config) + "</th><th>" + e(L.other) + "</th><th>" + e(L.impact) + "</th>")
	if hasVerdict {
		sb.WriteString("<th>" + e(L.verdict) + "</th>")
	}
	if hasCheck {
		sb.WriteString("<th>" + e(L.check) + "</th>")
	}
	sb.WriteString("</tr>")
	lvl := func(imp ImpactResult, has bool) string {
		if !has {
			return "<span class=\"badge lo\">" + e(L.levelNone) + "</span>"
		}
		switch imp.Level {
		case "high":
			return fmt.Sprintf("<span class=\"badge hi\">%s %d</span>", e(L.levelHigh), imp.High)
		case "mid":
			return fmt.Sprintf("<span class=\"badge mi\">%s %d</span>", e(L.levelMid), imp.Mid)
		case "low":
			return fmt.Sprintf("<span class=\"badge lo\">%s %d</span>", e(L.levelLow), imp.Low)
		}
		return "<span class=\"badge lo\">" + e(L.levelNone) + "</span>"
	}
	for _, o := range devs {
		sb.WriteString("<tr><td><b>" + e(o.dev.Name) + "</b></td>")
		if o.dev.Error != "" {
			sb.WriteString("<td colspan=\"4\">" + e(o.dev.Error) + "</td>")
		} else {
			c, w := o.sum.Config, o.sum.Whole
			other := Counts{Added: max(0, w.Added-c.Added), Removed: max(0, w.Removed-c.Removed)}
			if !(o.sum.ConfigA && o.sum.ConfigB) {
				c, other = Counts{}, w
			}
			sb.WriteString("<td>" + e(o.state) + "</td>")
			sb.WriteString(fmt.Sprintf("<td class=\"mono\">+%d / -%d / ~%d / %d</td>", c.Added, c.Removed, c.Changed, c.Moved))
			sb.WriteString(fmt.Sprintf("<td class=\"mono\">+%d / -%d</td>", other.Added, other.Removed))
			sb.WriteString("<td>" + lvl(o.imp, o.res.Mode == "config") + "</td>")
		}
		if hasVerdict {
			sb.WriteString("<td>" + e(o.dev.Verdict) + "</td>")
		}
		if hasCheck {
			sb.WriteString("<td class=\"mono\">" + e(o.dev.Check) + "</td>")
		}
		sb.WriteString("</tr>")
	}
	sb.WriteString("</table>")
	sb.WriteString("<div class=\"legend\"><i style=\"background:#fef9c3\"></i>" + e(L.legendChanged) + "<i style=\"background:#fee2e2\"></i>" + e(L.legendA) + "<i style=\"background:#dcfce7\"></i>" + e(L.legendB) + "<i style=\"background:#ede9fe\"></i>" + e(L.legendMoved) + "</div>")
	for _, o := range devs {
		if o.dev.Error != "" {
			continue
		}
		r := o.res
		changed := r.Added+r.Removed+r.Changed+r.Moved > 0
		sb.WriteString("<h2>" + e(o.dev.Name) + "　<span class=\"badge " + map[bool]string{true: "wn", false: "ok"}[changed] + "\">" + e(o.state) + "</span></h2>")
		if r.Mode != "config" {
			sb.WriteString("<div class=\"meta\">" + e(L.noConfig) + "</div>")
		}
		if !changed {
			sb.WriteString("<div class=\"meta\">" + e(L.noDiff) + "</div>")
			continue
		}
		if len(o.imp.Items) > 0 {
			sb.WriteString("<div class=\"meta\">" + e(L.impact) + ": " + lvl(o.imp, true))
			n := 0
			for _, it := range o.imp.Items {
				if it.Level != "high" {
					continue
				}
				if n == 6 {
					sb.WriteString(" …")
					break
				}
				sb.WriteString("　<span class=\"mono\">" + e(it.Text) + "</span>（" + e(it.Reason) + "）")
				n++
			}
			sb.WriteString("</div>")
		}
		sb.WriteString("<table class=\"d\"><colgroup><col style=\"width:46px\"><col style=\"width:calc(50% - 46px)\"><col style=\"width:46px\"><col style=\"width:calc(50% - 46px)\"></colgroup>")
		sb.WriteString("<tr><th></th><th>" + e(L.lineA) + "</th><th></th><th>" + e(L.lineB) + "</th></tr>")
		const ctx = 3
		keep := make([]bool, len(r.Lines))
		for i, l := range r.Lines {
			if l.Kind != "=" || l.Moved {
				for j := max(0, i-ctx); j <= min(len(r.Lines)-1, i+ctx); j++ {
					keep[j] = true
				}
			}
		}
		seg := func(segs []Seg, text, cls string) string {
			if segs == nil {
				return e(text)
			}
			var b strings.Builder
			for _, s := range segs {
				if s.Kind == "=" {
					b.WriteString(e(s.Text))
				} else {
					b.WriteString("<span class=\"" + cls + "\">" + e(s.Text) + "</span>")
				}
			}
			return b.String()
		}
		skip := 0
		flush := func() {
			if skip > 0 {
				sb.WriteString("<tr class=\"fold\"><td colspan=\"4\">" + e(fmt.Sprintf(L.foldFmt, skip)) + "</td></tr>")
				skip = 0
			}
		}
		for i, l := range r.Lines {
			if !keep[i] {
				skip++
				continue
			}
			flush()
			cls := "same"
			switch {
			case l.Noise:
				cls = "noise"
			case l.Moved:
				cls = "mv"
			case l.Kind == "~":
				cls = "chg"
			case l.Kind == "-":
				cls = "del"
			case l.Kind == "+":
				cls = "add"
			}
			sb.WriteString("<tr class=\"" + cls + "\">")
			if l.Kind == "+" || (l.Noise && l.A == "") {
				sb.WriteString("<td class=\"ln\"></td><td class=\"tx a ph\"></td>")
			} else {
				sb.WriteString(fmt.Sprintf("<td class=\"ln\">%d</td><td class=\"tx a\">%s</td>", l.LineA, seg(l.SegA, l.A, "sd")))
			}
			if l.Kind == "-" || (l.Noise && l.B == "") {
				sb.WriteString("<td class=\"ln\"></td><td class=\"tx b ph\"></td>")
			} else {
				sb.WriteString(fmt.Sprintf("<td class=\"ln\">%d</td><td class=\"tx b\">%s</td>", l.LineB, seg(l.SegB, l.B, "sa")))
			}
			sb.WriteString("</tr>")
		}
		flush()
		sb.WriteString("</table>")
	}
	sb.WriteString("</body></html>")
	return sb.String()
}
