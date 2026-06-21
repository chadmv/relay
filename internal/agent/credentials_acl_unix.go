//go:build !windows

package agent

// secureTokenFile tightens the OS-level access controls on the token file at
// path. On Unix the file is already created with 0600 by Persist's os.WriteFile,
// which is the correct and sufficient protection, so this is a no-op. It exists
// to match the Windows build's signature (see credentials_acl_windows.go), where
// 0600 carries no ACL meaning and an explicit DACL must be applied.
func secureTokenFile(path string) error {
	return nil
}
