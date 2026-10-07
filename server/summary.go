package main

import (
	"regexp"
	"strings"
)

var (
	mdLink   = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	mdEmph   = regexp.MustCompile("(\\*\\*|__|`)")
	sentence = regexp.MustCompile(`[.!?](\s+|$)`)
	firstURL = regexp.MustCompile(`https?://[^\s)>\]]+[^\s)>\].,;:]`)
)

// reportSummary distills a herdr-projects report into a few sentences: the
// first prose paragraph of its "## Report" section, cut to three sentences.
func reportSummary(report string) string {
	if i := strings.Index(report, "## Report"); i >= 0 {
		report = report[i+len("## Report"):]
	}
	var para []string
	for _, line := range strings.Split(report, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case t == "" && len(para) > 0:
			return trimSentences(strings.Join(para, " "), 3)
		case t == "", strings.HasPrefix(t, "#"), strings.HasPrefix(t, "|"), strings.HasPrefix(t, "```"):
			if strings.HasPrefix(t, "## ") && len(para) > 0 {
				return trimSentences(strings.Join(para, " "), 3)
			}
			continue
		case strings.HasPrefix(t, "- "), strings.HasPrefix(t, "* "):
			if len(para) > 0 {
				return trimSentences(strings.Join(para, " "), 3)
			}
			continue
		}
		para = append(para, t)
	}
	return trimSentences(strings.Join(para, " "), 3)
}

func trimSentences(s string, n int) string {
	s = mdEmph.ReplaceAllString(mdLink.ReplaceAllString(s, "$1"), "")
	locs := sentence.FindAllStringIndex(s, -1)
	if len(locs) > n {
		s = s[:locs[n-1][0]+1]
	}
	if len(s) > 600 {
		s = s[:597] + "…"
	}
	return strings.TrimSpace(s)
}
