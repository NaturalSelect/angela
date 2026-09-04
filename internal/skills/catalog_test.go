package skills

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCatalog(t *testing.T) {
	t.Parallel()

	active := []*Skill{
		{SkillFilePath: BuiltinPrefix + "angela-config/SKILL.md", Name: "angela-config", Description: "d1", Builtin: true, UserInvocable: true},
		{SkillFilePath: "/home/user/.config/angela/skills/my-skill/SKILL.md", Name: "my-skill", Description: "d2"},
	}

	entries := Catalog(active, nil, "")
	require.Len(t, entries, 2)

	require.Equal(t, "angela-config", entries[0].Name)
	require.Equal(t, BuiltinPrefix+"angela-config/SKILL.md", entries[0].ID)
	require.Equal(t, SourceSystem, entries[0].Source)
	require.True(t, entries[0].UserInvocable)

	require.Equal(t, "my-skill", entries[1].Name)
	require.Equal(t, SourceUser, entries[1].Source)
	require.False(t, entries[1].UserInvocable)
}

func TestCatalog_Empty(t *testing.T) {
	t.Parallel()

	require.Empty(t, Catalog(nil, nil, ""))
}

func TestFindEffective(t *testing.T) {
	t.Parallel()

	active := []*Skill{
		{SkillFilePath: "/skills/a/SKILL.md", Name: "a"},
		{SkillFilePath: "/skills/b/SKILL.md", Name: "b"},
	}

	found, err := FindEffective(active, "/skills/b/SKILL.md")
	require.NoError(t, err)
	require.Equal(t, "b", found.Name)

	_, err = FindEffective(active, "/skills/missing/SKILL.md")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrSkillNotFound)
}

func TestReadContent_Builtin(t *testing.T) {
	t.Parallel()

	active := DiscoverBuiltin()
	require.NotEmpty(t, active)

	var target *Skill
	for _, s := range active {
		if s.Name == "angela-config" {
			target = s
		}
	}
	require.NotNil(t, target)

	content, result, err := ReadContent(active, nil, "", target.SkillFilePath)
	require.NoError(t, err)
	require.NotEmpty(t, content)
	require.Equal(t, "angela-config", result.Name)
	require.True(t, result.Builtin)
	require.Equal(t, SourceSystem, result.Source)
}

func TestReadContent_BuiltinMissingFile(t *testing.T) {
	t.Parallel()

	active := []*Skill{{SkillFilePath: BuiltinPrefix + "does-not-exist/SKILL.md", Name: "does-not-exist", Builtin: true}}

	_, _, err := ReadContent(active, nil, "", active[0].SkillFilePath)
	require.Error(t, err)
}

func TestReadContent_UserSkill(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	skillDir := filepath.Join(dir, "my-skill")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	skillPath := filepath.Join(skillDir, SkillFileName)
	body := "---\nname: my-skill\ndescription: A test skill.\n---\nHello.\n"
	require.NoError(t, os.WriteFile(skillPath, []byte(body), 0o644))

	skill, err := Parse(skillPath)
	require.NoError(t, err)
	active := []*Skill{skill}

	content, result, err := ReadContent(active, []string{dir}, "", skillPath)
	require.NoError(t, err)
	require.Contains(t, string(content), "Hello.")
	require.Equal(t, "my-skill", result.Name)
	require.False(t, result.Builtin)
}

func TestReadContent_NotFound(t *testing.T) {
	t.Parallel()

	_, _, err := ReadContent(nil, nil, "", "missing")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrSkillNotFound)
}

func TestReadContent_FileMissingOnDisk(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	skillPath := filepath.Join(dir, "SKILL.md")
	active := []*Skill{{SkillFilePath: skillPath, Name: "gone"}}

	_, _, err := ReadContent(active, nil, "", skillPath)
	require.Error(t, err)
}

func TestSkillLabel(t *testing.T) {
	t.Parallel()

	label, source := skillLabel(nil, "", &Skill{Builtin: true, Name: "angela-config"})
	require.Equal(t, "system:angela-config", label)
	require.Equal(t, SourceSystem, source)

	// Project skill: base path lives inside the working dir.
	wd := t.TempDir()
	projectSkillDir := filepath.Join(wd, ".angela", "skills", "proj-skill")
	skillFile := filepath.Join(projectSkillDir, SkillFileName)
	label, source = skillLabel([]string{filepath.Join(wd, ".angela", "skills")}, wd, &Skill{SkillFilePath: skillFile, Name: "proj-skill"})
	require.Equal(t, "project:proj-skill", label)
	require.Equal(t, SourceProject, source)

	// User skill: base path lives outside the working dir.
	homeDir := t.TempDir()
	userSkillDir := filepath.Join(homeDir, "skills", "user-skill")
	skillFile2 := filepath.Join(userSkillDir, SkillFileName)
	label, source = skillLabel([]string{filepath.Join(homeDir, "skills")}, wd, &Skill{SkillFilePath: skillFile2, Name: "user-skill"})
	require.Equal(t, "user:user-skill", label)
	require.Equal(t, SourceUser, source)

	// No skillPaths entry matches the skill's file: falls back to the
	// default user label.
	label, source = skillLabel([]string{"/some/other/path"}, "", &Skill{SkillFilePath: "/unrelated/dir/skill-x/SKILL.md", Name: "skill-x"})
	require.Equal(t, "user:skill-x", label)
	require.Equal(t, SourceUser, source)
}

func TestEscapesParent(t *testing.T) {
	t.Parallel()

	require.True(t, escapesParent(".."))
	require.True(t, escapesParent("../sibling"))
	require.False(t, escapesParent("."))
	require.False(t, escapesParent("sub/dir"))
}

func TestIsProjectSkillPath(t *testing.T) {
	t.Parallel()

	require.False(t, isProjectSkillPath("/some/base", ""))

	wd := t.TempDir()
	base := filepath.Join(wd, ".angela", "skills")
	require.True(t, isProjectSkillPath(base, wd))

	outside := t.TempDir()
	require.False(t, isProjectSkillPath(outside, wd))
}
