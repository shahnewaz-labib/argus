package tui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/transcript"
)

// Antigravity tool detail renderers.

// agyResultBody strips the "Created At:"/"Completed At:" timestamp lines that
// prefix every antigravity tool result, returning the remaining payload.
func agyResultBody(result string) string {
	lines := strings.Split(result, "\n")
	i := 0
	for i < len(lines) {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "Created At:") || strings.HasPrefix(t, "Completed At:") {
			i++
			continue
		}
		break
	}
	return strings.TrimLeft(strings.Join(lines[i:], "\n"), "\n")
}

// agyBoilerplate lists the recurring instruction sentences agy appends to some results.
var agyBoilerplate = []string{
	"If relevant, proactively run terminal commands to execute this code for the USER. Don't ask for permission.",
	"Do not output the path of this image to show to the user since the user can already see it. However, you can embed this image in artifacts for the USER's review.",
}

func stripAgyBoilerplate(s string) string {
	for _, b := range agyBoilerplate {
		s = strings.ReplaceAll(s, b, "")
	}
	return strings.TrimSpace(s)
}

// stripAgyReminder cuts a result at its "REMINDER:" line.
func stripAgyReminder(s string) string {
	if i := strings.Index(s, "REMINDER:"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// appendResult writes the "Result"/"Error" section (rule + label + pre-rendered
// body) onto sb when body is non-empty.
func appendResult(sb *strings.Builder, it transcript.Item, width int, body string) {
	if strings.TrimSpace(body) == "" {
		return
	}
	if sb.Len() > 0 {
		sb.WriteString(sectionRule(width) + "\n")
	}
	sb.WriteString(sectionLabel(resultLabelText(it), it.ResultIsError) + "\n")
	sb.WriteString(body)
}

// formatBytes formats a byte count: 1234 -> "1.2k", 1234567 -> "1.2M".
func formatBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1fM", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fk", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}

// runCommandDetail renders a run_command call: "$ cmd" under an optional "# cwd",
// then the result body.
func (m model) runCommandDetail(it transcript.Item, width int) string {
	var in struct {
		CommandLine string `json:"CommandLine"`
		Cwd         string `json:"Cwd"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.CommandLine != "" {
		if in.Cwd != "" {
			sb.WriteString(StyleDim.Render("# "+in.Cwd) + "\n")
		}
		sb.WriteString(StyleSecondaryBold.Render("$ "+in.CommandLine) + "\n")
	} else if it.ToolInput != "" {
		sb.WriteString(dumpLines(prettyJSON(it.ToolInput), width) + "\n")
	}

	if it.Result != "" {
		if sb.Len() > 0 {
			sb.WriteString(sectionRule(width) + "\n")
		}
		sb.WriteString(sectionLabel(resultLabelText(it), it.ResultIsError) + "\n")
		sb.WriteString(m.runCommandResultBody(it.Result, width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// runCommandResultBody renders a run_command result, splitting scaffolding from
// the command's own output after the Output:/Stdout:/Stderr: marker.
func (m model) runCommandResultBody(result string, width int) string {
	lines := strings.Split(strings.TrimRight(result, "\n"), "\n")
	outIdx, indent := -1, ""
	for i, ln := range lines {
		trimmed := strings.TrimLeft(ln, "\t")
		if trimmed == "Output:" || trimmed == "Stdout:" || trimmed == "Stderr:" {
			outIdx, indent = i, ln[:len(ln)-len(trimmed)]
			break
		}
	}
	if outIdx < 0 {
		return dumpLines(stripLinePrefix(result, "\t"), width)
	}
	head := strings.Join(stripLines(lines[:outIdx+1], indent), "\n")
	out := dumpLines(head, width)
	if outIdx+1 < len(lines) {
		body := strings.TrimRight(strings.Join(stripLines(lines[outIdx+1:], indent), "\n"), "\n")
		if body != "" {
			out += "\n" + m.renderToolText(body, width)
		}
	}
	return out
}

func stripLines(lines []string, prefix string) []string {
	out := make([]string, len(lines))
	for i, ln := range lines {
		out[i] = strings.TrimPrefix(ln, prefix)
	}
	return out
}

func stripLinePrefix(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimLeft(ln, prefix)
	}
	return strings.Join(lines, "\n")
}

// grepSearchDetail renders a grep_search: query header then matched results.
func (m model) grepSearchDetail(it transcript.Item, width int) string {
	var in struct {
		Query           string `json:"Query"`
		SearchPath      string `json:"SearchPath"`
		IsRegex         bool   `json:"IsRegex"`
		CaseInsensitive bool   `json:"CaseInsensitive"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.Query != "" {
		sb.WriteString(StyleSecondaryBold.Render(`"` + in.Query + `"`))
		if in.SearchPath != "" {
			sb.WriteString(StyleDim.Render(" in " + in.SearchPath))
		}
		var flags []string
		if in.IsRegex {
			flags = append(flags, "regex")
		}
		if in.CaseInsensitive {
			flags = append(flags, "case-insensitive")
		}
		if len(flags) > 0 {
			sb.WriteString(StyleDim.Render("  (" + strings.Join(flags, " · ") + ")"))
		}
	} else if it.ToolInput != "" {
		sb.WriteString(m.renderToolText(it.ToolInput, width))
	}

	appendResult(&sb, it, width, m.grepSearchResult(it.Result, width))
	return strings.TrimRight(sb.String(), "\n")
}

func (m model) grepSearchResult(result string, width int) string {
	body := agyResultBody(result)
	if body == "" {
		return ""
	}
	var rows []string
	for _, ln := range strings.Split(body, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		var e struct {
			File        string `json:"File"`
			LineNumber  int    `json:"LineNumber"`
			LineContent string `json:"LineContent"`
		}
		if json.Unmarshal([]byte(ln), &e) == nil && e.File != "" {
			loc := fmt.Sprintf("%s:%d", e.File, e.LineNumber)
			rows = append(rows, StyleDim.Render(loc)+" "+strings.TrimSpace(e.LineContent))
		} else {
			rows = append(rows, ln)
		}
	}
	return hardWrap(strings.Join(rows, "\n"), width)
}

// listDirDetail renders a list_dir: path header then entries.
func (m model) listDirDetail(it transcript.Item, width int) string {
	var in struct {
		DirectoryPath string `json:"DirectoryPath"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.DirectoryPath != "" {
		sb.WriteString(StyleSecondary.Render(in.DirectoryPath))
	}
	appendResult(&sb, it, width, m.listDirResult(it.Result, width))
	return strings.TrimRight(sb.String(), "\n")
}

func (m model) listDirResult(result string, width int) string {
	body := agyResultBody(result)
	if body == "" {
		return ""
	}
	var rows []string
	for _, ln := range strings.Split(body, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		var e struct {
			Name      string `json:"name"`
			IsDir     bool   `json:"isDir"`
			SizeBytes string `json:"sizeBytes"`
		}
		if json.Unmarshal([]byte(ln), &e) == nil && e.Name != "" {
			if e.IsDir {
				rows = append(rows, StyleSecondary.Render(e.Name+"/"))
				continue
			}
			row := e.Name
			if n, err := strconv.Atoi(e.SizeBytes); err == nil {
				row += "  " + StyleDim.Render(formatBytes(n))
			}
			rows = append(rows, row)
		} else {
			rows = append(rows, ln)
		}
	}
	return hardWrap(strings.Join(rows, "\n"), width)
}

// viewFileDetail renders a view_file: path header, a compact meta line, then the
// numbered file content.
func (m model) viewFileDetail(it transcript.Item, width int) string {
	var in struct {
		AbsolutePath string `json:"AbsolutePath"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.AbsolutePath != "" {
		sb.WriteString(StyleSecondary.Render(in.AbsolutePath) + "\n")
	}
	meta, content := splitViewFileResult(it.Result)
	if meta != "" {
		sb.WriteString(StyleDim.Render(meta) + "\n")
	}
	switch {
	case content != "":
		if sb.Len() > 0 {
			sb.WriteString(sectionRule(width) + "\n")
		}
		sb.WriteString(m.renderToolText(content, width))
	case it.Result != "":
		if sb.Len() > 0 {
			sb.WriteString(sectionRule(width) + "\n")
		}
		sb.WriteString(m.renderToolText(agyResultBody(it.Result), width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func splitViewFileResult(result string) (meta, content string) {
	body := agyResultBody(result)
	if body == "" {
		return "", ""
	}
	lines := strings.Split(body, "\n")
	var metaParts []string
	contentStart := -1
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(t, "Total Lines:"), strings.HasPrefix(t, "Total Bytes:"), strings.HasPrefix(t, "Showing lines"):
			metaParts = append(metaParts, t)
		case strings.HasPrefix(t, "The following code"):
			contentStart = i + 1
		}
		if contentStart >= 0 {
			break
		}
	}
	if contentStart >= 0 && contentStart < len(lines) {
		content = strings.TrimRight(strings.Join(lines[contentStart:], "\n"), "\n")
	}
	return strings.Join(metaParts, " · "), content
}

// writeToFileDetail renders a write_to_file: optional description, target path
// (with an overwrite marker), then the new content as an all-additions diff.
func (m model) writeToFileDetail(it transcript.Item, width int) string {
	var in struct {
		TargetFile  string `json:"TargetFile"`
		Description string `json:"Description"`
		CodeContent string `json:"CodeContent"`
		Overwrite   bool   `json:"Overwrite"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.Description != "" {
		sb.WriteString(StyleDim.Render("# "+in.Description) + "\n")
	}
	if in.TargetFile != "" {
		head := StyleSecondary.Render(in.TargetFile)
		if in.Overwrite {
			head += StyleDim.Render("  (overwrite)")
		}
		sb.WriteString(head + "\n")
	}
	if in.CodeContent != "" {
		sb.WriteString(sectionRule(width) + "\n")
		sb.WriteString(strings.Join(addedLines(in.CodeContent), "\n"))
	} else if it.ToolInput != "" {
		sb.WriteString(m.renderToolText(it.ToolInput, width))
	}
	appendResult(&sb, it, width, m.renderToolText(stripAgyBoilerplate(agyResultBody(it.Result)), width))
	return strings.TrimRight(sb.String(), "\n")
}

// replaceFileContentDetail renders a replace_file_content edit as a colored diff
// under the file path and line range.
func (m model) replaceFileContentDetail(it transcript.Item, width int) string {
	var in struct {
		TargetFile         string `json:"TargetFile"`
		Description        string `json:"Description"`
		StartLine          int    `json:"StartLine"`
		EndLine            int    `json:"EndLine"`
		TargetContent      string `json:"TargetContent"`
		ReplacementContent string `json:"ReplacementContent"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.Description != "" {
		sb.WriteString(StyleDim.Render("# "+in.Description) + "\n")
	}
	if in.TargetFile != "" {
		head := StyleSecondary.Render(in.TargetFile)
		if in.StartLine > 0 || in.EndLine > 0 {
			head += StyleDim.Render(fmt.Sprintf("  (lines %d–%d)", in.StartLine, in.EndLine))
		}
		sb.WriteString(head + "\n")
	}
	if in.TargetContent != "" || in.ReplacementContent != "" {
		sb.WriteString(sectionRule(width) + "\n")
		sb.WriteString(strings.Join(lineDiff(in.TargetContent, in.ReplacementContent), "\n"))
	} else if it.ToolInput != "" {
		sb.WriteString(m.renderToolText(it.ToolInput, width))
	}
	if it.ResultIsError {
		appendResult(&sb, it, width, m.renderToolText(agyResultBody(it.Result), width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// multiReplaceFileContentDetail renders a multi_replace_file_content edit as one
// colored diff per chunk under the file path and edit count.
func (m model) multiReplaceFileContentDetail(it transcript.Item, width int) string {
	var in struct {
		TargetFile        string `json:"TargetFile"`
		Description       string `json:"Description"`
		ReplacementChunks []struct {
			StartLine          int    `json:"StartLine"`
			EndLine            int    `json:"EndLine"`
			TargetContent      string `json:"TargetContent"`
			ReplacementContent string `json:"ReplacementContent"`
		} `json:"ReplacementChunks"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.Description != "" {
		sb.WriteString(StyleDim.Render("# "+in.Description) + "\n")
	}
	if in.TargetFile != "" {
		head := StyleSecondary.Render(in.TargetFile)
		if n := len(in.ReplacementChunks); n > 0 {
			head += StyleDim.Render(fmt.Sprintf("  (%d edits)", n))
		}
		sb.WriteString(head + "\n")
	}
	if len(in.ReplacementChunks) > 0 {
		sb.WriteString(sectionRule(width))
		for i, c := range in.ReplacementChunks {
			sb.WriteString("\n" + StyleDim.Render(fmt.Sprintf("─── edit %d (lines %d–%d) ───", i+1, c.StartLine, c.EndLine)) + "\n")
			sb.WriteString(strings.Join(lineDiff(c.TargetContent, c.ReplacementContent), "\n"))
		}
	} else if it.ToolInput != "" {
		sb.WriteString(m.renderToolText(it.ToolInput, width))
	}
	if it.ResultIsError {
		appendResult(&sb, it, width, m.renderToolText(agyResultBody(it.Result), width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// searchWebDetail renders a search_web: query and domain then the summary.
func (m model) searchWebDetail(it transcript.Item, width int) string {
	var in struct {
		Query  string `json:"query"`
		Domain string `json:"domain"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.Query != "" {
		sb.WriteString(StyleSecondaryBold.Render(`"` + in.Query + `"`))
		if in.Domain != "" {
			sb.WriteString(StyleDim.Render("  " + in.Domain))
		}
	}
	body := agyResultBody(it.Result)
	if idx := strings.Index(body, "returned the following summary:"); idx >= 0 {
		body = strings.TrimSpace(body[idx+len("returned the following summary:"):])
	}
	if body != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n" + sectionRule(width) + "\n")
		}
		sb.WriteString(m.renderMD(body, width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// generateImageDetail renders a generate_image: name + aspect ratio, the prompt,
// then the saved-image path.
func (m model) generateImageDetail(it transcript.Item, width int) string {
	var in struct {
		Prompt      string `json:"Prompt"`
		ImageName   string `json:"ImageName"`
		AspectRatio string `json:"AspectRatio"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.ImageName != "" {
		head := StyleSecondaryBold.Render(in.ImageName)
		if in.AspectRatio != "" {
			head += StyleDim.Render("  (" + in.AspectRatio + ")")
		}
		sb.WriteString(head + "\n")
	}
	if in.Prompt != "" {
		sb.WriteString(StyleDim.Render("# "+in.Prompt) + "\n")
	}
	appendResult(&sb, it, width, m.renderToolText(agyImageResult(it.Result), width))
	return strings.TrimRight(sb.String(), "\n")
}

func agyImageResult(result string) string {
	body := stripAgyBoilerplate(agyResultBody(result))
	var keep []string
	for _, ln := range strings.Split(body, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "Using prompt:") {
			continue
		}
		keep = append(keep, t)
	}
	return strings.Join(keep, "\n")
}

// defineSubagentDetail renders a define_subagent: name, description, enabled tool
// groups, then the system prompt (markdown).
func (m model) defineSubagentDetail(it transcript.Item, width int) string {
	var in struct {
		Name                string `json:"name"`
		Description         string `json:"description"`
		SystemPrompt        string `json:"system_prompt"`
		EnableWriteTools    bool   `json:"enable_write_tools"`
		EnableMCPTools      bool   `json:"enable_mcp_tools"`
		EnableSubagentTools bool   `json:"enable_subagent_tools"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.Name != "" {
		sb.WriteString(StyleSecondaryBold.Render(in.Name) + "\n")
	}
	if in.Description != "" {
		sb.WriteString(wrapDim(in.Description, width) + "\n")
	}
	var tools []string
	if in.EnableWriteTools {
		tools = append(tools, "write")
	}
	if in.EnableMCPTools {
		tools = append(tools, "mcp")
	}
	if in.EnableSubagentTools {
		tools = append(tools, "subagent")
	}
	label := "none"
	if len(tools) > 0 {
		label = strings.Join(tools, " · ")
	}
	sb.WriteString(StyleDim.Render("tools: " + label))
	if in.SystemPrompt != "" {
		sb.WriteString("\n" + sectionRule(width) + "\n")
		sb.WriteString(StyleSecondaryBold.Render("System Prompt") + "\n")
		sb.WriteString(m.renderMD(in.SystemPrompt, width))
	}
	if it.ResultIsError {
		appendResult(&sb, it, width, m.renderToolText(agyResultBody(it.Result), width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// manageSubagentsDetail renders a manage_subagents call: the action then the
// (free-form) result body.
func (m model) manageSubagentsDetail(it transcript.Item, width int) string {
	var in struct {
		Action string `json:"Action"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.Action != "" {
		sb.WriteString(StyleSecondaryBold.Render(in.Action))
	}
	if body := agyResultBody(it.Result); body != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n" + sectionRule(width) + "\n")
		}
		sb.WriteString(m.renderToolText(body, width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// manageTaskDetail renders a manage_task call: `action taskId` then the task's
// status fields as key/value lines (agy's poll-warning reminder is dropped).
func (m model) manageTaskDetail(it transcript.Item, width int) string {
	var in struct {
		Action string `json:"Action"`
		TaskID string `json:"TaskId"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.Action != "" {
		head := StyleSecondaryBold.Render(in.Action)
		if in.TaskID != "" {
			head += StyleDim.Render("  " + in.TaskID)
		}
		sb.WriteString(head)
	}
	if body := stripAgyReminder(agyResultBody(it.Result)); body != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n" + sectionRule(width) + "\n")
		}
		sb.WriteString(dumpLines(body, width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// askQuestionDetail renders an answered ask_question: header + prompt, options
// with the chosen one(s) marked.
func (m model) askQuestionDetail(it transcript.Item, width int) string {
	var in struct {
		Questions []struct {
			Question    string   `json:"question"`
			MultiSelect bool     `json:"is_multi_select"`
			Options     []string `json:"options"`
		} `json:"questions"`
	}
	unmarshalInput(it.ToolInput, &in)
	if len(in.Questions) == 0 {
		return m.genericToolBody(it, width)
	}
	answers := parseAgyAnswers(agyResultBody(it.Result))

	blocks := make([]string, 0, len(in.Questions))
	for qi, q := range in.Questions {
		var b strings.Builder
		b.WriteString(StyleAccentBold.Render(Icon.Chat.Glyph + " Agent is asking") + "\n")
		if q.Question != "" {
			b.WriteString(m.renderMD(q.Question, width-2))
		}
		ans := answers[qi]
		for _, opt := range q.Options {
			isChosen := ans != "" && strings.Contains(ans, opt)
			var mark string
			if q.MultiSelect {
				if isChosen {
					mark = "[x] "
				} else {
					mark = "[ ] "
				}
			} else if isChosen {
				mark = lipgloss.NewStyle().Foreground(ColorAccent).Render("◉") + " "
			} else {
				mark = StyleDim.Render("○") + " "
			}
			label := StyleSecondary.Render(opt)
			if isChosen {
				label = StylePrimaryBold.Render(opt)
			}
			b.WriteString("\n" + mark + label)
		}
		blocks = append(blocks, b.String())
	}
	return strings.Join(blocks, "\n"+sectionRule(width)+"\n")
}

// parseAgyAnswers parses agy's "A1: …\nA2: …" answer block into a 0-based
// question-index → answer map.
func parseAgyAnswers(result string) map[int]string {
	out := map[int]string{}
	for _, ln := range strings.Split(result, "\n") {
		ln = strings.TrimSpace(ln)
		if len(ln) < 3 || ln[0] != 'A' {
			continue
		}
		colon := strings.IndexByte(ln, ':')
		if colon < 0 {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(ln[1:colon]))
		if err != nil {
			continue
		}
		out[n-1] = strings.TrimSpace(ln[colon+1:])
	}
	return out
}

// askPermissionDetail renders an ask_permission call: `action(target)`, the
// reason, then the grant/deny result.
func (m model) askPermissionDetail(it transcript.Item, width int) string {
	var in struct {
		Action string `json:"Action"`
		Target string `json:"Target"`
		Reason string `json:"Reason"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.Action != "" {
		title := in.Action
		if in.Target != "" {
			title += "(" + in.Target + ")"
		}
		sb.WriteString(StyleSecondaryBold.Render(title) + "\n")
	}
	if in.Reason != "" {
		sb.WriteString(wrapDim(in.Reason, width))
	}
	appendResult(&sb, it, width, m.renderToolText(agyResultBody(it.Result), width))
	return strings.TrimRight(sb.String(), "\n")
}

// listPermissionsDetail renders a list_permissions result verbatim.
func (m model) listPermissionsDetail(it transcript.Item, width int) string {
	body := agyResultBody(it.Result)
	if body == "" {
		return m.genericToolBody(it, width)
	}
	return m.renderToolText(body, width)
}

// sendMessageDetail renders a send_message call: `→ recipient` then the message
// (markdown).
func (m model) sendMessageDetail(it transcript.Item, width int) string {
	var in struct {
		Message   string `json:"Message"`
		Recipient string `json:"Recipient"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.Recipient != "" {
		sb.WriteString(StyleSecondaryBold.Render("→ "+in.Recipient) + "\n")
	}
	if in.Message != "" {
		sb.WriteString(m.renderMD(in.Message, width))
	} else if it.ToolInput != "" {
		sb.WriteString(m.renderToolText(it.ToolInput, width))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// scheduleDetail renders a schedule call: `Timer Ns (condition: …)`, the prompt,
// then the result (task id / description).
func (m model) scheduleDetail(it transcript.Item, width int) string {
	var in struct {
		DurationSeconds string `json:"DurationSeconds"`
		Prompt          string `json:"Prompt"`
		TimerCondition  string `json:"TimerCondition"`
	}
	unmarshalInput(it.ToolInput, &in)

	var sb strings.Builder
	if in.DurationSeconds != "" {
		head := StyleSecondaryBold.Render("Timer " + in.DurationSeconds + "s")
		if in.TimerCondition != "" {
			head += StyleDim.Render("  (condition: " + in.TimerCondition + ")")
		}
		sb.WriteString(head + "\n")
	}
	if in.Prompt != "" {
		sb.WriteString(StyleDim.Render("# " + in.Prompt))
	}
	appendResult(&sb, it, width, m.renderToolText(agyResultBody(it.Result), width))
	return strings.TrimRight(sb.String(), "\n")
}
