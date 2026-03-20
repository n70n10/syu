package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/fatih/color"
	"github.com/n70n10/syu/internal/db"
	"github.com/n70n10/syu/internal/pacman"
	"github.com/n70n10/syu/internal/rollback"
	"github.com/n70n10/syu/internal/ui"
)

const version = "1.0.0"

// readOnlyCommands are subcommands that don't require root.
var readOnlyCommands = map[string]bool{
	"list": true, "ls": true,
	"info": true, "show": true,
	"delta": true, "diff": true,
	"history": true, "pkg": true,
	"stats": true, "cache": true,
	"help": true, "--help": true, "-h": true,
	"--version": true, "-v": true, "version": true,
}

func main() {
	args := os.Args[1:]

	// No subcommand: print help and exit cleanly.
	if len(args) == 0 {
		printHelp()
		os.Exit(0)
	}

	// Enforce root for write operations.
	if os.Geteuid() != 0 && !readOnlyCommands[args[0]] {
		ui.Errorf("syu must be run as root (use sudo)")
		os.Exit(1)
	}

	database, err := db.Open(db.DefaultDBPath)
	if err != nil {
		ui.Errorf("Cannot open database: %v", err)
		os.Exit(1)
	}
	defer database.Close()

	switch args[0] {
	case "upgrade", "up":
		cmdUpgrade(database, args[1:])

	case "rollback", "undo":
		cmdRollback(database, args[1:])

	case "list", "ls":
		cmdList(database, args[1:])

	case "info", "show":
		cmdInfo(database, args[1:])

	case "delta", "diff":
		cmdDelta(database, args[1:])

	case "history", "pkg":
		cmdHistory(database, args[1:])

	case "stats":
		cmdStats(database)

	case "cache":
		cmdCache()

	case "prune":
		cmdPrune(database, args[1:])

	case "help", "--help", "-h":
		printHelp()

	case "--version", "-v", "version":
		fmt.Printf("syu %s\n", version)

	default:
		ui.Errorf("Unknown command %q. Run 'syu help' for usage.", args[0])
		os.Exit(1)
	}
}

// ─── upgrade ──────────────────────────────────────────────────────────────────

func cmdUpgrade(database *db.DB, extraArgs []string) {
	ui.Header("syu — System Upgrade")

	ui.Infof("Snapshotting current package state...")
	before, err := pacman.Snapshot()
	if err != nil {
		ui.Errorf("Failed to snapshot packages: %v", err)
		os.Exit(1)
	}
	ui.Successf("%d packages recorded", len(before))
	fmt.Println()

	// Run pacman -Syu — live output goes to terminal
	if err := pacman.RunUpgrade(extraArgs); err != nil {
		ui.Errorf("pacman exited with error: %v", err)
		// Still try to record what changed (partial upgrade)
	}

	fmt.Println()
	ui.Infof("Snapshotting post-upgrade state...")
	after, err := pacman.Snapshot()
	if err != nil {
		ui.Errorf("Failed to snapshot after upgrade: %v", err)
		os.Exit(1)
	}

	changes := pacman.Diff(before, after)

	if len(changes) == 0 {
		ui.Infof("System was already up to date. No changes recorded.")
		return
	}

	sessionID, err := database.CreateSession("")
	if err != nil {
		ui.Errorf("Failed to create session: %v", err)
		os.Exit(1)
	}

	if err := database.RecordChanges(sessionID, changes); err != nil {
		ui.Errorf("Failed to record changes: %v", err)
		os.Exit(1)
	}

	// Print summary
	var upgraded, installed, removed, downgraded int
	for _, c := range changes {
		switch c.ChangeType {
		case "upgraded":
			upgraded++
		case "installed":
			installed++
		case "removed":
			removed++
		case "downgraded":
			downgraded++
		}
	}

	fmt.Println()
	ui.Successf("Session #%d recorded:", sessionID)
	if upgraded > 0 {
		color.Yellow("      ↑ %d upgraded", upgraded)
	}
	if installed > 0 {
		color.Green("      + %d installed", installed)
	}
	if removed > 0 {
		color.Red("      ✗ %d removed", removed)
	}
	if downgraded > 0 {
		color.Cyan("      ↓ %d downgraded", downgraded)
	}
	fmt.Printf("\n  Run 'syu rollback' to undo this upgrade.\n\n")
}

