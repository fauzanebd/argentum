package spec

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/fauzanebd/argentum/internal/adapters/db"
)

// Resolved is one panel's answer in the shape the browser draws.
//
// Values are raw: no formatting, no rounding, no currency symbol. The Fmt the
// panel carries travels beside them so the browser decides how to write them,
// which is what keeps a chart label, a table cell and a CSV export from
// disagreeing about the same number.
type Resolved struct {
	PanelID string `json:"panel_id"`
	Title   string `json:"title,omitempty"`
	Viz     Viz    `json:"viz"`
	Fmt     Format `json:"fmt,omitempty"`

	// Charts.
	Labels []string `json:"labels,omitempty"`
	Series []Series `json:"series,omitempty"`

	// KPI. Pointers, because a KPI over no rows has no value and a zero there is
	// the fabrication T-Q9 exists to stop: "nothing matched" and "the total is
	// nought" are different facts and only one of them is safe to read aloud.
	Value      *float64 `json:"value,omitempty"`
	Comparison *float64 `json:"comparison,omitempty"`
	Delta      *float64 `json:"delta,omitempty"`
	DeltaPct   *float64 `json:"delta_pct,omitempty"`

	// Table.
	Columns []string         `json:"columns,omitempty"`
	Rows    []map[string]any `json:"rows,omitempty"`

	RowCount int `json:"row_count"`
	// Truncated says there is more data than the panel drew. SeriesTruncated
	// says there are more *series* than the palette can colour. They are
	// different facts and read differently to a person, so they are two fields.
	Truncated       bool   `json:"truncated,omitempty"`
	SeriesTruncated bool   `json:"series_truncated,omitempty"`
	Note            string `json:"note,omitempty"`
	Error           string `json:"error,omitempty"`
}

// Series is one line, bar group or slice set. A nil point is a gap in the data,
// never a zero — a line chart that draws a missing month at the axis is a claim
// the data does not make.
type Series struct {
	Name   string     `json:"name"`
	Points []*float64 `json:"points"`
}

// Project turns a result set into what the browser draws, according to the
// panel's mapping.
//
// A mapping that names a column the result lacks is the failure this is written
// around: it is the new failure class the spec introduces (Metabase inferred
// column roles; here the author states them), and a chart with the wrong series
// draws without complaint. So the error names the columns that would have
// worked, and the caller can hand that straight to whoever wrote the spec.
func Project(p *Panel, res *db.QueryResult) (*Resolved, error) {
	if res == nil {
		return nil, fmt.Errorf("the query returned nothing")
	}
	out := &Resolved{
		PanelID:   p.ID,
		Title:     p.Title,
		Viz:       p.Viz,
		Fmt:       p.Fmt,
		RowCount:  res.Count,
		Truncated: res.Truncated,
	}

	// "The query ran and matched nothing" is a fact worth carrying, and it is not
	// the same fact as an error. Only the KPI path used to say it, so a chart
	// over an empty window came back as an empty series with no explanation —
	// which reads, to anything that is not the browser, as a dashboard that
	// worked.
	if res.Count == 0 {
		out.Note = "no rows matched this panel's filters"
	}

	switch p.Viz {
	case VizTable:
		// A table draws what the query returned, in the order it returned it.
		out.Columns, out.Rows = res.Columns, res.Rows
		return out, nil
	case VizKPI:
		return projectKPI(p, res, out)
	case VizPie, VizDonut:
		return projectCategorical(p, res, out)
	default:
		if p.Map.SeriesBy != "" {
			return projectLong(p, res, out)
		}
		return projectWide(p, res, out)
	}
}

func projectKPI(p *Panel, res *db.QueryResult, out *Resolved) (*Resolved, error) {
	if err := requireColumns(res, p.Map.Value, p.Map.DeltaValue); err != nil {
		return nil, err
	}
	if len(res.Rows) == 0 {
		// Not an error and not a zero. The panel says so, and T-D7's caller keeps
		// the note rather than inventing a number.
		out.Note = "no rows matched this panel's filters"
		return out, nil
	}
	v, err := cell(res.Rows[0], p.Map.Value)
	if err != nil {
		return nil, fmt.Errorf("map.value %q: %w", p.Map.Value, err)
	}
	out.Value = v
	if p.Map.DeltaValue != "" {
		c, err := cell(res.Rows[0], p.Map.DeltaValue)
		if err != nil {
			return nil, fmt.Errorf("map.delta_value %q: %w", p.Map.DeltaValue, err)
		}
		out.Comparison = c
		if v != nil && c != nil {
			d := *v - *c
			out.Delta = &d
			if *c != 0 {
				pct := d / abs(*c) * 100
				out.DeltaPct = &pct
			}
		}
	}
	if res.Count > 1 {
		// A KPI whose query returned more than one row is answering a different
		// question from the one it draws. Reported rather than refused, because
		// the first row is still a number somebody can check — and because
		// refusing at read time would blank a panel that has been fine for weeks.
		out.Note = fmt.Sprintf("the query returned %d rows; a kpi reads the first", res.Count)
	}
	return out, nil
}

func projectCategorical(p *Panel, res *db.QueryResult, out *Resolved) (*Resolved, error) {
	if err := requireColumns(res, p.Map.Label, p.Map.Value); err != nil {
		return nil, err
	}
	points := make([]*float64, 0, len(res.Rows))
	for _, row := range res.Rows {
		out.Labels = append(out.Labels, label(row[p.Map.Label]))
		v, err := cell(row, p.Map.Value)
		if err != nil {
			return nil, fmt.Errorf("map.value %q: %w", p.Map.Value, err)
		}
		points = append(points, v)
	}
	if len(points) > 0 {
		out.Series = []Series{{Name: p.Map.Value, Points: points}}
	}
	return out, nil
}

