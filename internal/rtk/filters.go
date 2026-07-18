package rtk

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type filterFunc func(string) string

type namedFilter struct {
	name string
	fn   filterFunc
}

func (f namedFilter) apply(text string) string { return f.fn(text) }

var lsNoiseDirs = map[string]struct{}{
	"node_modules": {}, ".git": {}, "target": {}, "__pycache__": {},
	".next": {}, "dist": {}, "build": {}, ".cache": {}, ".turbo": {},
	".vercel": {}, ".pytest_cache": {}, ".mypy_cache": {}, ".tox": {},
	".venv": {}, "venv": {}, "env": {}, "coverage": {}, ".nyc_output": {},
	".DS_Store": {}, "Thumbs.db": {}, ".idea": {}, ".vscode": {}, ".vs": {},
}

func filterGitDiff(diff string) string {
	result := []string{}
	currentFile := ""
	added, removed := 0, 0
	inHunk := false
	hunkShown, hunkSkipped := 0, 0
	wasTruncated := false
	maxLines := 500

	flushHunk := func() {
		if hunkSkipped > 0 {
			result = append(result, fmt.Sprintf("  ... (%d lines truncated)", hunkSkipped))
			wasTruncated = true
			hunkSkipped = 0
		}
	}

	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "diff --git") {
			flushHunk()
			if currentFile != "" && (added > 0 || removed > 0) {
				result = append(result, fmt.Sprintf("  +%d -%d", added, removed))
			}
			currentFile = "unknown"
			if parts := strings.Split(line, " b/"); len(parts) > 1 {
				currentFile = strings.Join(parts[1:], " b/")
			}
			result = append(result, "\n"+currentFile)
			added, removed = 0, 0
			inHunk, hunkShown = false, 0
		} else if strings.HasPrefix(line, "@@") {
			flushHunk()
			inHunk = true
			hunkShown = 0
			result = append(result, "  "+line)
		} else if inHunk {
			switch {
			case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
				added++
				if hunkShown < gitDiffHunkMaxLines {
					result = append(result, "  "+line)
					hunkShown++
				} else {
					hunkSkipped++
				}
			case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
				removed++
				if hunkShown < gitDiffHunkMaxLines {
					result = append(result, "  "+line)
					hunkShown++
				} else {
					hunkSkipped++
				}
			case hunkShown < gitDiffHunkMaxLines && !strings.HasPrefix(line, "\\"):
				if hunkShown > 0 {
					result = append(result, "  "+line)
					hunkShown++
				}
			}
		}
		if len(result) >= maxLines {
			result = append(result, "\n... (more changes truncated)")
			wasTruncated = true
			break
		}
	}
	flushHunk()
	if currentFile != "" && (added > 0 || removed > 0) {
		result = append(result, fmt.Sprintf("  +%d -%d", added, removed))
	}
	if wasTruncated {
		result = append(result, "[full diff: rtk git diff --no-compact]")
	}
	return strings.Join(result, "\n")
}

