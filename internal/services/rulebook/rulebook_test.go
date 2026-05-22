package rulebook

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFixedDocumentLineCaches(t *testing.T) {
	dir := t.TempDir()
	spellPath := filepath.Join(dir, "spell.md")
	monsterPath := filepath.Join(dir, "monster.md")
	writeTestFile(t, spellPath, "# 法术\n第一行\n血肉防护术 消耗MP\n结束\n")
	writeTestFile(t, monsterPath, "# 怪物\n拜亚基\n护甲 2点\n结束\n")

	if err := LoadSpellBook(spellPath); err != nil {
		t.Fatalf("LoadSpellBook failed: %v", err)
	}
	if err := LoadMonsterBook(monsterPath); err != nil {
		t.Fatalf("LoadMonsterBook failed: %v", err)
	}

	spellHits := GrepSpellBook("血肉防护术")
	if len(spellHits) != 1 || spellHits[0].LineNum != 3 || spellHits[0].Text != "血肉防护术 消耗MP" {
		t.Fatalf("unexpected spell grep hits: %#v", spellHits)
	}
	if got := GetSpellContentByLineNum(2, 3); got != "第一行\n血肉防护术 消耗MP\n" {
		t.Fatalf("unexpected spell content: %q", got)
	}

	monsterHits := GrepMonsterBook("拜亚基")
	if len(monsterHits) != 1 || monsterHits[0].LineNum != 2 || monsterHits[0].Text != "拜亚基" {
		t.Fatalf("unexpected monster grep hits: %#v", monsterHits)
	}
	if got := GetMonsterContentByLineNum(2, 3); got != "拜亚基\n护甲 2点\n" {
		t.Fatalf("unexpected monster content: %q", got)
	}
}

func TestLoadRulebookResetsLineCache(t *testing.T) {
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "first.md")
	secondPath := filepath.Join(dir, "second.md")
	writeTestFile(t, firstPath, "# 第一版\n旧规则\n")
	writeTestFile(t, secondPath, "# 第二版\n新规则\n")

	if _, err := Load(firstPath); err != nil {
		t.Fatalf("first Load failed: %v", err)
	}
	if _, err := Load(secondPath); err != nil {
		t.Fatalf("second Load failed: %v", err)
	}

	if hits := GrepRuleBook("旧规则"); len(hits) != 0 {
		t.Fatalf("rulebook line cache was not reset: %#v", hits)
	}
	if hits := GrepRuleBook("新规则"); len(hits) != 1 || hits[0].LineNum != 2 {
		t.Fatalf("unexpected new rule hits: %#v", hits)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write test file failed: %v", err)
	}
}
