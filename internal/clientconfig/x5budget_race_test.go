//go:build race

// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package clientconfig

import "time"

// x5Budget under the race detector. See x5budget_norace_test.go.
const x5Budget = 30 * time.Second