func filterGitStatus(input string) string {
	lines := strings.Split(input, "\n")
	if len(lines) == 0 || (len(lines) == 1 && strings.TrimSpace(lines[0]) == "") {
		return "Clean working tree"
	}
	branch := ""
	stagedFiles, modifiedFiles, untrackedFiles := []string{}, []string{}, []string{}
	staged, modified, untracked, conflicts := 0, 0, 0, 0
	longBranchRe := regexp.MustCompile(`^On branch (\S+)`)
	longMatchRe := regexp.MustCompile(`^\s*(modified|new file|deleted|renamed|both modified):\s+(.+)$`)
	porcelainRe := regexp.MustCompile(`^[ MADRCU?!][ MADRCU?!] `)

	for _, raw := range lines {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		if m := longBranchRe.FindStringSubmatch(raw); len(m) > 1 {
			branch = m[1]
			continue
		}
		if strings.HasPrefix(raw, "##") {
			branch = strings.TrimSpace(strings.TrimPrefix(raw, "##"))
			continue
		}
		if len(raw) >= 3 && porcelainRe.MatchString(raw) {
			x, y, file := raw[0], raw[1], raw[3:]
			if raw[:2] == "??" {
				untracked++
				untrackedFiles = append(untrackedFiles, file)
				continue
			}
			if strings.ContainsAny(string(x), "MADRC") {
				staged++
				stagedFiles = append(stagedFiles, file)
			} else if x == 'U' {
				conflicts++
			}
			if y == 'M' || y == 'D' {
				modified++
				modifiedFiles = append(modifiedFiles, file)
			}
			continue
		}
		if m := longMatchRe.FindStringSubmatch(raw); len(m) > 2 {
			kind, path := m[1], strings.TrimSpace(m[2])
			switch kind {
			case "both modified":
				conflicts++
			case "modified", "deleted":
				modified++
				modifiedFiles = append(modifiedFiles, path)
			case "new file", "renamed":
				staged++
				stagedFiles = append(stagedFiles, path)
			}
		}
	}

	var out strings.Builder
	if branch != "" {
		out.WriteString("* " + branch + "\n")
	}
	appendFiles := func(label string, count int, files []string, max int) {
		if count <= 0 {
			return
		}
		fmt.Fprintf(&out, "%s %d files\n", label, count)
		limit := len(files)
		if limit > max {
			limit = max
		}
		for _, f := range files[:limit] {
			fmt.Fprintf(&out, "   %s\n", f)
		}
		if len(files) > max {
			fmt.Fprintf(&out, "   ... +%d more\n", len(files)-max)
		}
	}
	appendFiles("+ Staged:", staged, stagedFiles, statusMaxFiles)
	appendFiles("~ Modified:", modified, modifiedFiles, statusMaxFiles)
	appendFiles("? Untracked:", untracked, untrackedFiles, statusMaxUntracked)
	if conflicts > 0 {
		fmt.Fprintf(&out, "conflicts: %d files\n", conflicts)
	}
	if staged == 0 && modified == 0 && untracked == 0 && conflicts == 0 {
		out.WriteString("clean — nothing to commit\n")
	}
	return strings.TrimRight(out.String(), "\n")
}

func filterGitLog(text string) string {
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	out := []string{}
	skipped := 0
	inCommit, subjectSeen := false, false
	commitRe := regexp.MustCompile(`(?i)^commit [0-9a-f]{7,40}$`)
	graphCommitRe := regexp.MustCompile(`(?i)^[*|/\\ ]+commit [0-9a-f]{7,40}`)
	metaRe := regexp.MustCompile(`(?i)^[*|/\\ ]*(Author|Date):`)
	subjectRe := regexp.MustCompile(`^[*|/\\ ]*    \S`)
	onelineRe := regexp.MustCompile(`(?i)^[0-9a-f]{7,40}\s+`)
	graphOnelineRe := regexp.MustCompile(`(?i)^[*|/\\ ]+([0-9a-f]{7,40}\s+.+)`)

	push := func(line string) {
		if len(out) < gitLogMaxLines {
			out = append(out, line)
			return
		}
		skipped++
	}

	for _, raw := range lines {
		line := strings.TrimRight(raw, " \t")
		trimmed := strings.TrimSpace(line)
		if commitRe.MatchString(trimmed) || graphCommitRe.MatchString(trimmed) {
			inCommit, subjectSeen = true, false
			push(line)
			continue
		}
		if inCommit {
			switch {
			case metaRe.MatchString(trimmed):
				push(trimmed)
			case trimmed == "":
			case !subjectSeen && subjectRe.MatchString(line):
				push("  Subject: " + trimmed)
				subjectSeen = true
			case regexp.MustCompile(`^\d+ file\w* changed`).MatchString(trimmed):
				push("  " + trimmed)
			case strings.HasPrefix(trimmed, "diff --git "):
				push("  ... diff body omitted")
			}
			continue
		}
		if m := graphOnelineRe.FindStringSubmatch(trimmed); len(m) > 1 {
			push(m[1])
			continue
		}
		if onelineRe.MatchString(trimmed) {
			push(trimmed)
			continue
		}
		if regexp.MustCompile(`^[*|/\\ ]+$`).MatchString(trimmed) && strings.ContainsAny(trimmed, "*|/\\") {
			continue
		}
		push(trimmed)
	}
	if skipped > 0 {
		out = append(out, fmt.Sprintf("... (%d more lines)", skipped))
	}
	result := strings.Join(out, "\n")
	if result == "" && text != "" {
		return text
	}
	if len(result) > len(text) {
		return text
	}
	return result
}

