// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package providers

import (
	"testing"
)

func TestMimocodeProviderCatalogAndAliases(t *testing.T) {
	// 1. Alias resolution
	for _, alias := range []string{"mimocode", "mimocode-free", "mmf", "mimo-free"} {
		canonical := ResolveAlias(alias)
		if canonical != "mimo-free" {
			t.Errorf("ResolveAlias(%q) = %q, want 'mimo-free'", alias, canonical)
		}
	}

	// 2. Registry alias mapping
	if alias := RegistryAliases["mimocode"]; alias != "mmf" {
		t.Errorf("RegistryAliases['mimocode'] = %q, want 'mmf'", alias)
	}

	// 3. Provider catalog definition
	cfg, ok := KnownProviders["mimocode"]
	if !ok {
		t.Fatal("KnownProviders missing 'mimocode'")
	}
	if cfg.BaseURL == "" || !cfg.NoAuth {
		t.Errorf("unexpected mimocode config: %+v", cfg)
	}

	// 4. Provider models
	models := GetProviderModels("mimo-free")
	if len(models) == 0 {
		t.Errorf("expected models for mimo-free, got none")
	}
}
