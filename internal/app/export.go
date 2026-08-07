package app

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/urfave/cli/v3"
	"github.com/xuri/excelize/v2"
	"gopkg.in/yaml.v3"

	"oss/internal/s3x"
)

// exportRow is one row of an exported listing.
type exportRow struct {
	Type         string    `yaml:"type"`
	Key          string    `yaml:"key"`
	Size         int64     `yaml:"size"`
	LastModified time.Time `yaml:"last_modified"`
	ETag         string    `yaml:"etag"`
	StorageClass string    `yaml:"storage_class"`
}

func toExportRows(entries []cachedEntry) []exportRow {
	rows := make([]exportRow, 0, len(entries))
	for _, e := range entries {
		r := exportRow{Key: e.Key, Size: e.Size, ETag: strings.Trim(e.ETag, "\""), StorageClass: e.SC}
		if e.Dir {
			r.Type = "dir"
		} else {
			r.Type = "file"
			if e.Mod != 0 {
				r.LastModified = time.Unix(e.Mod, 0)
			}
		}
		rows = append(rows, r)
	}
	return rows
}

// runExport collects every entry matching the filters (full listing, no
// display limit) and writes it to path in a format chosen by its extension:
// .txt .csv .xlsx .yaml/.yml .md
func runExport(ctx context.Context, c *cli.Command, cl *s3x.Client, t *s3x.Target, path string) error {
	format := exportFormat(path)
	if format == "" {
		return fmt.Errorf(T(
			"不支持的导出格式 %q（需要 .txt .csv .xlsx .yaml .md）",
			"unsupported export format for %q (want .txt .csv .xlsx .yaml .md)"), path)
	}

	p := resolveListParams(c, t)
	ef := newEntryFilter(c, p)

	var entries []cachedEntry
	useCache := !c.Bool("no-cache") && !c.Bool("reset")
	fromCache, err := walkEntries(ctx, cl, t, p, ef, useCache, func(e cachedEntry) (bool, error) {
		entries = append(entries, e)
		return true, nil
	})
	if err != nil {
		return apiErr(err, cl.Anonymous)
	}
	// Warm the cache after a fresh full fetch so subsequent runs are instant.
	if !fromCache && !c.Bool("no-cache") {
		saveCompleteSnapshot(cl, t, p.prefix, p.delimStr, p.startAfter, entries)
	}
	rows := toExportRows(entries)

	if err := writeExport(path, format, rows); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "%s%s\n", checkMark(), eGreen(fmt.Sprintf(
		T("已导出 %d 条记录（%s 格式）→ %s", "exported %d entries (%s format) → %s"),
		len(rows), format, path)))
	return nil
}

func exportFormat(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt":
		return "txt"
	case ".csv":
		return "csv"
	case ".xlsx":
		return "xlsx"
	case ".yaml", ".yml":
		return "yaml"
	case ".md", ".markdown":
		return "markdown"
	}
	return ""
}

func writeExport(path, format string, rows []exportRow) error {
	switch format {
	case "txt":
		return writeTxt(path, rows)
	case "csv":
		return writeCSV(path, rows)
	case "xlsx":
		return writeXLSX(path, rows)
	case "yaml":
		return writeYAML(path, rows)
	case "markdown":
		return writeMarkdown(path, rows)
	}
	return fmt.Errorf("unsupported format %q", format)
}

// ---- txt ----

func writeTxt(path string, rows []exportRow) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	fmt.Fprintf(f, "%-6s %-12s %-19s  %s\n", "TYPE", "SIZE", "MODIFIED", "KEY")
	for _, r := range rows {
		size, mod := "-", "-"
		if r.Type == "file" {
			size = humanSize(r.Size, false)
			if !r.LastModified.IsZero() {
				mod = r.LastModified.Local().Format("2006-01-02 15:04:05")
			}
		}
		fmt.Fprintf(f, "%-6s %-12s %-19s  %s\n", strings.ToUpper(r.Type), size, mod, r.Key)
	}
	return nil
}

// ---- csv ----

var csvHeader = []string{"type", "key", "size_bytes", "last_modified", "etag", "storage_class"}

func writeCSV(path string, rows []exportRow) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	if err := w.Write(csvHeader); err != nil {
		return err
	}
	for _, r := range rows {
		rec := []string{r.Type, r.Key, fmt.Sprintf("%d", r.Size), modStr(r), r.ETag, r.StorageClass}
		if err := w.Write(rec); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

func modStr(r exportRow) string {
	if r.LastModified.IsZero() {
		return ""
	}
	return r.LastModified.UTC().Format(time.RFC3339)
}

// ---- yaml ----

func writeYAML(path string, rows []exportRow) error {
	type yamlRow struct {
		Type         string `yaml:"type"`
		Key          string `yaml:"key"`
		Size         int64  `yaml:"size,omitempty"`
		LastModified string `yaml:"last_modified,omitempty"`
		ETag         string `yaml:"etag,omitempty"`
		StorageClass string `yaml:"storage_class,omitempty"`
	}
	out := make([]yamlRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, yamlRow{
			Type: r.Type, Key: r.Key, Size: r.Size,
			LastModified: modStr(r), ETag: r.ETag, StorageClass: r.StorageClass,
		})
	}
	data, err := yaml.Marshal(out)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// ---- xlsx ----

func writeXLSX(path string, rows []exportRow) error {
	f := excelize.NewFile()
	defer f.Close()
	sheet := "objects"
	idx, err := f.NewSheet(sheet)
	if err != nil {
		return err
	}
	f.SetActiveSheet(idx)
	f.DeleteSheet("Sheet1")

	for i, h := range csvHeader {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue(sheet, cell, h)
	}
	for ri, r := range rows {
		row := ri + 2
		vals := []any{r.Type, r.Key, r.Size, modStr(r), r.ETag, r.StorageClass}
		for ci, v := range vals {
			cell, _ := excelize.CoordinatesToCellName(ci+1, row)
			_ = f.SetCellValue(sheet, cell, v)
		}
	}
	return f.SaveAs(path)
}

// ---- markdown ----

func writeMarkdown(path string, rows []exportRow) error {
	var b strings.Builder
	b.WriteString("| Type | Key | Size | Last Modified | Storage Class |\n")
	b.WriteString("|------|-----|------|---------------|---------------|\n")
	for _, r := range rows {
		size, mod := "-", "-"
		if r.Type == "file" {
			size = humanSize(r.Size, false)
			if !r.LastModified.IsZero() {
				mod = r.LastModified.Local().Format("2006-01-02 15:04:05")
			}
		}
		key := strings.ReplaceAll(r.Key, "|", "\\|")
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", r.Type, key, size, mod, r.StorageClass)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