func filterBuildOutput(input string) string {
	lines := strings.Split(input, "\n")
	if len(lines) == 0 {
		return input
	}
	errors, warnings, deprecations := []string{}, []string{}, []string{}
	var summary *string
	compilingCount, downloadingCount := 0, 0
	inCargoError := false
	cargoContRe := regexp.MustCompile(`^\s*(-->|\||\d+\s*\||=)`)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if inCargoError {
			if trimmed == "" {
				inCargoError = false
				continue
			}
			if cargoContRe.MatchString(line) {
				errors = append(errors, line)
				continue
			}
			inCargoError = false
		}
		if trimmed == "" {
			continue
		}
		switch {
		case regexp.MustCompile(`(?i)^npm (ERR!|error)`).MatchString(trimmed), regexp.MustCompile(`(?i)^yarn error`).MatchString(trimmed):
			errors = append(errors, line)
		case regexp.MustCompile(`(?i)^npm warn deprecated`).MatchString(trimmed):
			deprecations = append(deprecations, line)
		case regexp.MustCompile(`(?i)^npm warn`).MatchString(trimmed), regexp.MustCompile(`(?i)^yarn warn`).MatchString(trimmed):
			warnings = append(warnings, line)
		case regexp.MustCompile(`(?i)^error(\[|:)`).MatchString(trimmed), strings.HasPrefix(trimmed, "error -->"):
			errors = append(errors, line)
			inCargoError = true
		case regexp.MustCompile(`(?i)^warning(\[|:)`).MatchString(trimmed), strings.HasPrefix(trimmed, "warning -->"):
			warnings = append(warnings, line)
			inCargoError = true
		case regexp.MustCompile(`(?i)^ERROR:`).MatchString(trimmed), regexp.MustCompile(`(?i)^\[ERROR\]`).MatchString(trimmed), regexp.MustCompile(`(?i)^BUILD FAILED`).MatchString(trimmed):
			errors = append(errors, line)
		case regexp.MustCompile(`(?i)^\[WARNING\]`).MatchString(trimmed):
			warnings = append(warnings, line)
		case regexp.MustCompile(`(?i)^\s*Compiling\s+\S+`).MatchString(trimmed):
			compilingCount++
		case regexp.MustCompile(`(?i)^\s*Downloading\s+\S+`).MatchString(trimmed), regexp.MustCompile(`(?i)^Fetching\s+`).MatchString(trimmed):
			downloadingCount++
		case regexp.MustCompile(`(?i)^(added|removed|changed|audited|installed)\s+\d+\s+package`).MatchString(trimmed),
			regexp.MustCompile(`(?i)^\s*Finished\s+`).MatchString(trimmed),
			regexp.MustCompile(`(?i)^BUILD SUCCESS`).MatchString(trimmed),
			regexp.MustCompile(`(?i)^\d+\s+(vulnerabilities|packages?|warnings?|errors?)`).MatchString(trimmed),
			regexp.MustCompile(`(?i)^Successfully (installed|built)`).MatchString(trimmed),
			regexp.MustCompile(`(?i)^To address .* issues`).MatchString(trimmed),
			regexp.MustCompile(`(?i)^Run ` + "`npm (audit|fund)`").MatchString(trimmed),
			strings.Contains(trimmed, "packages are looking for funding"):
			if summary == nil {
				summary = &line
			} else {
				joined := *summary + "\n" + line
				summary = &joined
			}
		}
	}

	var out strings.Builder
	for i, d := range deprecations {
		if i >= 3 {
			fmt.Fprintf(&out, "... +%d more deprecated packages\n", len(deprecations)-3)
			break
		}
		out.WriteString(d + "\n")
	}
	if compilingCount > 0 {
		fmt.Fprintf(&out, "Compiled %d packages\n", compilingCount)
	}
	if downloadingCount > 0 {
		fmt.Fprintf(&out, "Downloaded %d packages\n", downloadingCount)
	}
	for _, e := range errors {
		out.WriteString(e + "\n")
	}
	for i, w := range warnings {
		if i >= 5 {
			fmt.Fprintf(&out, "... +%d more warnings\n", len(warnings)-5)
			break
		}
		out.WriteString(w + "\n")
	}
	if summary != nil {
		out.WriteString(*summary + "\n")
	}
	result := strings.TrimRight(out.String(), "\n")
	if result == "" {
		return input
	}
	return result
}

