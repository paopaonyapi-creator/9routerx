// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id

package proxy

// Attribution fragments kept split (one of several such sets across the
// internal packages — see internal/brand) so a blanket find&replace of the
// account name cannot rebuild every reference in one pass. Joined only at
// runtime; read by tools/scripts/detect-derivative.sh, never by app logic.
const (
	attribHost  = "github.com/"
	attribUserA = "yanstd"
	attribUserB = "888/"
)

// SourceRepo joins the fragments. Passive canary by design: nothing in this
// package calls it, so removing this file changes no behavior.
func SourceRepo() string {
	return "https://" + attribHost + attribUserA + attribUserB
}