// ─── rollback ─────────────────────────────────────────────────────────────────

func cmdRollback(database *db.DB, args []string) {
	dryRun := false
	var sessionID int64 = -1

	for _, arg := range args {
		switch arg {
		case "--dry-run", "-n":
			dryRun = true
		default:
			id, err := strconv.ParseInt(arg, 10, 64)
			if err != nil {
				ui.Errorf("Invalid session ID %q", arg)
				os.Exit(1)
			}
			sessionID = id
		}
	}

	var session *db.Session
	var err error

	if sessionID > 0 {
		session, err = database.GetSession(sessionID)
	} else {
		session, err = database.LatestSession()
	}
	if err != nil {
		ui.Errorf("Cannot load session: %v", err)
		os.Exit(1)
	}

	if session.Status == "rolled_back" {
		ui.Errorf("Session #%d has already been rolled back.", session.ID)
		os.Exit(1)
	}

	ui.Header(fmt.Sprintf("Rollback — Session #%d (%s)",
		session.ID, session.Timestamp.Local().Format("2006-01-02 15:04")))

	if len(session.Changes) == 0 {
		ui.Infof("Session #%d has no recorded changes.", session.ID)
		return
	}

	ui.Infof("Resolving rollback plan...")
	plan, err := rollback.BuildPlan(session)
	if err != nil {
		ui.Errorf("Failed to build rollback plan: %v", err)
		os.Exit(1)
	}

	rollback.PrintPlan(plan)

	if rollback.HasUnresolvable(plan) {
		ui.Errorf("Cannot proceed — some packages are unresolvable.")
		os.Exit(1)
	}

	if dryRun {
		ui.Infof("Dry run — no changes made.")
		return
	}

	if !rollback.Confirm(false) {
		ui.Infof("Rollback cancelled.")
		return
	}

	if err := rollback.Execute(plan, database, session.ID); err != nil {
		ui.Errorf("Rollback failed: %v", err)
		os.Exit(1)
	}
}

// ─── list ─────────────────────────────────────────────────────────────────────

func cmdList(database *db.DB, args []string) {
	limit := 20
	for _, arg := range args {
		if n, err := strconv.Atoi(arg); err == nil {
			limit = n
		}
	}

	ui.Header("Upgrade History")
	sessions, err := database.ListSessions(limit)
	if err != nil {
		ui.Errorf("Failed to list sessions: %v", err)
		os.Exit(1)
	}
	ui.PrintSessionList(sessions)
	fmt.Println()
}

// ─── info ─────────────────────────────────────────────────────────────────────

func cmdInfo(database *db.DB, args []string) {
	if len(args) == 0 {
		session, err := database.LatestSession()
		if err != nil {
			ui.Errorf("%v", err)
			os.Exit(1)
		}
		ui.PrintSessionDetail(session)
		return
	}

	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		ui.Errorf("Expected a session ID, got %q", args[0])
		os.Exit(1)
	}
	session, err := database.GetSession(id)
	if err != nil {
		ui.Errorf("%v", err)
		os.Exit(1)
	}
	ui.PrintSessionDetail(session)
}

// ─── delta ────────────────────────────────────────────────────────────────────

func cmdDelta(database *db.DB, args []string) {
	if len(args) < 2 {
		// Default: compare last two sessions
		sessions, err := database.ListSessions(2)
		if err != nil || len(sessions) < 2 {
			ui.Errorf("Need at least two sessions. Specify: syu delta <id1> <id2>")
			os.Exit(1)
		}
		// sessions[0] is newest, sessions[1] is older
		s1, _ := database.GetSession(sessions[1].ID)
		s2, _ := database.GetSession(sessions[0].ID)
		ui.PrintDelta(s1, s2)
		return
	}

	id1, err1 := strconv.ParseInt(args[0], 10, 64)
	id2, err2 := strconv.ParseInt(args[1], 10, 64)
	if err1 != nil || err2 != nil {
		ui.Errorf("Expected two session IDs")
		os.Exit(1)
	}
	s1, err := database.GetSession(id1)
	if err != nil {
		ui.Errorf("%v", err)
		os.Exit(1)
	}
	s2, err := database.GetSession(id2)
	if err != nil {
		ui.Errorf("%v", err)
		os.Exit(1)
	}
	ui.PrintDelta(s1, s2)
}

