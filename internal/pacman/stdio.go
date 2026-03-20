package pacman

import "os"

func openStdin() *os.File  { return os.Stdin }
func openStdout() *os.File { return os.Stdout }
func openStderr() *os.File { return os.Stderr }
