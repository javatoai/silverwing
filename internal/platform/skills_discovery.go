package platform

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"silverwing-go/internal/domain"
)

func discoverSkills(source domain.SkillSource, checkout string) ([]ExternalSkill, []string, error) {
	return discoverSkillsWithContext(context.Background(), source, checkout)
}

func discoverSkillsWithContext(ctx context.Context, source domain.SkillSource, checkout string) ([]ExternalSkill, []string, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	root, err := filepath.Abs(checkout)
	if err != nil {
		return nil, nil, err
	}
	relatives := []string{".agents/skills", "skills"}
	if source.SkillRoot != nil && strings.TrimSpace(*source.SkillRoot) != "" {
		relatives = []string{*source.SkillRoot}
	}
	skills, roots := []ExternalSkill{}, []string{}
	seen := map[string]bool{}
	for _, relative := range relatives {
		candidate, err := safeChild(root, relative)
		if err != nil {
			return nil, nil, err
		}
		info, err := os.Lstat(candidate)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, nil, fmt.Errorf("Skill 发现目录不是普通目录：%s", candidate)
		}
		before := len(skills)
		err = filepath.WalkDir(candidate, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("Skill 来源不允许符号链接：%s", path)
			}
			if !entry.IsDir() {
				return nil
			}
			if path != candidate && strings.EqualFold(entry.Name(), ".git") {
				return filepath.SkipDir
			}
			if !isRegular(filepath.Join(path, skillFileName)) {
				return nil
			}
			item, err := parseSkillDirectoryWithContext(ctx, path, root)
			if err != nil {
				return err
			}
			key := strings.ToLower(item.Name)
			if seen[key] {
				return fmt.Errorf("Skill 来源包含重复名称：%s", item.Name)
			}
			seen[key] = true
			skills = append(skills, item)
			return filepath.SkipDir // Its descendants are resources of this skill.
		})
		if err != nil {
			return nil, nil, err
		}
		if len(skills) > before {
			rel, err := filepath.Rel(root, candidate)
			if err != nil {
				return nil, nil, err
			}
			roots = append(roots, filepath.ToSlash(rel))
		}
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	return skills, roots, nil
}

func parseSkillDirectory(directory, checkout string) (ExternalSkill, error) {
	return parseSkillDirectoryWithContext(context.Background(), directory, checkout)
}

func parseSkillDirectoryWithContext(ctx context.Context, directory, checkout string) (ExternalSkill, error) {
	if err := ensureNoSymlinksBetween(checkout, directory); err != nil {
		return ExternalSkill{}, err
	}
	if err := validateSkillTreeWithContext(ctx, directory); err != nil {
		return ExternalSkill{}, err
	}
	data, err := readExtensionText(filepath.Join(directory, skillFileName), maxSkillBytes)
	if err != nil {
		return ExternalSkill{}, err
	}
	if err := ctx.Err(); err != nil {
		return ExternalSkill{}, err
	}
	front := skillFrontMatter.FindSubmatch(data)
	if len(front) < 2 {
		return ExternalSkill{}, fmt.Errorf("Skill 缺少 YAML frontmatter：%s", directory)
	}
	name, description := frontMatterValue(string(front[1]), "name"), frontMatterValue(string(front[1]), "description")
	if !skillNamePattern.MatchString(name) {
		return ExternalSkill{}, fmt.Errorf("Skill name 不合法：%s", name)
	}
	if description == "" {
		return ExternalSkill{}, fmt.Errorf("Skill description 不能为空：%s", name)
	}
	rel, err := filepath.Rel(checkout, directory)
	if err != nil {
		return ExternalSkill{}, err
	}
	return ExternalSkill{Name: name, Description: description, SourcePath: filepath.ToSlash(rel)}, nil
}
