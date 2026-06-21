//go:build windows

package agent

import (
	"path/filepath"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// aceSID returns the trustee SID of an allowed ACE by computing the address of
// its variable-length SidStart field.
func aceSID(ace *windows.ACCESS_ALLOWED_ACE) *windows.SID {
	return (*windows.SID)(unsafe.Pointer(&ace.SidStart))
}

// readSecurityDescriptor reads back the security descriptor of the file at path,
// including its owner, DACL, and control bits.
func readSecurityDescriptor(t *testing.T, path string) *windows.SECURITY_DESCRIPTOR {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	require.NoError(t, err, "GetNamedSecurityInfo")
	return sd
}

// allowedACEs returns the allowed ACEs of the given security descriptor's DACL.
func allowedACEs(t *testing.T, sd *windows.SECURITY_DESCRIPTOR) []*windows.ACCESS_ALLOWED_ACE {
	t.Helper()
	dacl, _, err := sd.DACL()
	require.NoError(t, err, "read DACL")
	require.NotNil(t, dacl, "DACL must be present")

	var aces []*windows.ACCESS_ALLOWED_ACE
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		require.NoError(t, windows.GetAce(dacl, i, &ace), "GetAce(%d)", i)
		if ace.Header.AceType == windows.ACCESS_ALLOWED_ACE_TYPE {
			aces = append(aces, ace)
		}
	}
	return aces
}

func TestPersist_AppliesRestrictiveDACL_Windows(t *testing.T) {
	dir := t.TempDir()
	c, err := LoadCredentials(dir)
	require.NoError(t, err)
	require.NoError(t, c.Persist("secret-agent-token"))

	path := filepath.Join(dir, "token")
	sd := readSecurityDescriptor(t, path)
	aces := allowedACEs(t, sd)
	require.NotEmpty(t, aces, "expected at least one allowed ACE")

	// (a) The DACL must be PROTECTED: secureTokenFile applies the "P" flag so the
	// token file does NOT inherit its parent directory's broad ACL. An inherited
	// DACL (what a tempdir file gets if secureTokenFile is a no-op) is not
	// protected, so this assertion alone distinguishes "fix applied" from "fix
	// absent" without needing ProgramData's broad inherited DACL.
	control, _, err := sd.Control()
	require.NoError(t, err, "read SD control bits")
	require.NotZero(t, control&windows.SE_DACL_PROTECTED,
		"token file DACL must be protected (SE_DACL_PROTECTED); an inherited DACL is not")

	// (b) The DACL must contain exactly three allow-ACEs, whose SIDs are the
	// principals named by tokenFileSDDL: the OW token, LocalSystem (SY), and
	// BUILTIN\Administrators (BA). Any extra or missing ACE means the wrong DACL
	// is live.
	//
	// The "OW" SDDL token resolves to the well-known "Owner Rights" SID (S-1-3-4),
	// not the literal owner SID: applied via SetNamedSecurityInfo this writes an
	// ACE for S-1-3-4, which grants access to whoever currently owns the file.
	// That is what lets the owner read the token back (checked in (d)) without
	// baking a specific account SID into the DACL. We assert S-1-3-4 directly
	// because that is the SID the applied SDDL actually produces.
	ownerRights, err := windows.StringToSid("S-1-3-4")
	require.NoError(t, err, "StringToSid(Owner Rights)")
	localSystem, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	require.NoError(t, err, "CreateWellKnownSid(WinLocalSystemSid)")
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	require.NoError(t, err, "CreateWellKnownSid(WinBuiltinAdministratorsSid)")

	want := []*windows.SID{ownerRights, localSystem, administrators}
	require.Len(t, aces, len(want),
		"token file DACL must have exactly %d allow-ACEs (Owner, LocalSystem, Administrators)", len(want))
	for _, sid := range want {
		found := false
		for _, ace := range aces {
			if sid.Equals(aceSID(ace)) {
				found = true
				break
			}
		}
		require.True(t, found, "expected allow-ACE for SID %s in the token DACL", sid)
	}

	// (c) No broad principal (Everyone / Authenticated Users / BUILTIN\Users) may
	// have any granted access.
	broad := []windows.WELL_KNOWN_SID_TYPE{
		windows.WinWorldSid,             // Everyone
		windows.WinAuthenticatedUserSid, // Authenticated Users
		windows.WinBuiltinUsersSid,      // BUILTIN\Users
	}
	for _, wk := range broad {
		sid, err := windows.CreateWellKnownSid(wk)
		require.NoError(t, err, "CreateWellKnownSid(%d)", wk)
		for _, ace := range aces {
			require.False(t, sid.Equals(aceSID(ace)),
				"broad principal (well-known SID type %d) must not appear in the token DACL", wk)
		}
	}

	// (d) The current process can still read the token it just wrote. This
	// exercises the owner-full-control ("OW") ACE end to end.
	c2, err := LoadCredentials(dir)
	require.NoError(t, err, "owner must be able to read the token back")
	require.Equal(t, "secret-agent-token", c2.AgentToken())
}
