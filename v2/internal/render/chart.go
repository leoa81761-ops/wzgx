package render

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Data charts: a table that compares numbers across a few rows can also be shown as a bar chart.
// The renderer decides (no model involved): the table stays, the chart picture goes right after it.

type Series struct {
	Name   string
	Unit   string
	Values []float64
}

type ChartSpec struct {
	Block  int // index of the table (or table-like list) block
	Title  string
	Labels []string
	Series []Series
}

const (
	MaxCharts    = 2
	minChartRows = 3
	maxChartRows = 12
)

var numCell = regexp.MustCompile(`^(?:约|大约|近|约为)?\s*(-?\d{1,3}(?:,\d{3})+|-?\d+)(\.\d+)?\s*([^\d\s,.，、]{0,4}?)\s*(?:左右)?$`)

// parseNum reads a cell like "300 lx", "约 1,200 元", "35%"; ranges and thresholds ("以上") are not numbers.
func parseNum(s string) (float64, string, bool) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "％", "%"))
	m := numCell.FindStringSubmatch(s)
	if m == nil || strings.ContainsAny(m[3], "以上下至到~～") {
		return 0, "", false
	}
	v, err := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", "")+m[2], 64)
	return v, m[3], err == nil
}

// tableRows returns the rows the renderer would show as a table for a block (header first).
func tableRows(bl Block) ([][]string, bool) {
	switch bl.Type {
	case "table":
		if validTable(bl.Rows) {
			return bl.Rows, true
		}
	case "list":
		if rows, ok := TableFit(bl.Items); ok {
			return append([][]string{{"项目", "说明"}}, rows...), true
		}
	}
	return nil, false
}

// ChartTables lists the tables worth a bar chart: 3–12 rows with a label column and at least one
// column whose cells are all numbers in the same unit, not all equal, none negative.
func ChartTables(a Article) []ChartSpec {
	var out []ChartSpec
	title := ""
	for i, bl := range a.Blocks {
		if bl.Type == "h2" {
			title = strings.TrimSpace(bl.Text)
		}
		if len(out) >= MaxCharts {
			break
		}
		rows, ok := tableRows(bl)
		if !ok || len(rows)-1 < minChartRows || len(rows)-1 > maxChartRows {
			continue
		}
		head, data := rows[0], rows[1:]
		spec := ChartSpec{Block: i}
		good := true
		for _, r := range data {
			l := strings.TrimSpace(r[0])
			if l == "" || utf8.RuneCountInString(l) > 16 {
				good = false
				break
			}
			spec.Labels = append(spec.Labels, l)
		}
		if !good {
			continue
		}
		for c := 1; c < len(head) && len(spec.Series) < 3; c++ {
			s := Series{Name: strings.TrimSpace(head[c])}
			ok := true
			for k, r := range data {
				if c >= len(r) {
					ok = false
					break
				}
				v, unit, isNum := parseNum(r[c])
				if !isNum || v < 0 || (k > 0 && unit != s.Unit) {
					ok = false
					break
				}
				s.Unit = unit
				s.Values = append(s.Values, v)
			}
			// one scale per chart: only series in the same unit as the first one
			if ok && distinct(s.Values) && (len(spec.Series) == 0 || s.Unit == spec.Series[0].Unit) {
				spec.Series = append(spec.Series, s)
			}
		}
		if len(spec.Series) == 0 {
			continue
		}
		spec.Title = title
		if spec.Title == "" || utf8.RuneCountInString(spec.Title) > 30 {
			names := []string{}
			for _, s := range spec.Series {
				names = append(names, s.Name)
			}
			spec.Title = strings.Join(names, "、") + "对比"
		}
		out = append(out, spec)
	}
	return out
}

func distinct(v []float64) bool {
	for _, x := range v[1:] {
		if x != v[0] {
			return true
		}
	}
	return false
}
