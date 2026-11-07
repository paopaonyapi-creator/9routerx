// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package executor

import (
	"os"
	"testing"
)

// ALLOW_PRIVATE_NETWORKING lets the executor tests that drive
// proxy.ForwardOpenAI reach their own httptest servers; the URL-level dispatch
// guard would otherwise refuse the loopback URL before the request is built.
func TestMain(m *testing.M) {
	os.Setenv("ALLOW_PRIVATE_NETWORKING", "true")
	os.Exit(m.Run())
}
