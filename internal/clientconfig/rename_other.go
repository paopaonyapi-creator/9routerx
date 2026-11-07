//go:build !windows

// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package clientconfig

// isRetryableRenameErr is Windows-only. POSIX rename fails for reasons that do
// not clear on their own — EACCES, ENOSPC, EXDEV — so retrying would only delay
// the error the caller needs.
func isRetryableRenameErr(err error) bool { return false }
