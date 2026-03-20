package rollback

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/fatih/color"
	"github.com/n70n10/syu/internal/db"
	"github.com/n70n10/syu/internal/pacman"
)

// Plan represents everything that needs to happen to undo a session.
type Plan struct {
	Downgrades  []Action // upgraded packages to downgrade
	Removals    []Action // newly installed packages to remove
	Reinstalls  []Action // removed packages to reinstall
	Unresolvable []Action // can't be done — missing from cache and repos
}

type Action struct {
	Change    db.PackageChange
	CachePath string // set if we found it in cache
	Source    string // "cache" | "repo" | ""
}

// BuildPlan inspects the session changes and resolves how to reverse each one.
func BuildPlan(session *db.Session) (*Plan, error) {
	plan := &Plan{}

	for _, c := range session.Changes {
		switch c.ChangeType {

		case "upgraded", "downgraded":
			// We need to go back to old_version
			target := c.OldVersion
			cachePath := pacman.FindCachedPkg(c.Name, target)
			if cachePath != "" {
				plan.Downgrades = append(plan.Downgrades, Action{
					Change:    c,
					CachePath: cachePath,
					Source:    "cache",
				})
			} else {
				available, src := pacman.CheckRepoAvailable(c.Name, target)
				if available {
					plan.Downgrades = append(plan.Downgrades, Action{
						Change: c,
						Source: src,
					})
				} else {
					plan.Unresolvable = append(plan.Unresolvable, Action{Change: c})
				}
			}

		case "installed":
			// Package didn't exist before — remove it
			plan.Removals = append(plan.Removals, Action{
				Change: c,
				Source: "system",
			})

		case "removed":
			// Package was removed — reinstall old version
			target := c.OldVersion
			cachePath := pacman.FindCachedPkg(c.Name, target)
			if cachePath != "" {
				plan.Reinstalls = append(plan.Reinstalls, Action{
					Change:    c,
					CachePath: cachePath,
					Source:    "cache",
				})
			} else {
				available, src := pacman.CheckRepoAvailable(c.Name, target)
				if available {
					plan.Reinstalls = append(plan.Reinstalls, Action{
						Change: c,
						Source: src,
					})
				} else {
					plan.Unresolvable = append(plan.Unresolvable, Action{Change: c})
				}
			}
		}
	}

	return plan, nil
}

// PrintPlan renders the rollback plan for the user to review.
func PrintPlan(plan *Plan) {
	bold := color.New(color.Bold)
	red := color.New(color.FgRed)
	yellow := color.New(color.FgYellow)
	green := color.New(color.FgGreen)
	cyan := color.New(color.FgCyan)

	fmt.Println()
	bold.Println("══════════════════════════════════════════════")
	bold.Println("  Rollback Plan")
	bold.Println("══════════════════════════════════════════════")

	if len(plan.Unresolvable) > 0 {
		fmt.Println()
		red.Printf("  ✗ UNRESOLVABLE (%d) — not in cache or repos:\n", len(plan.Unresolvable))
		for _, a := range plan.Unresolvable {
			fmt.Printf("      %-30s  %s → %s\n",
				a.Change.Name, a.Change.NewVersion, a.Change.OldVersion)
		}
		fmt.Println()
		red.Println("  These packages cannot be rolled back. Aborting.")
		return
	}

	if len(plan.Downgrades) > 0 {
		fmt.Println()
		yellow.Printf("  ↓ Downgrade (%d):\n", len(plan.Downgrades))
		for _, a := range plan.Downgrades {
			src := sourceLabel(a.Source)
			fmt.Printf("      %-30s  %s → %s  %s\n",
				a.Change.Name, a.Change.NewVersion, a.Change.OldVersion, src)
		}
	}

	if len(plan.Removals) > 0 {
		fmt.Println()
		red.Printf("  ✗ Remove (%d):\n", len(plan.Removals))
		for _, a := range plan.Removals {
			fmt.Printf("      %-30s  %s\n", a.Change.Name, a.Change.NewVersion)
		}
	}

	if len(plan.Reinstalls) > 0 {
		fmt.Println()
		green.Printf("  ✚ Reinstall (%d):\n", len(plan.Reinstalls))
		for _, a := range plan.Reinstalls {
			src := sourceLabel(a.Source)
			fmt.Printf("      %-30s  %s  %s\n", a.Change.Name, a.Change.OldVersion, src)
		}
	}

	total := len(plan.Downgrades) + len(plan.Removals) + len(plan.Reinstalls)
	fmt.Println()
	cyan.Printf("  Total: %d operations\n", total)
	bold.Println("══════════════════════════════════════════════")
}