// projectWide reads the wide form: one row per category, one column per series
// (month, revenue, cost).
func projectWide(p *Panel, res *db.QueryResult, out *Resolved) (*Resolved, error) {
	cols := append([]string{p.Map.Label}, p.Map.Series...)
	if err := requireColumns(res, cols...); err != nil {
		return nil, err
	}
	series := make([]Series, len(p.Map.Series))
	for i, name := range p.Map.Series {
		series[i] = Series{Name: name, Points: make([]*float64, 0, len(res.Rows))}
	}
	for _, row := range res.Rows {
		out.Labels = append(out.Labels, label(row[p.Map.Label]))
		for i, name := range p.Map.Series {
			v, err := cell(row, name)
			if err != nil {
				return nil, fmt.Errorf("series %q: %w", name, err)
			}
			series[i].Points = append(series[i].Points, v)
		}
	}
	out.Series = series
	return out, nil
}

// projectLong reads the long form: one row per (category, series) pair, with the
// measure in a third column (month, channel, revenue). This is the shape an
// agent writes most often, because it is what GROUP BY two columns returns.
func projectLong(p *Panel, res *db.QueryResult, out *Resolved) (*Resolved, error) {
	if err := requireColumns(res, p.Map.Label, p.Map.SeriesBy, p.Map.Value); err != nil {
		return nil, err
	}

	var labels []string
	labelAt := map[string]int{}
	var names []string
	byName := map[string][]*float64{}
	magnitude := map[string]float64{}

	for _, row := range res.Rows {
		l := label(row[p.Map.Label])
		if _, ok := labelAt[l]; !ok {
			labelAt[l] = len(labels)
			labels = append(labels, l)
		}
		name := label(row[p.Map.SeriesBy])
		if _, ok := byName[name]; !ok {
			names = append(names, name)
			byName[name] = nil
		}
		v, err := cell(row, p.Map.Value)
		if err != nil {
			return nil, fmt.Errorf("map.value %q: %w", p.Map.Value, err)
		}
		// Grow to the label's index; every gap stays nil, because a pair the
		// query did not return is a gap and not a zero.
		pts := byName[name]
		for len(pts) <= labelAt[l] {
			pts = append(pts, nil)
		}
		pts[labelAt[l]] = v
		byName[name] = pts
		if v != nil {
			magnitude[name] += abs(*v)
		}
	}

	// More series than the palette has colours: keep the largest, because a
	// reader looking at a chart of channels wants the channels that matter, and
	// say so. Track F is where the dropped ones become an "Other" band — the
	// server-side renderer already does that (docs/coverage/report-charts.md),
	// and one normalisation shared with the PDF is the point of doing it there
	// rather than twice.
	if len(names) > SeriesCap {
		slices.SortStableFunc(names, func(a, b string) int {
			switch {
			case magnitude[a] > magnitude[b]:
				return -1
			case magnitude[a] < magnitude[b]:
				return 1
			default:
				return 0
			}
		})
		out.SeriesTruncated = true
		names = names[:SeriesCap]
	}

	out.Labels = labels
	for _, name := range names {
		pts := byName[name]
		for len(pts) < len(labels) {
			pts = append(pts, nil)
		}
		out.Series = append(out.Series, Series{Name: name, Points: pts})
	}
	return out, nil
}

// requireColumns refuses a mapping that names a column the result does not have,
// naming the ones that would have worked.
func requireColumns(res *db.QueryResult, want ...string) error {
	for _, name := range want {
		if name == "" {
			continue
		}
		if !slices.Contains(res.Columns, name) {
			return fmt.Errorf("the result has no column %q — %s", name, columnList(res.Columns))
		}
	}
	return nil
}

// cell coerces one result value into a float64, or nil for SQL NULL.
//
// NULL is a gap, not a zero, on every path in this file. It is the same
// distinction run_sql and the metric registry make, and it is worth the pointer:
// SUM over an empty set returns NULL on all three dialects, so a chart that
// treats it as 0 draws a claim the warehouse never made.
func cell(row map[string]any, col string) (*float64, error) {
	v, ok := row[col]
	if !ok || v == nil {
		return nil, nil
	}
	switch n := v.(type) {
	case float64:
		return &n, nil
	case float32:
		f := float64(n)
		return &f, nil
	case int64:
		f := float64(n)
		return &f, nil
	case int32:
		f := float64(n)
		return &f, nil
	case int:
		f := float64(n)
		return &f, nil
	case bool:
		f := 0.0
		if n {
			f = 1
		}
		return &f, nil
	case []byte:
		return parseFloat(string(n))
	case string:
		return parseFloat(n)
	default:
		return nil, fmt.Errorf("column %q holds %T, which is not a number a chart can draw", col, v)
	}
}

func parseFloat(s string) (*float64, error) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return nil, fmt.Errorf("%q is not numeric", s)
	}
	return &f, nil
}

// label renders a category value as text. A chart's x-axis is text by the time
// it is drawn, whatever the column's type, and doing the conversion here means
// the browser never has to guess how to write a driver's time.Time.
func label(v any) string {
	switch s := v.(type) {
	case nil:
		return ""
	case string:
		return s
	case []byte:
		return string(s)
	case fmt.Stringer:
		return s.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
