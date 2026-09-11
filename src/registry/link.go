package registry

import (
	"fmt"
	"regexp"
	"strconv"
)

// PCIe link tokens.
//
// `lspci -vv` reports the maximum capability of a link on an LnkCap line and the
// negotiated state on an LnkSta line:
//
//	LnkCap: Port #0, Speed 16GT/s, Width x16, ASPM L1, Exit Latency L0s <64ns
//	LnkSta: Speed 8GT/s, Width x8 (downgraded)
//
// A link that negotiated less than it is capable of is the classic signature of
// a bad riser, a mis-seated card, or a slot wired narrower than the card. That
// difference is what this token reports.
//
// Reading the link state requires access to PCI configuration space, so on a
// non-root run `lspci -vv` omits these lines entirely and this token simply
// finds nothing.
var (
	// linkSpeedCandidate captures a PCIe generation speed such as "16GT/s" or
	// the older "5.0GT/s" spelling.
	linkSpeedCandidate = regexp.MustCompile(`(?i)\b(\d+(?:\.\d+)?)\s*GT/s`)

	// linkWidthCandidate captures a lane width such as "x16".
	linkWidthCandidate = regexp.MustCompile(`(?i)\bwidth\s+x(\d+)`)

	// lnkCapLine finds the capability line within a device stanza.
	lnkCapLine = regexp.MustCompile(`(?im)^\s*LnkCap:.*$`)

	// lnkStaLine finds the negotiated-state line within a device stanza.
	lnkStaLine = regexp.MustCompile(`(?im)^\s*LnkSta:.*$`)
)

// linkSpec is a parsed link speed and width.
type linkSpec struct {
	// speedGT is the transfer rate in GT/s; 0 when unknown.
	speedGT float64
	// width is the lane count; 0 when unknown.
	width int
}

// parseLinkSpec extracts the speed and width from one LnkCap or LnkSta line.
func parseLinkSpec(line string) linkSpec {
	var spec linkSpec

	if m := linkSpeedCandidate.FindStringSubmatch(line); len(m) == 2 {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			spec.speedGT = v
		}
	}
	if m := linkWidthCandidate.FindStringSubmatch(line); len(m) == 2 {
		if v, err := strconv.Atoi(m[1]); err == nil {
			spec.width = v
		}
	}
	return spec
}

// linkDowngrade reports a human-readable description of a degraded PCIe link, or
// an empty string when the link is at full capability or the data is absent.
//
// A downgrade is reported when the negotiated speed or width is lower than the
// capability. Speeds and widths are compared independently, so both a
// speed-only and a width-only degradation are caught.
func linkDowngrade(text string) string {
	capLine := lnkCapLine.FindString(text)
	staLine := lnkStaLine.FindString(text)
	if capLine == "" || staLine == "" {
		return ""
	}

	cap := parseLinkSpec(capLine)
	sta := parseLinkSpec(staLine)

	var reasons []string
	if cap.speedGT > 0 && sta.speedGT > 0 && sta.speedGT < cap.speedGT {
		reasons = append(reasons, fmt.Sprintf("speed %gGT/s of %gGT/s",
			sta.speedGT, cap.speedGT))
	}
	if cap.width > 0 && sta.width > 0 && sta.width < cap.width {
		reasons = append(reasons, fmt.Sprintf("width x%d of x%d",
			sta.width, cap.width))
	}
	if len(reasons) == 0 {
		return ""
	}
	return "link downgraded: " + joinReasons(reasons)
}

// joinReasons renders the degraded dimensions as a single phrase.
func joinReasons(reasons []string) string {
	switch len(reasons) {
	case 0:
		return ""
	case 1:
		return reasons[0]
	default:
		out := reasons[0]
		for _, r := range reasons[1:] {
			out += ", " + r
		}
		return out
	}
}

// matchLinkDowngrade reports the degraded link found in text, if any.
//
// The description is derived from two separate lines rather than a single span
// of text, and a device has exactly one link, so this yields at most one entry.
func matchLinkDowngrade(text string) []string {
	if d := linkDowngrade(text); d != "" {
		return []string{d}
	}
	return nil
}

// init registers the PCIe link token.
func init() {
	Register("link_downgrade", funcMatcher{
		name: "link_downgrade",
		desc: "link_downgrade - PCIe link negotiated below its capability",
		fn:   matchLinkDowngrade,
	})
}
