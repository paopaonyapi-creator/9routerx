//go:build !windows

// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package schedule

// windowsTimeZoneKeyName is the non-Windows stub of the registry read. The
// registry does not exist off Windows, and the caller already guards on
// runtime.GOOS, so the answer here is always "no zone".
func windowsTimeZoneKeyName() string { return "" }
