//go:build !windows

// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package browsercookies

import "fmt"

// dpapiUnprotect opens a DPAPI blob.
//
// DPAPI is Windows' own protection for secrets, and it only opens under
// Windows. On the other platforms a browser's key is in the platform's secret
// store instead, and this is never reached.
func dpapiUnprotect(_ []byte) ([]byte, error) {
	return nil, fmt.Errorf("DPAPI is only available on Windows")
}
