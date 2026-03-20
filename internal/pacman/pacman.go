package pacman

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"github.com/n70n10/syu/internal/db"
)

// Snapshot captures the current state of all installed packages.
// Returns map[name]version.
func Snapshot() (map[string]string, error) {
	out, err := exec.Command("pacman", "-Q").Output()
	if err != nil {
		return nil, fmt.Errorf("pacman -Q: %w", err)
	}
	pkgs := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			pkgs[parts[0]] = parts[1]
		}
	}
	return pkgs, scanner.Err()
}

// Diff computes what changed between before and after snapshots.
func Diff(before, after map[string]string) []db.PackageChange {
	var changes []db.PackageChange

	// Check all packages in 'after'
	for name, newVer := range after {
		oldVer, existed := before[name]
		if !existed {
			changes = append(changes, db.PackageChange{
				Name:       name,
				ChangeType: "installed",
				NewVersion: newVer,
			})
		} else if oldVer != newVer {
			ct := "upgraded"
			if versionLess(newVer, oldVer) {
				ct = "downgraded"
			}
			changes = append(changes, db.PackageChange{
				Name:       name,
				ChangeType: ct,
				OldVersion: oldVer,
				NewVersion: newVer,
			})
		}
	}

	// Packages in 'before' but not 'after' were removed
	for name, oldVer := range before {
		if _, exists := after[name]; !exists {
			changes = append(changes, db.PackageChange{
				Name:       name,
				ChangeType: "removed",
				OldVersion: oldVer,
			})
		}
	}

	return changes
}

// RunUpgrade executes pacman -Syu and streams output to the terminal.
func RunUpgrade(extraArgs []string) error {
	args := append([]string{"-Syu"}, extraArgs...)
	cmd := exec.Command("pacman", args...)
	cmd.Stdin = nil // pacman will handle its own stdin for confirmations
	cmd.Stdout = nil
	cmd.Stderr = nil

	// We want live output — attach to the process's stdio
	cmd.Stdin = openStdin()
	cmd.Stdout = openStdout()
	cmd.Stderr = openStderr()

	return cmd.Run()
}

// FindCachedPkg finds a .pkg.tar.* file in pacman's cache for pkg@version.
// Returns the path or "" if not found.
func FindCachedPkg(name, version string) string {
	// Try common cache location first
	cacheDirs := []string{"/var/cache/pacman/pkg"}
	patterns := []string{
		fmt.Sprintf("%s-%s-*.pkg.tar.zst", name, version),
		fmt.Sprintf("%s-%s-*.pkg.tar.xz", name, version),
		fmt.Sprintf("%s-%s-*.pkg.tar.gz", name, version),
		fmt.Sprintf("%s-%s-*.pkg.tar.bz2", name, version),
	}
	for _, dir := range cacheDirs {
		for _, pat := range patterns {
			out, err := exec.Command("sh", "-c",
				fmt.Sprintf("ls %s/%s 2>/dev/null | head -1", dir, pat)).Output()
			if err == nil && len(strings.TrimSpace(string(out))) > 0 {
				return strings.TrimSpace(string(out))
			}
		}
	}
	return ""
}

// CheckAURAvailable checks if a package version is available from any
// configured repo (standard repos + AUR via yay/paru if present).
// Returns (available bool, source string).
func CheckRepoAvailable(name, version string) (bool, string) {
	// Check official repos
	out, err := exec.Command("pacman", "-Si", name).Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "Version") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 && strings.TrimSpace(parts[1]) == version {
					return true, "repo"
				}
			}
		}
	}
	return false, ""
}

// InstallFromCache installs a package from its local cache file.
func InstallFromCache(path string) error {
	cmd := exec.Command("pacman", "-U", "--noconfirm", path)
	cmd.Stdin = openStdin()
	cmd.Stdout = openStdout()
	cmd.Stderr = openStderr()
	return cmd.Run()
}

// InstallVersion attempts to install pkg=version from repos.
func InstallVersion(name, version string) error {
	spec := fmt.Sprintf("%s=%s", name, version)
	cmd := exec.Command("pacman", "-S", "--noconfirm", spec)
	cmd.Stdin = openStdin()
	cmd.Stdout = openStdout()
	cmd.Stderr = openStderr()
	return cmd.Run()
}

// RemovePackage removes a package without dependency check (for rollback of installs).
func RemovePackage(name string) error {
	cmd := exec.Command("pacman", "-Rdd", "--noconfirm", name)
	cmd.Stdin = openStdin()
	cmd.Stdout = openStdout()
	cmd.Stderr = openStderr()
	return cmd.Run()
}

// ReinstallPackage reinstalls a removed package from repos.
func ReinstallPackage(name, version string) error {
	// Try versioned install first, fall back to latest
	spec := name
	if version != "" {
		spec = fmt.Sprintf("%s=%s", name, version)
	}
	cmd := exec.Command("pacman", "-S", "--noconfirm", spec)
	cmd.Stdin = openStdin()
	cmd.Stdout = openStdout()
	cmd.Stderr = openStderr()
	if err := cmd.Run(); err != nil && version != "" {
		// retry without version pin
		cmd2 := exec.Command("pacman", "-S", "--noconfirm", name)
		cmd2.Stdin = openStdin()
		cmd2.Stdout = openStdout()
		cmd2.Stderr = openStderr()
		return cmd2.Run()
	}
	return nil
}

// CacheStats returns total size and file count in pacman's package cache.
func CacheStats() (count int, sizeBytes int64, err error) {
	out, err := exec.Command("sh", "-c",
		`find /var/cache/pacman/pkg -maxdepth 1 -name "*.pkg.tar.*" 2>/dev/null`).Output()
	if err != nil {
		return 0, 0, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		count++
		du, e2 := exec.Command("stat", "-c", "%s", l).Output()
		if e2 == nil {
			var sz int64
			fmt.Sscanf(strings.TrimSpace(string(du)), "%d", &sz)
			sizeBytes += sz
		}
	}
	return count, sizeBytes, nil
}

// versionLess returns true if a < b using a simple string comparison heuristic.
// For a robust implementation you'd use alpm's vercmp.
func versionLess(a, b string) bool {
	out, err := exec.Command("vercmp", a, b).Output()
	if err != nil {
		return false
	}
	var result int
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &result)
	return result < 0
}