func sourceLabel(src string) string {
	switch src {
	case "cache":
		return color.New(color.FgHiBlack).Sprint("[cache]")
	case "repo":
		return color.New(color.FgHiBlack).Sprint("[repo]")
	default:
		return ""
	}
}

// HasUnresolvable returns true if the plan can't be fully executed.
func HasUnresolvable(plan *Plan) bool {
	return len(plan.Unresolvable) > 0
}

// Confirm asks the user to confirm the rollback.
func Confirm(dryRun bool) bool {
	if dryRun {
		return false
	}
	color.New(color.FgYellow, color.Bold).Print("\n  Proceed with rollback? [y/N] ")
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		ans := strings.TrimSpace(strings.ToLower(scanner.Text()))
		return ans == "y" || ans == "yes"
	}
	return false
}

// Execute applies the rollback plan.
func Execute(plan *Plan, database *db.DB, sessionID int64) error {
	green := color.New(color.FgGreen, color.Bold)
	red := color.New(color.FgRed)
	yellow := color.New(color.FgYellow)

	fmt.Println()

	// Step 1: Downgrades
	for _, a := range plan.Downgrades {
		yellow.Printf("  ↓ Downgrading %s (%s → %s)...\n",
			a.Change.Name, a.Change.NewVersion, a.Change.OldVersion)
		var err error
		if a.Source == "cache" {
			err = pacman.InstallFromCache(a.CachePath)
		} else {
			err = pacman.InstallVersion(a.Change.Name, a.Change.OldVersion)
		}
		if err != nil {
			red.Printf("  ✗ Failed to downgrade %s: %v\n", a.Change.Name, err)
			return fmt.Errorf("downgrade %s: %w", a.Change.Name, err)
		}
		green.Printf("  ✓ Downgraded %s\n", a.Change.Name)
	}

	// Step 2: Remove newly installed packages
	for _, a := range plan.Removals {
		red.Printf("  ✗ Removing %s (%s)...\n", a.Change.Name, a.Change.NewVersion)
		if err := pacman.RemovePackage(a.Change.Name); err != nil {
			red.Printf("  ✗ Failed to remove %s: %v\n", a.Change.Name, err)
			return fmt.Errorf("remove %s: %w", a.Change.Name, err)
		}
		green.Printf("  ✓ Removed %s\n", a.Change.Name)
	}

	// Step 3: Reinstall removed packages
	for _, a := range plan.Reinstalls {
		green.Printf("  ✚ Reinstalling %s (%s)...\n", a.Change.Name, a.Change.OldVersion)
		var err error
		if a.Source == "cache" {
			err = pacman.InstallFromCache(a.CachePath)
		} else {
			err = pacman.ReinstallPackage(a.Change.Name, a.Change.OldVersion)
		}
		if err != nil {
			red.Printf("  ✗ Failed to reinstall %s: %v\n", a.Change.Name, err)
			return fmt.Errorf("reinstall %s: %w", a.Change.Name, err)
		}
		green.Printf("  ✓ Reinstalled %s\n", a.Change.Name)
	}

	// Mark session as rolled back
	if err := database.MarkRolledBack(sessionID); err != nil {
		return fmt.Errorf("mark rolled back: %w", err)
	}

	fmt.Println()
	green.Println("  ✓ Rollback complete.")
	return nil
}