func filterGrep(input string) string {
	type match struct {
		lineNum string
		content string
	}
	byFile := map[string][]match{}
	total := 0
	for _, line := range strings.Split(input, "\n") {
		first := strings.Index(line, ":")
		if first == -1 {
			continue
		}
		second := strings.Index(line[first+1:], ":")
		if second == -1 {
			continue
		}
		second += first + 1
		file := line[:first]
		lineNum := line[first+1 : second]
		content := line[second+1:]
		if _, err := strconv.Atoi(lineNum); err != nil {
			continue
		}
		total++
		byFile[file] = append(byFile[file], match{lineNum: lineNum, content: content})
	}
	if total == 0 {
		return input
	}
	files := make([]string, 0, len(byFile))
	for file := range byFile {
		files = append(files, file)
	}
	sort.Strings(files)
	var out strings.Builder
	fmt.Fprintf(&out, "%d matches in %dF:\n\n", total, len(files))
	for _, file := range files {
		matches := byFile[file]
		fmt.Fprintf(&out, "[file] %s (%d):\n", file, len(matches))
		limit := len(matches)
		if limit > grepPerFileMax {
			limit = grepPerFileMax
		}
		for _, m := range matches[:limit] {
			fmt.Fprintf(&out, "  %4s: %s\n", m.lineNum, strings.TrimSpace(m.content))
		}
		if len(matches) > grepPerFileMax {
			fmt.Fprintf(&out, "  +%d\n", len(matches)-grepPerFileMax)
		}
		out.WriteString("\n")
	}
	return strings.TrimRight(out.String(), "\n")
}

