// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package securitylog

import (
	"testing"
)

func TestClassifyProbeSignatures(t *testing.T) {
	tests := []struct {
		name      string
		entry     Entry
		wantLabel string
	}{
		{
			name:      "sql_injection_union_select",
			entry:     Entry{Path: "/v1/models", Detail: "id=1' UNION SELECT 1,2--"},
			wantLabel: "SQL injection attempt",
		},
		{
			name:      "sql_injection_or_1_equals_1",
			entry:     Entry{Path: "/api/keys?name=x'%20OR%201=1--"},
			wantLabel: "SQL injection attempt",
		},
		{
			name:      "sql_injection_sleep",
			entry:     Entry{Path: "/v1/chat/completions", Detail: "sleep(5)"},
			wantLabel: "SQL injection attempt",
		},
		{
			name:      "script_injection_script_tag",
			entry:     Entry{Path: "/<script>alert(1)</script>"},
			wantLabel: "Script injection attempt",
		},
		{
			name:      "script_injection_onerror",
			entry:     Entry{Path: "/img", Detail: "onerror=alert(1)"},
			wantLabel: "Script injection attempt",
		},
		{
			name:      "path_traversal_dot_dot_slash",
			entry:     Entry{Path: "/static/../../etc/passwd"},
			wantLabel: "Path traversal attempt",
		},
		{
			name:      "path_traversal_encoded",
			entry:     Entry{Path: "/static/%2e%2e/proc/self"},
			wantLabel: "Path traversal attempt",
		},
		{
			name:      "path_traversal_win_ini",
			entry:     Entry{Path: "/c:/windows/win.ini"},
			wantLabel: "Path traversal attempt",
		},
		{
			name:      "secret_file_git",
			entry:     Entry{Path: "/..git/config"},
			wantLabel: "Secret file probe",
		},
		{
			name:      "secret_file_id_rsa",
			entry:     Entry{Path: "/root/.ssh/id_rsa"},
			wantLabel: "Secret file probe",
		},
		{
			name:      "secret_file_npmrc",
			entry:     Entry{Path: "/home/user/.npmrc"},
			wantLabel: "Secret file probe",
		},
		{
			name:      "secret_file_dot_env",
			entry:     Entry{Path: "/.env"},
			wantLabel: "Secret file probe",
		},
		{
			name:      "auth_material_cookie",
			entry:     Entry{Path: "/test", Detail: "dump cookie content"},
			wantLabel: "Auth material probe",
		},
		{
			name:      "auth_material_x_api_key",
			entry:     Entry{Path: "/test", Detail: "leak x-api-key"},
			wantLabel: "Auth material probe",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := Classify(tt.entry)
			if v.Level != "critical" {
				t.Errorf("level = %q, want critical", v.Level)
			}
			if v.Classification != "breach" {
				t.Errorf("classification = %q, want breach", v.Classification)
			}
			if v.Label != tt.wantLabel {
				t.Errorf("label = %q, want %q", v.Label, tt.wantLabel)
			}
		})
	}
}

func TestClassifyEvents(t *testing.T) {
	t.Run("auth_flow_detail_password_is_not_probe", func(t *testing.T) {
		// In auth flows, detail mentions password without triggering probe
		e := Entry{Type: "login_failed", Path: "/api/auth/login", Detail: "wrong password"}
		v := Classify(e)
		if v.Level != "warn" || v.Classification != "suspicious" || v.Label != "Failed sign-in" {
			t.Errorf("unexpected verdict for login_failed: %+v", v)
		}
	})

	t.Run("login_default_password", func(t *testing.T) {
		e := Entry{Type: "login_default_password", Detail: "mustChangePassword"}
		v := Classify(e)
		if v.Level != "critical" || v.Classification != "breach" || v.Label != "Signed in with the published default password" {
			t.Errorf("unexpected verdict for default password: %+v", v)
		}
	})

	t.Run("login_success", func(t *testing.T) {
		e := Entry{Type: "login_success"}
		v := Classify(e)
		if v.Level != "info" || v.Classification != "normal" || v.Label != "Password sign-in" {
			t.Errorf("unexpected verdict for login_success: %+v", v)
		}
	})

	t.Run("apikey_login_success", func(t *testing.T) {
		e := Entry{Type: "apikey_login_success"}
		v := Classify(e)
		if v.Level != "info" || v.Classification != "normal" || v.Label != "Signed in with an API key" {
			t.Errorf("unexpected verdict for apikey_login_success: %+v", v)
		}
	})

	t.Run("status_401_or_403", func(t *testing.T) {
		e401 := Entry{Type: "apikey_auth_failed", Status: 401}
		v401 := Classify(e401)
		if v401.Level != "warn" || v401.Classification != "suspicious" || v401.Label != "Refused request" {
			t.Errorf("unexpected verdict for 401: %+v", v401)
		}

		e403 := Entry{Type: "permission_denied", Status: 403}
		v403 := Classify(e403)
		if v403.Level != "warn" || v403.Classification != "suspicious" || v403.Label != "Refused request" {
			t.Errorf("unexpected verdict for 403: %+v", v403)
		}
	})

	t.Run("admin_changes", func(t *testing.T) {
		e := Entry{Type: "key_created", Detail: "Created key sk-123"}
		v := Classify(e)
		if v.Level != "info" || v.Classification != "normal" || v.Label != "Created key sk-123" {
			t.Errorf("unexpected verdict for key_created: %+v", v)
		}
	})
}
