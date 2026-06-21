//go:build windows

package agent

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// tokenFileSDDL is the security descriptor applied to the persisted agent token
// on Windows. It defines a PROTECTED DACL (the "P" flag), so the token file does
// NOT inherit C:\ProgramData's broad ACL (which typically grants BUILTIN\Users
// read access). It grants FILE_ALL_ACCESS ("FA") to:
//   - OW: the file Owner (the creating principal), so the agent can always read
//     its own token regardless of whether it runs as LocalSystem, a service
//     account, or an interactive user.
//   - SY: LocalSystem.
//   - BA: the Administrators built-in group.
const tokenFileSDDL = "D:PAI(A;;FA;;;OW)(A;;FA;;;SY)(A;;FA;;;BA)"

// secureTokenFile applies tokenFileSDDL's DACL to the token file at path,
// replacing whatever DACL it inherited at creation. On Windows the 0600 mode
// passed to os.WriteFile is meaningless, so this is the real protection. It is
// applied on every Persist (replacing, not merging, the DACL) so an overwrite of
// a token written by an older build also ends up correctly restricted. Any
// failure is returned so Persist treats it as fatal rather than leaving a
// token with a broad inherited DACL.
func secureTokenFile(path string) error {
	sd, err := windows.SecurityDescriptorFromString(tokenFileSDDL)
	if err != nil {
		return fmt.Errorf("parse token file SDDL: %w", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("extract DACL: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, // owner unchanged
		nil, // group unchanged
		dacl,
		nil, // sacl unchanged
	); err != nil {
		return fmt.Errorf("apply DACL to %s: %w", path, err)
	}
	return nil
}
