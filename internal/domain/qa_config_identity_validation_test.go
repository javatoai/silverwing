package domain

import "testing"

func TestQAConfigRejectsDuplicateRetainedIdentities(t *testing.T) {
	withRef := func(value string) *string { return &value }
	validSkill := func(id, repository, ref, root string) SkillSource {
		return SkillSource{ID: id, Name: id, RepositoryURL: repository, Ref: withRef(ref), SkillRoot: &root}
	}
	validMarketplace := func(id, repository, ref, directory string) CodexPluginMarketplaceSource {
		return CodexPluginMarketplaceSource{ID: id, Name: id, RepositoryURL: repository, Ref: withRef(ref), MarketplaceDirectory: directory}
	}
	tests := []struct {
		name   string
		mutate func(*AppConfig)
	}{
		{
			name: "trimmed group name ignoring case",
			mutate: func(config *AppConfig) {
				config.Groups = append(config.Groups, GroupConfig{ID: "secondary", Name: " 默认组 "})
			},
		},
		{
			name: "exact Meegle project key",
			mutate: func(config *AppConfig) {
				config.MeegleProjects = []MeegleProjectConfig{{ProjectKey: "Transit", SimpleName: "a"}, {ProjectKey: "Transit", SimpleName: "b"}}
			},
		},
		{
			name: "Skill source ID ignoring case",
			mutate: func(config *AppConfig) {
				config.SkillSources = []SkillSource{validSkill("Team", "https://example.com/team/one", "main", "."), validSkill("team", "https://example.com/team/two", "main", ".")}
			},
		},
		{
			name: "normalized Skill source key ignoring case",
			mutate: func(config *AppConfig) {
				config.SkillSources = []SkillSource{
					validSkill("first", "https://Example.com/org/skills.git", "Main", "Skills"),
					validSkill("second", "https://example.com/org/skills/", "main", "skills"),
				}
			},
		},
		{
			name: "plugin marketplace ID ignoring case",
			mutate: func(config *AppConfig) {
				config.CodexPluginMarketplaceSources = []CodexPluginMarketplaceSource{
					validMarketplace("Team", "https://example.com/team/one", "main", "."),
					validMarketplace("team", "https://example.com/team/two", "main", "."),
				}
			},
		},
		{
			name: "normalized plugin marketplace key ignoring case",
			mutate: func(config *AppConfig) {
				config.CodexPluginMarketplaceSources = []CodexPluginMarketplaceSource{
					validMarketplace("first", "https://Example.com/org/plugins.git", "Main", "Marketplace"),
					validMarketplace("second", "https://example.com/org/plugins/", "main", "marketplace"),
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := DefaultAppConfig()
			test.mutate(&config)
			if err := config.Validate(); err == nil {
				t.Fatal("duplicate retained identity was accepted")
			}
		})
	}
}

func TestQAConfigRetainedIdentityValidationKeepsDistinctValuesValid(t *testing.T) {
	main, docs := "main", "docs"
	code, product := "code", "product"
	config := DefaultAppConfig()
	config.Groups = append(config.Groups, GroupConfig{ID: "secondary", Name: "产品"})
	config.MeegleProjects = []MeegleProjectConfig{{ProjectKey: "Transit", SimpleName: "transit"}, {ProjectKey: "transit", SimpleName: "transit-lower"}}
	config.SkillSources = []SkillSource{
		{ID: "team", Name: "team", RepositoryURL: "https://example.com/org/skills", Ref: &main, SkillRoot: &code},
		{ID: "docs", Name: "docs", RepositoryURL: "https://example.com/org/skills", Ref: &docs, SkillRoot: &code},
	}
	config.CodexPluginMarketplaceSources = []CodexPluginMarketplaceSource{
		{ID: "team", Name: "team", RepositoryURL: "https://example.com/org/plugins", Ref: &main, MarketplaceDirectory: "catalog"},
		{ID: "product", Name: "product", RepositoryURL: "https://example.com/org/plugins", Ref: &product, MarketplaceDirectory: "catalog"},
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("distinct valid retained identities rejected: %v", err)
	}
}
