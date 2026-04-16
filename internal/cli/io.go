// internal/cli/io.go
package cli

import "os"

func stdinReader() *os.File  { return os.Stdin }
func stderrWriter() *os.File { return os.Stderr }
