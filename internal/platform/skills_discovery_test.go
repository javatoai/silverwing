package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
)

func completionSkillSource() domain.SkillSource {
	return domain.SkillSource{ID: "example-source", Name: "Example source", RepositoryURL: "https://example.invalid/skills.git"}
}

func writeCompletionSkill(t *testing.T, root, relative, name string) string {
	t.Helper()
	directory := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, skillFileName), []byte("---\nname: "+name+"\ndescription: Synthetic skill\n---\nBody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return directory
}

func TestSkillDiscoveryNestedBoundariesAndRelativePaths(t *testing.T) {
	root := t.TempDir()
	deep := "skills/" + strings.Repeat("category/", 24) + "example"
	writeCompletionSkill(t, root, deep, "example")
	// A nested SKILL.md is a resource once its parent's skill boundary is found.
	writeCompletionSkill(t, root, deep+"/examples/embedded", "embedded")
	writeCompletionSkill(t, root, ".agents/skills/language/go/tools", "go-tools")
	skills, roots, err := discoverSkillsWithContext(context.Background(), completionSkillSource(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 2 || skills[0].Name != "example" || skills[0].SourcePath != deep || skills[1].SourcePath != ".agents/skills/language/go/tools" {
		t.Fatalf("nested discovery or boundary changed: %+v", skills)
	}
	if strings.Join(roots, ",") != ".agents/skills,skills" {
		t.Fatal("discovery root identity changed", roots)
	}
	configured := completionSkillSource()
	configured.SkillRoot = stringPtr("skills/category")
	skills, roots, err = discoverSkillsWithContext(context.Background(), configured, root)
	if err != nil || len(skills) != 1 || skills[0].SourcePath != deep || strings.Join(roots, ",") != "skills/category" {
		t.Fatalf("configured root lost checkout-relative paths: %+v %v %v", skills, roots, err)
	}
}

func TestSkillDiscoveryRootSkillAndMetadataFailures(t *testing.T) {
	for _, scenario := range []string{"root-boundary", "invalid-description", "duplicate", "case-collision"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			source := completionSkillSource()
			switch scenario {
			case "root-boundary":
				source.SkillRoot = stringPtr(".")
				writeCompletionSkill(t, root, ".", "root-skill")
				writeCompletionSkill(t, root, "skills/hidden", "hidden")
			case "invalid-description":
				directory := writeCompletionSkill(t, root, "skills/category/invalid", "invalid")
				if err := os.WriteFile(filepath.Join(directory, skillFileName), []byte("---\nname: invalid\n---\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "duplicate":
				writeCompletionSkill(t, root, "skills/first/one", "same")
				writeCompletionSkill(t, root, "skills/second/two", "same")
			case "case-collision":
				writeCompletionSkill(t, root, "skills/first/one", "Same")
				writeCompletionSkill(t, root, "skills/second/two", "same")
			}
			skills, _, err := discoverSkillsWithContext(context.Background(), source, root)
			if scenario == "root-boundary" {
				if err != nil || len(skills) != 1 || skills[0].Name != "root-skill" || skills[0].SourcePath != "." {
					t.Fatal(skills, err)
				}
			} else if err == nil || skills != nil {
				t.Fatal("invalid/colliding discovery returned a partial catalog", skills, err)
			}
		})
	}
}

type discoveryCanceledContext struct {
	context.Context
	remaining int
}

func (c *discoveryCanceledContext) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}

func TestSkillDiscoveryCancellation(t *testing.T) {
	root := t.TempDir()
	writeCompletionSkill(t, root, "skills/"+strings.Repeat("deep/", 40)+"example", "example")
	ctx := &discoveryCanceledContext{Context: context.Background(), remaining: 12}
	if skills, _, err := discoverSkillsWithContext(ctx, completionSkillSource(), root); !errors.Is(err, context.Canceled) || skills != nil {
		t.Fatal("walk did not observe cancellation atomically", skills, err)
	}
}

func TestSkillDiscoveryLinkBoundary(t *testing.T) {
	outside := t.TempDir()
	writeCompletionSkill(t, outside, "example", "outside")
	linked := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(linked, "skills")); err != nil {
		t.Skip("symlink privilege unavailable:", err)
	}
	if _, _, err := discoverSkillsWithContext(context.Background(), completionSkillSource(), linked); err == nil {
		t.Fatal("linked discovery root escaped checkout")
	}
}