func filterFind(input string) string {
	lines := []string{}
	for _, line := range strings.Split(input, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return input
	}
	byDir := map[string][]string{}
	for _, path := range lines {
		lastSep := strings.LastIndexAny(path, `/\`)
		dir, basename := ".", path
		if lastSep != -1 {
			dir = path[:lastSep]
			if dir == "" {
				dir = "/"
			}
			basename = path[lastSep+1:]
		}
		byDir[dir] = append(byDir[dir], basename)
	}
	dirs := make([]string, 0, len(byDir))
	for dir := range byDir {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	var out strings.Builder
	fmt.Fprintf(&out, "%d files in %d dirs:\n\n", len(lines), len(dirs))
	limitDirs := len(dirs)
	if limitDirs > findTotalDirMax {
		limitDirs = findTotalDirMax
	}
	for _, dir := range dirs[:limitDirs] {
		files := byDir[dir]
		fmt.Fprintf(&out, "%s/  (%d)\n", strings.ReplaceAll(dir, `\`, "/"), len(files))
		show := len(files)
		if show > findPerDirMax {
			show = findPerDirMax
		}
		for _, f := range files[:show] {
			fmt.Fprintf(&out, "  %s\n", f)
		}
		if len(files) > findPerDirMax {
			fmt.Fprintf(&out, "  +%d\n", len(files)-findPerDirMax)
		}
	}
	if len(dirs) > findTotalDirMax {
		fmt.Fprintf(&out, "\n+%d more dirs\n", len(dirs)-findTotalDirMax)
	}
	return strings.TrimRight(out.String(), "\n")
}

func filterDedupLog(input string) string {
	lines := strings.Split(input, "\n")
	out := []string{}
	var prev *string
	runCount, blankStreak := 0, 0
	flush := func() {
		if prev != nil && runCount > 1 {
			out = append(out, fmt.Sprintf("  ... (%d duplicate lines)", runCount-1))
		}
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			if blankStreak < 1 {
				out = append(out, line)
			}
			blankStreak++
			flush()
			prev, runCount = nil, 0
			continue
		}
		blankStreak = 0
		if prev != nil && line == *prev {
			runCount++
			continue
		}
		flush()
		out = append(out, line)
		prev = &out[len(out)-1]
		runCount = 1
		if len(out) >= dedupLineMax {
			out = append(out, fmt.Sprintf("... (truncated at %d lines)", dedupLineMax))
			return strings.Join(out, "\n")
		}
	}
	flush()
	return strings.Join(out, "\n")
}

func filterSmartTruncate(input string) string {
	lines := strings.Split(input, "\n")
	if len(lines) < smartTruncateMinLines {
		return input
	}
	head := lines[:smartTruncateHead]
	tail := lines[len(lines)-smartTruncateTail:]
	cut := len(lines) - len(head) - len(tail)
	parts := append([]string{}, head...)
	parts = append(parts, fmt.Sprintf("... +%d lines truncated", cut))
	parts = append(parts, tail...)
	return strings.Join(parts, "\n")
}

func filterReadNumbered(input string) string {
	lines := strings.Split(input, "\n")
	if len(lines) < smartTruncateMinLines {
		return input
	}
	head := lines[:smartTruncateHead]
	tail := lines[len(lines)-smartTruncateTail:]
	cut := len(lines) - len(head) - len(tail)
	parts := append([]string{}, head...)
	parts = append(parts, fmt.Sprintf("... +%d lines truncated (file continues)", cut))
	parts = append(parts, tail...)
	return strings.Join(parts, "\n")
}

func filterTree(input string) string {
	lines := strings.Split(input, "\n")
	if len(lines) == 0 {
		return input
	}
	filtered := []string{}
	for _, line := range lines {
		if strings.Contains(line, "director") && strings.Contains(line, "file") {
			continue
		}
		if strings.TrimSpace(line) == "" && len(filtered) == 0 {
			continue
		}
		filtered = append(filtered, line)
	}
	for len(filtered) > 0 && strings.TrimSpace(filtered[len(filtered)-1]) == "" {
		filtered = filtered[:len(filtered)-1]
	}
	if len(filtered) > treeMaxLines {
		cut := len(filtered) - treeMaxLines
		return strings.Join(filtered[:treeMaxLines], "\n") + fmt.Sprintf("\n... +%d more lines", cut)
	}
	return strings.Join(filtered, "\n")
}

var lsDateRe = regexp.MustCompile(`\s+(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\s+\d{1,2}\s+(\d{4}|\d{2}:\d{2})\s+`)

func humanSize(bytes int) string {
	switch {
	case bytes >= 1_048_576:
		return fmt.Sprintf("%.1fM", float64(bytes)/1_048_576)
	case bytes >= 1024:
		return fmt.Sprintf("%.1fK", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}

func filterLS(input string) string {
	dirs := []string{}
	files := [][2]string{}
	byExt := map[string]int{}
	for _, line := range strings.Split(input, "\n") {
		if strings.HasPrefix(line, "total ") || line == "" {
			continue
		}
		m := lsDateRe.FindStringIndex(line)
		if m == nil {
			continue
		}
		name := line[m[1]:]
		before := strings.Fields(line[:m[0]])
		if len(before) < 4 {
			continue
		}
		perms := before[0]
		size := 0
		for i := len(before) - 1; i >= 0; i-- {
			if n, err := strconv.Atoi(before[i]); err == nil {
				size = n
				break
			}
		}
		if name == "." || name == ".." {
			continue
		}
		if _, skip := lsNoiseDirs[name]; skip {
			continue
		}
		switch perms[0] {
		case 'd':
			dirs = append(dirs, name)
		case '-', 'l':
			dot := strings.LastIndex(name, ".")
			ext := "no ext"
			if dot > 0 {
				ext = name[dot:]
			}
			byExt[ext]++
			files = append(files, [2]string{name, humanSize(size)})
		}
	}
	if len(dirs) == 0 && len(files) == 0 {
		return input
	}
	var out strings.Builder
	for _, d := range dirs {
		out.WriteString(d + "/\n")
	}
	for _, f := range files {
		fmt.Fprintf(&out, "%s  %s\n", f[0], f[1])
	}
	summary := fmt.Sprintf("\nSummary: %d files, %d dirs", len(files), len(dirs))
	if len(byExt) > 0 {
		type extCount struct {
			ext   string
			count int
		}
		exts := make([]extCount, 0, len(byExt))
		for ext, count := range byExt {
			exts = append(exts, extCount{ext, count})
		}
		sort.Slice(exts, func(i, j int) bool { return exts[i].count > exts[j].count })
		limit := len(exts)
		if limit > lsExtSummaryTop {
			limit = lsExtSummaryTop
		}
		parts := make([]string, 0, limit)
		for _, item := range exts[:limit] {
			parts = append(parts, fmt.Sprintf("%d %s", item.count, item.ext))
		}
		summary += " (" + strings.Join(parts, ", ")
		if len(exts) > lsExtSummaryTop {
			summary += fmt.Sprintf(", +%d more", len(exts)-lsExtSummaryTop)
		}
		summary += ")"
	}
	return strings.TrimRight(out.String()+summary, "\n")
}

var searchListHeaderRe = regexp.MustCompile(`^Result of search in '[^']*' \(total (\d+) files?\):`)

func filterSearchList(input string) string {
	lines := strings.Split(input, "\n")
	if len(lines) == 0 {
		return input
	}
	header := lines[0]
	paths := []string{}
	for _, raw := range lines[1:] {
		t := strings.TrimSpace(raw)
		if strings.HasPrefix(t, "- ") {
			paths = append(paths, t[2:])
		}
	}
	if len(paths) == 0 {
		return input
	}
	byDir := map[string][]string{}
	for _, p := range paths {
		slash := strings.LastIndex(p, "/")
		dir, name := ".", p
		if slash != -1 {
			dir = p[:slash]
			if dir == "" {
				dir = "/"
			}
			name = p[slash+1:]
		}
		byDir[dir] = append(byDir[dir], name)
	}
	dirs := make([]string, 0, len(byDir))
	for dir := range byDir {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	var out strings.Builder
	fmt.Fprintf(&out, "%s\n%d files in %d dirs:\n\n", header, len(paths), len(dirs))
	limitDirs := len(dirs)
	if limitDirs > searchListTotalDirMax {
		limitDirs = searchListTotalDirMax
	}
	for _, dir := range dirs[:limitDirs] {
		names := byDir[dir]
		fmt.Fprintf(&out, "%s/ (%d):\n", dir, len(names))
		show := len(names)
		if show > searchListPerDirMax {
			show = searchListPerDirMax
		}
		for _, n := range names[:show] {
			fmt.Fprintf(&out, "  %s\n", n)
		}
		if len(names) > searchListPerDirMax {
			fmt.Fprintf(&out, "  +%d\n", len(names)-searchListPerDirMax)
		}
		out.WriteString("\n")
	}
	if len(dirs) > searchListTotalDirMax {
		fmt.Fprintf(&out, "+%d more dirs\n", len(dirs)-searchListTotalDirMax)
	}
	return strings.TrimRight(out.String(), "\n")
}
