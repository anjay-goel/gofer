package cmd

import (
	"flag"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/anjay-goel/gofer/internal/core"
)

const (
	columnGap   = 2
	detailClip  = 40
	summaryClip = 100
	summaryMin  = 16
)

func (a *app) ls(args []string) error {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	peek := fs.Bool("peek", false, "")
	asJSON := fs.Bool("json", false, "")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	views, err := spinCall(a.client, "Reading "+a.host, core.List, core.ListParams{ClientID: a.cfg.ClientID, Peek: *peek})
	if err != nil {
		return err
	}
	saveNames(a.host, views)
	if *asJSON {
		return printJSON(views)
	}
	if len(views) == 0 {
		fmt.Println("No agents on", a.host)
		return nil
	}
	fmt.Print(agentTable(views, terminalWidth()))
	return nil
}

type column struct {
	title string
	cells []cell
}

func (c column) width() int {
	width := displayWidth(c.title)
	for _, cl := range c.cells {
		width = max(width, displayWidth(cl.text))
	}
	return width
}

func (c column) blank() bool {
	return !slices.ContainsFunc(c.cells, func(cl cell) bool {
		return cl.text != "" && (c.title != "SOURCE" || cl.text != "gofer")
	})
}

func commitWord(count *int) string {
	if count == nil {
		return ""
	}
	return plural(*count, "commit")
}

func summaryText(v core.View) string {
	if (v.Stuck() || v.State == core.StateUnknown) && v.Next != "" {
		return "Run " + v.Next
	}
	return v.Summary
}

func agentColumns(views []core.View) []column {
	columns := []column{{title: "NAME"}, {title: "STATE"}, {title: "CHANGED"}, {title: "COMMITS"}, {title: "BRANCH"}, {title: "SOURCE"}, {title: "SUMMARY"}}
	for _, v := range views {
		name := cell{v.Name, bold}
		if v.New {
			name.text += "*"
		}
		row := []cell{
			name, stateCell(v, detailClip), {v.Ago, dim}, {commitWord(v.Commits), ""}, {v.Branch, ""}, {v.Source, dim},
			{clipWidth(summaryText(v), summaryClip), ""},
		}
		for i, c := range row {
			columns[i].cells = append(columns[i].cells, c)
		}
	}
	return columns
}

func removeColumn(columns []column, title string) []column {
	return slices.DeleteFunc(columns, func(c column) bool { return c.title == title })
}

func tableWidth(columns []column, summary int) int {
	width := (len(columns) - 1) * columnGap
	for _, c := range columns {
		if c.title == "SUMMARY" {
			width += min(c.width(), summary)
		} else {
			width += c.width()
		}
	}
	return width
}

func shrink(columns []column, title string, by int) int {
	for i, c := range columns {
		if c.title != title {
			continue
		}
		size := max(c.width()-by, 1)
		used := c.width() - size
		columns[i].title = clipWidth(c.title, size)
		columns[i].cells = make([]cell, len(c.cells))
		for j, cl := range c.cells {
			columns[i].cells[j] = cell{clipWidth(cl.text, size), cl.code}
		}
		return used
	}
	return 0
}

func fitColumns(columns []column, width int) []column {
	columns = slices.DeleteFunc(columns, func(c column) bool {
		return c.blank() && c.title != "NAME" && c.title != "STATE"
	})
	for _, title := range []string{"SOURCE", "BRANCH"} {
		if tableWidth(columns, math.MaxInt) > width {
			columns = removeColumn(columns, title)
		}
	}
	for _, title := range []string{"COMMITS", "CHANGED", "SUMMARY"} {
		if tableWidth(columns, summaryMin) > width {
			columns = removeColumn(columns, title)
		}
	}
	excess := tableWidth(columns, math.MaxInt) - width
	for _, title := range []string{"SUMMARY", "STATE", "NAME"} {
		if excess > 0 {
			excess -= shrink(columns, title, excess)
		}
	}
	return columns
}

func agentTable(views []core.View, width int) string {
	columns := agentColumns(views)
	if width > 0 {
		columns = fitColumns(columns, width)
	}
	rows := make([][]cell, len(views)+1)
	for _, c := range columns {
		rows[0] = append(rows[0], cell{c.title, dim})
		for i, cl := range c.cells {
			rows[i+1] = append(rows[i+1], cl)
		}
	}
	return table(rows)
}

func table(rows [][]cell) string {
	widths := map[int]int{}
	for _, row := range rows {
		for i, c := range row {
			widths[i] = max(widths[i], displayWidth(c.text))
		}
	}
	var out strings.Builder
	for _, row := range rows {
		last := len(row) - 1
		for last > 0 && row[last].text == "" {
			last--
		}
		for i, c := range row[:last+1] {
			out.WriteString(c.String())
			if i < last {
				out.WriteString(strings.Repeat(" ", widths[i]-displayWidth(c.text)+columnGap))
			}
		}
		out.WriteByte('\n')
	}
	return out.String()
}