// ─── history ──────────────────────────────────────────────────────────────────

func cmdHistory(database *db.DB, args []string) {
	if len(args) == 0 {
		ui.Errorf("Usage: syu history <package-name>")
		os.Exit(1)
	}
	changes, err := database.PackageHistory(args[0])
	if err != nil {
		ui.Errorf("%v", err)
		os.Exit(1)
	}
	ui.PrintPackageHistory(args[0], changes)
}

// ─── stats ────────────────────────────────────────────────────────────────────

func cmdStats(database *db.DB) {
	stats, err := database.Stats()
	if err != nil {
		ui.Errorf("%v", err)
		os.Exit(1)
	}
	ui.PrintStats(stats)
}

// ─── cache ────────────────────────────────────────────────────────────────────

func cmdCache() {
	count, size, err := pacman.CacheStats()
	if err != nil {
		ui.Errorf("Failed to read cache: %v", err)
		os.Exit(1)
	}
	ui.PrintCacheInfo(count, size)
	if count == 0 {
		color.Yellow("  ⚠  Cache is empty — rollbacks requiring cached packages will fail.\n")
	}
}

// ─── prune ────────────────────────────────────────────────────────────────────

func cmdPrune(database *db.DB, args []string) {
	keepN := 10
	if len(args) > 0 {
		if n, err := strconv.Atoi(args[0]); err == nil {
			keepN = n
		}
	}

	sessions, err := database.ListSessions(0) // all
	if err != nil {
		ui.Errorf("%v", err)
		os.Exit(1)
	}

	if len(sessions) <= keepN {
		ui.Infof("Nothing to prune (%d sessions, keeping %d).", len(sessions), keepN)
		return
	}

	toDelete := sessions[keepN:] // oldest are at the end (list is DESC)
	ui.Infof("Pruning %d old session(s)...", len(toDelete))
	for _, s := range toDelete {
		if err := database.DeleteSession(s.ID); err != nil {
			ui.Errorf("Failed to delete session #%d: %v", s.ID, err)
		} else {
			ui.Successf("Deleted session #%d (%s)", s.ID, s.Timestamp.Local().Format("2006-01-02"))
		}
	}
}

// ─── help ─────────────────────────────────────────────────────────────────────

func printHelp() {
	cyan := color.New(color.FgCyan, color.Bold)
	bold := color.New(color.Bold)
	dim := color.New(color.FgHiBlack)

	fmt.Println()
	cyan.Println("  syu — tracked pacman -Syu with rollback")
	dim.Printf("  version %s\n", version)
	fmt.Println()
	bold.Println("  USAGE")
	fmt.Println()
	fmt.Println("    sudo syu up / upgrade     Run pacman -Syu and record changes")
	fmt.Println("    sudo syu rollback [ID]        Roll back the last (or specified) session")
	fmt.Println("    sudo syu rollback --dry-run   Preview rollback without applying")
	fmt.Println()
	bold.Println("  COMMANDS")
	fmt.Println()
	fmt.Println("    list   [N]          Show last N upgrade sessions (default: 20)")
	fmt.Println("    info   [ID]         Show detailed changes for a session (default: latest)")
	fmt.Println("    delta  [ID1] [ID2]  Compare package changes between two sessions")
	fmt.Println("    history <pkg>       Show full version history for a package")
	fmt.Println("    stats               Show database statistics")
	fmt.Println("    cache               Show pacman cache size and file count")
	fmt.Println("    prune  [N]          Delete old sessions, keeping the most recent N (default: 10)")
	fmt.Println()
	bold.Println("  ROLLBACK BEHAVIOUR")
	fmt.Println()
	fmt.Println("    · Upgraded packages  → downgraded to their previous version")
	fmt.Println("    · Newly installed    → removed from the system")
	fmt.Println("    · Removed packages   → reinstalled at their previous version")
	fmt.Println("    · If any target is missing from both cache and repos → aborts cleanly")
	fmt.Println()
	bold.Println("  DATA")
	fmt.Println()
	fmt.Printf("    Database: %s\n", db.DefaultDBPath)
	fmt.Println("    Packages are resolved from /var/cache/pacman/pkg first,")
	fmt.Println("    then official repositories.")
	fmt.Println()
}
