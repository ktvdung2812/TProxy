package rtk

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	reGitDiff       = regexp.MustCompile(`(?m)^diff --git `)
	reGitDiffHunk   = regexp.MustCompile(`(?m)^@@ `)
	reGitStatus     = regexp.MustCompile(`(?m)^On branch |^nothing to commit|^Changes (not |to be )|^Untracked files:`)
	reGitLog        = regexp.MustCompile(`(?m)^[*|/\\ ]*commit [0-9a-f]{7,40}$`)
	rePorcelain     = regexp.MustCompile(`(?m)^[ MADRCU?!][ MADRCU?!] \S`)
	reBuildOutput   = regexp.MustCompile(`(?im)^(npm (warn|error|ERR!)|yarn (warn|error)|\s*Compiling\s+\S+|\s*Downloading\s+\S+|added \d+ package|\[ERROR\]|BUILD (SUCCESS|FAILED)|\s*Finished\s+|Successfully (installed|built)|ERROR:)`)
	reTreeGlyph     = regexp.MustCompile(`[├└]──|│  `)
	reLSRow         = regexp.MustCompile(`(?m)^[-dlbcps][rwx-]{9}`)
	reLSTotal       = regexp.MustCompile(`(?m)^total \d+$`)
	readNumberedRe  = regexp.MustCompile(`^\s*\d+\|`)
)

var filterRegistry = map[string]namedFilter{
	"git-diff":        {name: "git-diff", fn: filterGitDiff},
	"git-status":      {name: "git-status", fn: filterGitStatus},
	"git-log":         {name: "git-log", fn: filterGitLog},
	"build-output":    {name: "build-output", fn: filterBuildOutput},
	"grep":            {name: "grep", fn: filterGrep},
	"find":            {name: "find", fn: filterFind},
	"tree":            {name: "tree", fn: filterTree},
	"ls":              {name: "ls", fn: filterLS},
	"search-list":     {name: "search-list", fn: filterSearchList},
	"read-numbered":   {name: "read-numbered", fn: filterReadNumbered},
	"dedup-log":       {name: "dedup-log", fn: filterDedupLog},
	"smart-truncate":  {name: "smart-truncate", fn: filterSmartTruncate},
}

func autoDetectFilter(text string) namedFilter {
	head := text
	if len(head) > detectWindow {
		head = head[:detectWindow]
	}
	if reGitLog.MatchString(head) {
		return filterRegistry["git-log"]
	}
	if reGitDiff.MatchString(head) || reGitDiffHunk.MatchString(head) {
		return filterRegistry["git-diff"]
	}
	if reGitStatus.MatchString(head) {
		return filterRegistry["git-status"]
	}
	if reBuildOutput.MatchString(head) {
		return filterRegistry["build-output"]
	}
	if isMostlyPorcelain(head) {
		return filterRegistry["git-status"]
	}
	lines := strings.Split(head, "\n")
	nonEmpty := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			nonEmpty = append(nonEmpty, line)
		}
	}
	if len(nonEmpty) >= 1 {
		limit := 5
		if len(nonEmpty) < limit {
			limit = len(nonEmpty)
		}
		for _, line := range nonEmpty[:limit] {
			if isGrepLine(line) {
				return filterRegistry["grep"]
			}
		}
	}
	if len(nonEmpty) >= 3 {
		allPathLike := true
		for _, line := range nonEmpty {
			if !isPathLike(line) {
				allPathLike = false
				break
			}
		}
		if allPathLike {
			return filterRegistry["find"]
		}
	}
	if reTreeGlyph.MatchString(head) {
		return filterRegistry["tree"]
	}
	if reLSTotal.MatchString(head) || countMatches(head, reLSRow) >= 3 {
		return filterRegistry["ls"]
	}
	if searchListHeaderRe.MatchString(head) {
		return filterRegistry["search-list"]
	}
	if len(lines) >= smartTruncateMinLines && isLineNumbered(lines) {
		return filterRegistry["read-numbered"]
	}
	if len(nonEmpty) >= 5 {
		return filterRegistry["dedup-log"]
	}
	if len(lines) >= smartTruncateMinLines {
		return filterRegistry["smart-truncate"]
	}
	return namedFilter{}
}

func safeApply(filter namedFilter, text string) string {
	if filter.fn == nil {
		return text
	}
	defer func() {
		_ = recover()
	}()
	out := filter.fn(text)
	if out == "" {
		return text
	}
	return out
}

func isGrepLine(line string) bool {
	first := strings.Index(line, ":")
	if first == -1 {
		return false
	}
	second := strings.Index(line[first+1:], ":")
	if second == -1 {
		return false
	}
	lineno := line[first+1 : first+1+second]
	_, err := strconv.Atoi(lineno)
	return err == nil
}

func isPathLike(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	if len(t) >= 3 && t[1] == ':' && ((t[2] == '\\') || t[2] == '/') {
		return true
	}
	if strings.Contains(t, ":") {
		return false
	}
	return strings.HasPrefix(t, ".") || strings.HasPrefix(t, "/") || strings.Contains(t, "/")
}

func isMostlyPorcelain(head string) bool {
	lines := strings.Split(head, "\n")
	nonEmpty := 0
	hits := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		nonEmpty++
		if rePorcelain.MatchString(line) {
			hits++
		}
	}
	if nonEmpty < 3 {
		return false
	}
	return float64(hits)/float64(nonEmpty) >= 0.6
}

func isLineNumbered(lines []string) bool {
	hits, nonEmpty := 0, 0
	limit := 100
	if len(lines) < limit {
		limit = len(lines)
	}
	for _, line := range lines[:limit] {
		if line == "" {
			continue
		}
		nonEmpty++
		if readNumberedRe.MatchString(line) {
			hits++
		}
	}
	if nonEmpty < 5 {
		return false
	}
	return float64(hits)/float64(nonEmpty) >= readNumberedMinHitRatio
}

func countMatches(text string, re *regexp.Regexp) int {
	return len(re.FindAllString(text, -1))
}
