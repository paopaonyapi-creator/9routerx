//go:build !race

// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package chat

// raceDetectorEnabled reports whether this test binary was built with -race.
// See usage_probe_race_test.go for why the latency scenarios care.
const raceDetectorEnabled = false
