// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package media

import (
	"os"
	"testing"

	"9routerx/proxy/internal/testnet"
)

// ALLOW_PRIVATE_NETWORKING lets the handler tests reach their own httptest
// servers; testnet.Seal keeps those servers the only reachable destination.
func TestMain(m *testing.M) {
	os.Setenv("ALLOW_PRIVATE_NETWORKING", "true")
	testnet.Seal()
	os.Exit(m.Run())
}
