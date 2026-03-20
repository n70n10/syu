package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/olekukonko/tablewriter"
	"github.com/n70n10/syu/internal/db"
)

var (
	Bold   = color.New(color.Bold)
	Green  = color.New(color.FgGreen)
	Red    = color.New(color.FgRed)
	Yellow = color.New(color.FgYellow)
	Cyan   = color.New(color.FgCyan)
	Dim    = color.New(color.FgHiBlack)
)

func Header(title string) {
	line := strings.Repeat("─", 60)
	fmt.Println()
	Cyan.Println("  " + line)
	Bold.Printf("  %-58s\n", "  "+title)
	Cyan.Println("  " + line)
	fmt.Println()
}

func PrintSessionList(sessions []db.SessionSummary) {
	if len(sessions) == 0 {
		Dim.Println("  No sessions recorded yet.")
		return
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"ID", "Date", "Status", "↑ Up", "+ Install", "✗ Remove", "↓ Down", "Label"})
	table.SetBorder(false)
	table.SetColumnSeparator("  ")
	table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetHeaderLine(true)
	table.SetAutoWrapText(false)

	for _, s := range sessions {
		statusStr := statusBadge(s.Status)
		row := []string{
			fmt.Sprintf("%d", s.ID),
			s.Timestamp.Local().Format("2006-01-02 15:04"),
			statusStr,
			fmt.Sprintf("%d", s.Upgraded),
			fmt.Sprintf("%d", s.Installed),
			fmt.Sprintf("%d", s.Removed),
			fmt.Sprintf("%d", s.Downgraded),
			s.Label,
		}
		table.Append(row)
	}
	table.Render()
}

func PrintSessionDetail(session *db.Session) {
	Header(fmt.Sprintf("Session #%d — %s", session.ID,
		session.Timestamp.Local().Format("Mon Jan 2 2006, 15:04:05")))

	if session.Label != "" {
		Bold.Printf("  Label: ")
		fmt.Println(session.Label)
	}
	Bold.Printf("  Status: ")
	fmt.Println(statusBadge(session.Status))
	fmt.Println()

	if len(session.Changes) == 0 {
		Dim.Println("  No changes recorded.")
		return
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Package", "Change", "Old Version", "New Version"})
	table.SetBorder(false)
	table.SetColumnSeparator("  ")
	table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetHeaderLine(true)
	table.SetAutoWrapText(false)

	for _, c := range session.Changes {
		table.Append([]string{
			c.Name,
			changeTypeBadge(c.ChangeType),
			c.OldVersion,
			c.NewVersion,
		})
	}
	table.Render()
	fmt.Println()
	Dim.Printf("  %d change(s) total\n", len(session.Changes))
}

func PrintDelta(before, after *db.Session) {
	Header(fmt.Sprintf("Delta: Session #%d → #%d", before.ID, after.ID))

	// Build maps
	bMap := make(map[string]db.PackageChange)
	for _, c := range before.Changes {
		bMap[c.Name] = c
	}
	aMap := make(map[string]db.PackageChange)
	for _, c := range after.Changes {
		aMap[c.Name] = c
	}

	// Find packages that appear in both
	type row struct {
		name   string
		before string
		after  string
	}
	var rows []row
	seen := make(map[string]bool)

	for name, ac := range aMap {
		seen[name] = true
		bc := bMap[name]
		rows = append(rows, row{name, bc.NewVersion, ac.NewVersion})
	}
	for name, bc := range bMap {
		if !seen[name] {
			rows = append(rows, row{name, bc.NewVersion, "(removed)"})
		}
	}

	if len(rows) == 0 {
		Dim.Println("  No overlapping changes between these sessions.")
		return
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Package", fmt.Sprintf("After #%d", before.ID), fmt.Sprintf("After #%d", after.ID)})
	table.SetBorder(false)
	table.SetColumnSeparator("  ")
	table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetHeaderLine(true)

	for _, r := range rows {
		table.Append([]string{r.name, r.before, r.after})
	}
	table.Render()
}

func PrintPackageHistory(name string, changes []db.PackageChange) {
	Header(fmt.Sprintf("History: %s", name))
	if len(changes) == 0 {
		Dim.Printf("  No history found for %q\n", name)
		return
	}
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Session", "Change", "From", "To"})
	table.SetBorder(false)
	table.SetColumnSeparator("  ")
	table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetHeaderLine(true)
	for _, c := range changes {
		table.Append([]string{
			fmt.Sprintf("#%d", c.SessionID),
			changeTypeBadge(c.ChangeType),
			c.OldVersion,
			c.NewVersion,
		})
	}
	table.Render()
}

func PrintStats(stats db.Stats) {
	Header("Database Statistics")
	fmt.Printf("  Sessions recorded:    %d\n", stats.TotalSessions)
	fmt.Printf("  Total package changes: %d\n", stats.TotalChanges)
	fmt.Printf("  Unique packages seen:  %d\n", stats.UniquePackages)
	fmt.Println()
}

func PrintCacheInfo(count int, sizeBytes int64) {
	Header("Pacman Cache")
	fmt.Printf("  Cached packages:  %d files\n", count)
	fmt.Printf("  Cache size:       %s\n", humanBytes(sizeBytes))
	fmt.Println()
}

func Successf(format string, args ...any) {
	Green.Printf("  ✓ "+format+"\n", args...)
}

func Errorf(format string, args ...any) {
	Red.Fprintf(os.Stderr, "  ✗ "+format+"\n", args...)
}

func Infof(format string, args ...any) {
	fmt.Printf("  "+format+"\n", args...)
}

func statusBadge(status string) string {
	switch status {
	case "completed":
		return color.GreenString("✓ completed")
	case "rolled_back":
		return color.YellowString("↩ rolled back")
	default:
		return status
	}
}

func changeTypeBadge(ct string) string {
	switch ct {
	case "upgraded":
		return color.YellowString("↑ upgraded")
	case "installed":
		return color.GreenString("+ installed")
	case "removed":
		return color.RedString("✗ removed")
	case "downgraded":
		return color.CyanString("↓ downgraded")
	default:
		return ct
	}
}

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// FormatAge returns a human-readable relative time.
func FormatAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("Jan 2, 2006")
	}
}
