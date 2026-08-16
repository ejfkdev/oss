package app

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xuri/excelize/v2"
	"gopkg.in/yaml.v3"
)

// findExportRow is one exported row. ListableURL is the dedicated field that
// stores the full access URL of an anonymously listable bucket; it is empty
// for buckets that are not anonymously listable.
type findExportRow struct {
	Input       string `yaml:"input" json:"input"`
	Provider    string `yaml:"provider" json:"provider"`
	Name        string `yaml:"name" json:"name"`
	Region      string `yaml:"region,omitempty" json:"region,omitempty"`
	State       string `yaml:"state" json:"state"`
	URL         string `yaml:"url" json:"url"`
	ListableURL string `yaml:"listable_url,omitempty" json:"listable_url,omitempty"`
}

var findExportHeader = []string{"input", "provider", "name", "region", "state", "url", "listable_url"}

// stateRank orders rows in the export: listable first, then exists, unknown,
// notfound, invalid.
func stateRank(state string) int {
	switch state {
	case findListable:
		return 0
	case findExists:
		return 1
	case findUnknown:
		return 2
	case findNotFound:
		return 3
	default:
		return 4
	}
}

func buildExportRows(inputs []string, results []findResult, invalid map[string]string) []findExportRow {
	rows := make([]findExportRow, 0, len(results)+len(invalid))
	for i := range results {
		r := &results[i]
		row := findExportRow{
			Input: r.Input, Provider: r.Provider, Name: r.Name, Region: r.Region,
			State: r.State, URL: r.URL,
		}
		if r.Listable {
			row.ListableURL = r.URL
		}
		rows = append(rows, row)
	}
	for _, in := range inputs {
		if msg, bad := invalid[in]; bad {
			rows = append(rows, findExportRow{Input: in, State: "invalid", URL: msg})
		}
	}
	sort.SliceStable(rows, func(a, b int) bool {
		return stateRank(rows[a].State) < stateRank(rows[b].State)
	})
	return rows
}

func rowToCells(r findExportRow) []string {
	return []string{r.Input, r.Provider, r.Name, r.Region, r.State, r.URL, r.ListableURL}
}

// exportFindResults writes results to path in a format chosen by extension:
// .txt .csv .xlsx .yaml/.yml .md
func exportFindResults(path string, inputs []string, results []findResult, invalid map[string]string) error {
	rows := buildExportRows(inputs, results, invalid)
	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt":
		return exportFindTxt(path, rows)
	case ".csv":
		return exportFindCSV(path, rows)
	case ".xlsx":
		return exportFindXLSX(path, rows)
	case ".yaml", ".yml":
		return exportFindYAML(path, rows)
	case ".md", ".markdown":
		return exportFindMD(path, rows)
	}
	return fmt.Errorf(T("不支持的导出格式 %q（支持 .txt .csv .xlsx .yaml .md）",
		"unsupported export format %q (want .txt .csv .xlsx .yaml .md)"), path)
}

func exportFindTxt(path string, rows []findExportRow) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	fmt.Fprintf(f, "%-40s %-22s %-8s %s\n", "INPUT", "PROVIDER", "STATE", "LISTABLE_URL")
	for _, r := range rows {
		listable := r.ListableURL
		if listable == "" {
			listable = "-"
		}
		label := r.Input
		if r.Provider != "" {
			label = r.Name
		}
		fmt.Fprintf(f, "%-40s %-22s %-8s %s\n", truncate(r.Input, 40), label, r.State, listable)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func exportFindCSV(path string, rows []findExportRow) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	if err := w.Write(findExportHeader); err != nil {
		return err
	}
	for _, r := range rows {
		if err := w.Write(rowToCells(r)); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

func exportFindXLSX(path string, rows []findExportRow) error {
	f := excelize.NewFile()
	defer f.Close()
	sheet := "find"
	idx, err := f.NewSheet(sheet)
	if err != nil {
		return err
	}
	f.SetActiveSheet(idx)
	f.DeleteSheet("Sheet1")
	for i, h := range findExportHeader {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue(sheet, cell, h)
	}
	for ri, r := range rows {
		for ci, v := range rowToCells(r) {
			cell, _ := excelize.CoordinatesToCellName(ci+1, ri+2)
			_ = f.SetCellValue(sheet, cell, v)
		}
	}
	return f.SaveAs(path)
}

func exportFindYAML(path string, rows []findExportRow) error {
	data, err := yaml.Marshal(rows)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func exportFindMD(path string, rows []findExportRow) error {
	var b strings.Builder
	b.WriteString("| " + strings.Join(findExportHeader, " | ") + " |\n")
	b.WriteString("|" + strings.Repeat("---|", len(findExportHeader)) + "\n")
	for _, r := range rows {
		cells := rowToCells(r)
		for i := range cells {
			cells[i] = strings.ReplaceAll(cells[i], "|", "\\|")
			if cells[i] == "" {
				cells[i] = "-"
			}
		}
		b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
