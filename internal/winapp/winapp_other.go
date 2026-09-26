//go:build !windows

package winapp

// registerIdentity does nothing outside Windows — AUMIDs are a Windows
// concept. Not an error: nothing anywhere depends on this having run on
// macOS or Linux.
func registerIdentity() error { return nil }
