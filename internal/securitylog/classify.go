// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package securitylog

import (
	"net/url"
	"regexp"
	"strings"
)

// ProbeSignature pairs a scanner regex with a descriptive label.
type ProbeSignature struct {
	Pattern *regexp.Regexp
	Label   string
}

// ProbeSignatures matches suspicious patterns in request paths and query/body details,
// ported directly from serenhope PROBE_SIGNATURES in securityLogRepo.js.
var ProbeSignatures = []ProbeSignature{
	{Pattern: regexp.MustCompile(`(?i)\b(select|insert|drop|union|or\s+1\s*=\s*1|sleep\(|benchmark\()`), Label: "SQL injection attempt"},
	{Pattern: regexp.MustCompile(`(?i)<script|javascript:|onerror\s*=|onload\s*=`), Label: "Script injection attempt"},
	{Pattern: regexp.MustCompile(`(?i)\.\./|%2e%2e|%252e|/etc/passwd|/proc/self|win\.ini`), Label: "Path traversal attempt"},
	{Pattern: regexp.MustCompile(`(?i)\.\.(?:git|env|aws|ssh)|id_rsa|\.npmrc`), Label: "Secret file probe"},
	{Pattern: regexp.MustCompile(`(?i)(?:^|/)\.env\b`), Label: "Secret file probe"},
	{Pattern: regexp.MustCompile(`(?i)authorization|x-api-key|cookie|passwd|password`), Label: "Auth material probe"},
}

var (
	authFlowRe        = regexp.MustCompile(`^(login|apikey_login|apikey_logout|logout)`)
	defaultPasswordRe = regexp.MustCompile(`(?i)default password|mustChangePassword`)
)

// Verdict is the classification result for a security event.
type Verdict struct {
	Level          string `json:"level"`          // "info", "warn", "critical"
	Classification string `json:"classification"` // "normal", "suspicious", "breach"
	Label          string `json:"label"`
}

// MatchProbe reports if a path or detail matches any known probe signature.
// It checks both the raw scope and its URL-unescaped variant so percent-encoded
// attacks (e.g. %20OR%201=1) cannot evade detection.
func MatchProbe(scope string) (bool, string) {
	unescaped, _ := url.QueryUnescape(scope)
	for _, sig := range ProbeSignatures {
		if sig.Pattern.MatchString(scope) || (unescaped != "" && unescaped != scope && sig.Pattern.MatchString(unescaped)) {
			return true, sig.Label
		}
	}
	return false, ""
}

// Classify scores a security event into normal, suspicious, or breach.
// Parity with serenhope classify() in securityLogRepo.js.
func Classify(e Entry) Verdict {
	isAuthFlow := authFlowRe.MatchString(e.Type)
	probeScope := strings.TrimSpace(e.Path + " " + e.Detail)
	if isAuthFlow {
		probeScope = e.Path
	}

	if match, label := MatchProbe(probeScope); match {
		return Verdict{Level: "critical", Classification: "breach", Label: label}
	}

	if e.Severity == "critical" {
		lbl := e.Detail
		if lbl == "" {
			lbl = "Security-critical action"
		}
		return Verdict{Level: "critical", Classification: "breach", Label: lbl}
	}

	if e.Type == "login_failed" || e.Status == 401 || e.Status == 403 {
		lbl := "Refused request"
		if e.Type == "login_failed" {
			lbl = "Failed sign-in"
		}
		return Verdict{Level: "warn", Classification: "suspicious", Label: lbl}
	}

	if e.Type == "login_default_password" || defaultPasswordRe.MatchString(e.Detail) {
		return Verdict{Level: "critical", Classification: "breach", Label: "Signed in with the published default password"}
	}

	if e.Type == "login_success" || e.Type == "apikey_login_success" {
		lbl := "Password sign-in"
		if e.Type == "apikey_login_success" {
			lbl = "Signed in with an API key"
		}
		return Verdict{Level: "info", Classification: "normal", Label: lbl}
	}

	if e.Type == "logout" || e.Type == "apikey_logout" {
		return Verdict{Level: "info", Classification: "normal", Label: "Signed out"}
	}

	if e.Type == "permission_denied" {
		return Verdict{Level: "warn", Classification: "suspicious", Label: "Refused request"}
	}

	if e.Type == "key_created" || e.Type == "key_deleted" || e.Type == "settings_changed" {
		lbl := e.Detail
		if lbl == "" {
			lbl = "Administrative change"
		}
		return Verdict{Level: "info", Classification: "normal", Label: lbl}
	}

	lbl := e.Detail
	if lbl == "" {
		lbl = e.Type
	}
	return Verdict{Level: "info", Classification: "normal", Label: lbl}
}
