package game

import "math/rand/v2"

// NOTE: 取自规则书「范例职业」一节，去掉只能出现在现代游戏里的黑客：人物卡默认时代是 1920 年代。
var Occupations = []string{
	"古董收藏家", "艺术家", "运动员", "作家", "神职人员", "罪犯", "业余艺术爱好者",
	"医生", "流浪者", "工程师", "艺人", "农民", "记者", "律师", "图书馆管理员",
	"军官", "传教士", "音乐家", "超心理学家", "飞行员", "警探", "警察", "私家侦探",
	"教授", "士兵", "部落成员", "狂热者",
}

// RandomOccupation 从范例职业里等概率抽一个。
func RandomOccupation() string {
	return Occupations[rand.IntN(len(Occupations))]
}
