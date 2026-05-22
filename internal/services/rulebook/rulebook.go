// NOTE: Parses and evaluates game rules and conditions.
// Package rulebook provides loading and keyword-based searching of the COC rulebook.
package rulebook

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

const RulebookDir = `
第一章 介绍

第二章 爱手艺与克苏鲁神话

第三章 创建调查员 23

第四章 技能 40

第五章 游戏系统 71

第六章 战斗 85

第七章 追逐 109

第八章 理智 129

第九章 魔法 143

第十章 主持游戏 153

第十一章 可怖传说书籍 189

第十二章 法术 205

第十三章 外星科技及其造物 230

第十四章 怪物、野兽和异界诸神 238

第十五章 模组 312

第十六章 附录 355

译名表 392

额外WIKI数据
`
const RulebookDetailDir = `
第一章 介绍
1.1 游戏概览
1.2 合作与竞争
1.3 游戏范例
1.4 游戏涵盖范围
1.5 守秘人必读
1.6 游戏用具
第二章 爱手艺与克苏鲁神话
2.1 霍华德·菲利普·洛夫克拉夫特
2.2 克苏鲁神话
第三章 创建调查员
3.1 第一步:生成属性
3.2 第二步:决定职业
3.3 第三步:决定技能并分配技能点
3.4 第四步:创造背景
3.5 第五步:决定装备
3.6 快速参考:创建调查员
3.7 创建调查员的其它选项
第四章 技能
4.1技能定义
4.2技能专攻
4.3 对立技能/难度等级
4.4 孤注一掷
4.5 组合技能检定
4.6技能列表
4.7 可选规则
第五章 游戏系统
5.1 何时掷骰
5.2技能检定
5.3 孤注一掷
5.4 复数玩家为一项技能检定掷骰？
5.5 大失败与大成功
5.6 其它检定
5.7 对抗检定:玩家对抗玩家以及近战
5.8 奖励骰与惩罚骰
5.9 组合技能检定
5.10 社交技能:难度等级
5.11 经验奖励:幕间成长
5.12 信用评级与调查员开支
5.13 熟人
5.14 训练
5.15 老化
5.16 可选规则
第六章 战斗
6.1 战斗轮
6.2 近战
6.3 战技
6.4 其它战斗情况
6.5 射击
6.6 伤害和治疗
6.7 可选规则
第七章 追逐
7.1 第一部分:建立追逐
7.2 第二部分:切入追逐
7.3 第三部分:移动
7.4 第四部分:冲突
7.5 第五部分:可选规则
第八章 理智
8.1 理智值与理智检定
8.2 疯狂
8.3 疯狂的影响
8.4 疯狂的治疗与恢复
8.5 习惯恐惧
8.6 可选规则
第九章 魔法
9.1 神话典籍
9.2 使用魔法
9.3 成为相信者
9.4 可选规则
第十章 主持游戏
10.1 新手守秘人
10.2 准备进行游戏
10.3 创建调查员
10.4 非玩家角色(NPC)
10.5 最大化利用调查员背景
10.6 掷骰与检定
10.7 掌控游戏节奏
10.8 灵感检定
10.9 非玩家角色的幸运值
10.10 发放信息
10.11 洞察检定
10.12 展示材料(Handouts)
10.13 使用规则
10.14 动作场景
10.15 展现神话的恐怖
10.16 赢家与败者
10.17 失败的理智检定
10.18 惊吓玩家
10.19 魔法
10.20 完结故事
10.21 创作模组
10.22 洛氏主题
10.23 导入
10.24 那么,最后…
第十一章 可怖传说书籍
11.1 描述神话典籍
11.2 使用神话典籍
11.3 神话典籍
11.4 神秘学书籍
第十二章 法术
12.1 法术
12.2 法术变体
12.3 法术列表
第十三章 外星科技及其造物
第十四章 怪物、野兽和神话诸神
14.1 神话生物
14.2 神话诸神
14.3 经典妖魔
14.4 野兽
14.5 可选规则
第十五章 模组
第十六章 附录
额外WIKI数据
`

var Aliens = func() []string {
	text := `
# 米戈 (Mi-Go)
# 古老者
# 伊斯人
# 飞天水螅
# 星之智慧教派
# 蛇人
# 深潜者
# 夏盖妖虫
	`
	lines := strings.Split(text, "\n")
	var aliens []string
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
		if line != "" {
			aliens = append(aliens, line)
		}
	}
	return aliens
}()

var Books = func() []string {
	text := `
# 《阿齐夫》
# 《阿撒托斯及其他》
# 《迪詹之书》
# 《伊波恩之书》
# 《刻莱诺残篇》
# 《狂僧克利萨努斯的忏悔》
# 《水中之喀特》
# 《<死灵之书>中的克苏鲁》
# 《食尸鬼教团》
# 《蠕虫之秘密》
# 《埃尔特当陶片》
# 《格哈恩残篇》
# 《黄衣之王》
# 《致夏盖的安魂弥撒》
# 《怪物及其族类》
# 《不可名状的教团》
# 《死灵之书》
# 《阿齐夫》
# 《死灵之书》
# 《萨塞克斯手稿》
# 《死灵之书》
# 《巨石的子民》
# 《纳克特抄本》
# 《波纳佩圣典》
# 《格拉基启示录》
# 《拉莱耶文本》
# 《新英格兰乐土上的奇术异事》
# 《真实的魔法》
# 《赞苏石板》
# 《宣福者美多迪乌斯》
# 《翡翠石板》
# 《金枝》
# 《易经》
# 《揭开面纱的伊西斯》
# 《所罗门之钥》
# 《女巫之锤》
# 《诺查丹玛斯的预言》
# 《西欧的异教巫术崇拜》
# 《光明篇》`
	lines := strings.Split(text, "\n")
	var books []string
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
		if line != "" {
			books = append(books, line)
		}
	}
	return books
}()

var GreadOldOnesAndGods = func() []string {
	text := `
# 阿布霍斯
# 阿特拉克.纳克亚
# 阿撒托斯
# 芭丝特
# 昌格纳·方庚
# 克图格亚
# 伟大的克苏鲁
# 赛伊格亚
# 道罗斯
# 埃霍特
# 加塔诺托亚
# 格拉基
# 哈斯塔,不可名状者
# 伊塔库亚
# 黄衣之王,哈斯塔的化身
# 黄印
# 诺登斯
# 奈亚拉托提普
# 尼约格萨
# 兰-提格斯
# 莎布-尼古拉斯
# 修德梅尔
# 撒托古亚
# 图尔兹查
# 乌波-萨斯拉
# 伊戈罗纳克
# 伊波-兹特尔
# 伊格
# 蛇之父伊格
# 犹格-索托斯
# 佐斯-奥摩格
# 格赫罗斯
# 撒达·赫格拉
# 塔维尔·亚特·乌姆尔
# 亚弗戈蒙
# 阿尔瓦撒
`
	lines := strings.Split(text, "\n")
	var gods []string
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
		if line != "" {
			gods = append(gods, line)
		}
	}
	return gods
}()

var Monsters = func() []string {
	text := `
# 幽灵
# 木乃伊
# 骷髅,人类
# 吸血鬼
# 丧尸
# 蝙蝠
# 鸟
# 熊
# 鳄鱼
# 马
# 狮子
# 老鼠
# 鲨鱼
# 蛇
# 巨型乌贼
# 黄蜂和蜜蜂群
# 狼`
	lines := strings.Split(text, "\n")
	var monsters []string
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
		if line != "" {
			monsters = append(monsters, line)
		}
	}
	return monsters
}()

var MythosCreatures = func() []string {
	text := `
独立种族（上级）
# 钻地魔虫
# 星之彩
# 巨噬蠕虫
# 飞天水螅
# 诺弗·刻
# 绿渊眷族
# 无形之骏马
# 罗伊格尔
# 修格斯领主
# 廷达罗斯猎犬
# 廷达罗斯领主
# 塞克洛托尔星的死之蔓藤
# 伊斯之伟大种族

独立种族（下级）
# 埃杜布拉里
# 阿尔斯卡里
# 风之子
# 空鱼
# 空鬼
# 远古者
# 邪恶真菌
# 妖鬼
# 食尸鬼
# 古革巨人
# 终北之地住民
# 昆扬人
# 冷族人
# 冷蛛
# 勒杰赫斯住民
# 玛尔滕斯一族
# 火星人
# 米·戈
# 精神寄生虫
# 月兽
# 奈欧斯·克欧格亥
# 奈汉·格瑞
# 鼠人
# 爬虫人
# 夏盖
# 夏盖虫族
# 原初修格斯
# 空间食魔
# 星之精
# 猪人
# 外域恐怖
# 廷达罗斯混血种
# 三尖树
# 地底掘进者
# 沃米人
# 沃尔人
# 亚狄斯住民
# 耶库伯居民
# 新伟大种族
# 祖格

仆从种族（上级）
# 夏乌戈纳尔·法格恩的弟兄
# 克苏鲁星之眷族
# 黯藻
# 深渊之民
# 古异子嗣
# 哈斯塔之眷族
# 奈亚拉托提普的恐怖猎手
# 欧图伊格的眷族
# 外神之仆役
# 修格斯
# 黑山羊幼仔
# 风之眷属
# 撒托古亚的子孙
# 乌波·萨丝拉的血裔
# 看守者
# 尤格·索托斯之子

仆从种族（下级）
# 阿布霍斯之眷属
# 爱伊海伊人
# 阿尼米丘利
# 阿特拉克-纳查之女
# 拜亚基
# 查寇塔
# 寒冷者
# 爬行者
# 梦境结晶器守护者
# 克苏鲁之仆役
# 漆黑者
# 深潜者
# 混血深潜者
# 尘人
# 艾霍特寄生体
# 炎之精
# 星海钓客
# 格拉基之仆从
# 夜魇
# 尼约格达的眷属
# 奥图姆的奴仆
# 苍白舞者
# 人面鼠
# 潜砂怪
# 搜寻者
# 夏塔克鸟
# 灵体猎手
# 斯芬克斯的孩子
# 丘丘人
# 坟兽
# 姆巴瓦树人
# 撒托古亚的无形之子
# 不可名状的支配者
# 雪怪
# 地上蠕虫
# 伊戈隆纳克的仆从
# 伊格的子孙
# 伊格的眷属
# 于格
# 扎尔

唯一存在
# 百万蒙宠者
# 蠕虫行者
`
	lines := strings.Split(text, "\n")
	var creatures []string
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
		if line != "" {
			creatures = append(creatures, line)
		}
	}
	return creatures
}()

var Spells = func() []string {
	text := `
# 阿波菲斯的诅咒
# 阿布霍斯唤醒术
# 阿布霍斯通神术
# 阿尔瓦萨请神术
# 阿里阿德涅之线
# 阿努比斯的守卫
# 阿佩普放逐术
# 阿撒托斯的恐怖诅咒
# 阿撒托斯请神术
# 阿撒托斯通神术
# 阿图请神术
# 艾霍特流放术
# 艾霍特通神术
# 爱情魔药酿造法
# 爱因斯坦公式
# 奥萨多戈瓦请神术
# 奥苏耶格通神术
# 巴尔塞之印创建术
# 巴卡召唤术
# 芭丝特的祝福
# 拜亚基束缚术
# 拜亚基召唤术
# 拜亚提斯放逐术
# 拜亚提斯通神术
# 悲惨瘙痒术
# 被诅咒的眼
# 彼端之旅
# 蝙蝠化形法
# 变形术
# 波罗纳斯的熔炉
# 博克鲁格通神术
# 不可名状的诺言
# 不可言喻的理解
# 不透明之墙
# 不朽术
# 布格-沙什请神术
# 擦肩无影术
# 草木号令术
# 长方形屏障
# 长矛附魔术
# 长命坠创建法
# 超K粉酿造术
# 鸱鸮之宴
# 赤印术
# 炽天使之耀
# 仇恨雕像
# 除名术
# 民俗魔法
# 大戴什召唤术
# 大献祭仪式
# 呆滞震爆术
# 胆言术
# 刀锋祝福术
# 刀具附魔术
# 道罗斯请神术
# 登上不朽的阶梯
# 等边屏幕术
# 敌人束缚术
# 笛子附魔术
# 地脉
# 第六萨斯拉塔吟诵术
# 冬眠术
# 动物残废法/动物治愈法
# 动物雕像附魔术
# 动物号令法
# 动物魅惑法
# 动物束缚法
# 毒血法
# 多尔公式
# 厄运法
# 厄运附魔
# 恶魔的感知
# 恶魔揭露术
# 恶神影请神术
# 恶神影通神术
# 恩宠移除术
# 发酵病术
# 法阵法术
# 法阵施放术
# 凡尘平静法
# 反胃法阵
# 反转移术
# 飞行术
# 飞水螅联络术
# 翡翠喇嘛通神术
# 费因疲乏术
# 分离抛掷术
# 分沙术
# 坟墓之吻
# 焚化术
# 风暴创建法
# 封印陷坑
# 疯狂之笛
# 弗洛林倾泻术
# 伏尔瓦多斯的净化之火
# 符文施放术
# 符咒创建法
# 腐烂外皮之诅咒
# 腐朽之触
# 附魔的阿努比斯之尘
# 附魔法术
# 附魔侦测术
# 复活术
# 复生术
# 复元冥想法
# 戈尔-戈罗斯请神术
# 钢铁意志术
# 戈尔戈罗斯形体扭曲术
# 活尸创建术
# 活血偷取术
# 活衣服
# 格拉基通神术
# 格拉基请神术
# 格哩-格哩附魔术
# 格利桑德之歌
# 格罗斯通神术
# 铬绿之风
# 古老者联络术
# 骨骼溶解术
# 光明与黑暗之眼
# 鬼魂号令法
# 棍棒附魔术
# 过来见我
# 哈布沙暴发生术
# 哈斯塔请神术
# 哈斯塔释放术
# 哈斯塔之歌
# 好贼水
# 和等待着的黑暗交谈之术
# 河童之息
# 赫耳墨斯·特里斯墨吉斯忒斯的毒尘
# 黑暗诅咒
# 黑山羊幼崽束缚术
# 黑山羊幼崽召唤术
# 黑箱术
# 黑质召集术
# 轰盲术
# 护身符
# 化骨术
# 坏疽术
# 坏尸粉创建术
# 幻梦境魔法
# 荒芜之风
# 黄金蜂蜜酒酿造术（甲型）
# 黄金蜂蜜酒酿造术（乙型）
# 黄泉深渊术
# 黄色跃魂封印法
# 黄印术
# 恍惚术
# 火舞术
# 火焰斗篷术
# 火焰护盾术
# 激活术
# 极乐术
# 记忆模糊术
# 记忆吞食术
# 加速术
# 加塔诺托亚请神术
# 僵尸创建术
# 僵尸召集术
# 僵尸之眼
# 郊狼粉尘
# 戒指附魔术
# 筋骨打结术
# 晶石召唤
# 精神的温暖
# 精神交换术
# 精神模糊术
# 精神囚禁术
# 精神淹溺术
# 精神震爆术
# 精神之舞
# 精神转移术
# 净化仪式
# 酒神狂欢术
# 旧日辟邪符附魔术
# 旧印开光术
# 咀-咀附魔法
# 巨龟唤醒术
# 巨噬蠕虫召唤术
# 剧毒瞥视术
# 卡戎乞求术
# 科斯通神术
# 科斯之印嘱咐术
# 科西切之死
# 拉莱耶造雾术
# 拉略罗娜请神术
# 拉神闪光术
# 拉神之声
# 蜡烛附魔法
# 乐土施恩术
# 联络术
# 辽丹酿造术
# 灵薄门
# 灵薄狱
# 灵魂辨识术
# 灵魂抽取术
# 灵魂出窍术
# 灵魂分配术
# 灵魂漫游术
# 灵魂窃取术
# 灵魂束缚术
# 灵魂陷阱术
# 灵魂榨取术
# 灵魂召唤术
# 灵魂之歌
# 灵魂转移术
# 灵体变身术
# 灵体猎手变身术
# 灵体剃刀术
# 录音附魔术
# 罗伊格尔联络术
# 螺旋升空术
# 绿腐术
# 马连卡门的瞩目闪击术
# 曼德拉术
# 蔓延的丧失
# 梦境发送术
# 梦境幻象术
# 梦境门
# 梦境驱逐术
# 梦想家搜寻术
# 梦想家诱捕术
# 梦想家助力术
# 梦学门
# 梦魇术（甲型）
# 梦魇术（乙型）
# 梦魇效果
# 迷身术
# 米-戈联络术
# 面纱轻揭术
# 面纱撕裂术
# 藐视重力术
# 摩摩伊仪式
# 魔法双杖附魔术
# 魔鬼逐出术
# 魔力吸取术
# 魔力之吟
# 魔像创建术
# 末法之龙神请神术
# 陌生人之眼
# 莫特兰玻璃幻术
# 姆纳加拉请神术
# 姆诺姆夸之蛇
# 木乃伊活化术
# 墓穴群虫联络术
# 纳克特五芒星嘱咐术
# 纳克-提特障壁创建术
# 奈哈戈送葬歌
# 奈亚拉托提普的祭刀附魔术
# 奈亚拉托提普通神术
# 奈亚拉托提普之影
# 耐用奴仆术
# 内部观测术
# 内心灵光唤醒术
# 尼安贝的魔力
# 尼约格萨紧握术
# 尼约格萨请神术
# 拟人术
# 涅弗伦-卡的封印
# 努曼西亚术
# 诺登斯通神术
# 诺弗-刻联络术
# 帕维尤特棒附魔法
# 帕祖祖通神术
# 帕祖祖之怒
# 帕祖祖之息
# 潘药剂酿造术
# 抛射物附魔术
# 膨胀术
# 皮肤控制术
# 皮行者术
# 辟邪符创建术
# 偏转术
# 飘浮术
# 平凡无奇术
# 仆从送还术
# 蒲林的埃及十字架
# 普塔斯翡翠飞镖
# 普塔斯薰衣草球
# 漆黑者请神术
# 契约尸巫术
# 器官转移术
# 潜沙怪联络术
# 青春吸取术
# 清心法
# 请神术
# 热利姆·沙伊科尔斯请神术
# 人化灌丛术
# 人类联络术
# 人类引诱术
# 人面鼠联络术
# 人面鼠诅咒
# 人偶附魔术
# 仁慈感化术
# 忍耐之吟
# 日光直视术
# 荣华生财法
# 肉傀儡活化术
# 肉体屈服术
# 蠕虫的同心圆
# 蠕虫术
# 蠕虫召来术
# 入梦之药酿造术
# 撒托古亚通神术
# 萨阿马阿仪式
# 塞克之光
# 塞里特的可怕末日
# 塞壬之歌
# 塞伊地请神术
# 塞伊格亚请神术
# 莎布-尼古拉丝请神术
# 莎布-尼古拉丝通神术
# 伤害偏转术
# 哨子附魔术
# 蛇臂术
# 蛇人搜寻术
# 设备失效术
# 伸触术
# 身体部件转移术
# 深潜者束缚术
# 深潜者召唤术
# 深渊之息
# 深渊之音加速术
# 神圣蛇蜕术
# 神圣真理之光
# 生魂棒创建术
# 生命觉察术
# 生命灵药酿造术
# 生命食粮术
# 生命偷取术
# 圣蛇发送术
# 圣者的堕落
# 尸体唤起术
# 尸体占据术
# 尸体制备术
# 失物找寻法
# 石板附魔术
# 石板诅咒
# 石化术
# 时光门
# 时光陷阱
# 时空窗创建术
# 时空门
# 时空门创建术
# 时空门观察术
# 时空门迁移术
# 时空门搜寻术
# 时空箱
# 食尸鬼联络术
# 手杖附魔术
# 守卫法阵
# 守卫术
# 守卫之印
# 守卫之咏
# 兽化人束缚术
# 书册附魔术
# 束缚术
# 水晶世界
# 水晶调谐术
# 瞬间启蒙术
# 思想发声术
# 斯芬克斯的子嗣创建术
# 斯芬克斯的子嗣联络术
# 死灵联络术
# 死亡面具术
# 死亡的气息
# 苏莱曼之尘
# 苏斯螺旋
# 岁月之怒
# 缩小术
# 索伦白网术
# 索罗斯强壮术
# 塔格-克拉图尔的反角度
# 塔昆·阿提普之镜
# 炭火盆附魔术
# 特兹查波特尔之铃
# 提拔术
# 天气改换术
# 天气畸变术
# 廷达洛斯之猎犬联络术
# 通神术
# 痛苦屏障
# 透特之咏
# 图鲁亚
# 图鲁亚幻术
# 退化术
# 外神仆役联络术
# 外神仆役束缚术
# 外神仆役召唤术
# 剜心术
# 完善术
# 网络幽灵术
# 忘却之波
# 维瑞之印
# 未来观测术
# 瘟疫发生术（甲型）
# 瘟疫发生术（乙型）
# 瘟疫召唤术
# 文本认知术
# 稳固术
# 无形眷族联络术
# 雾之眷属加速术
# 寤寐术
# 希什的蕾丝帘幕
# 夏恩驱赶术
# 夏恩逐出术
# 现世裂隙术
# 相似的敏锐
# 相貌吞食术
# 肖格纳尔·方的诅咒
# 肖格纳尔·方通神术
# 肖格纳尔·方之兄弟召唤术
# 肖像画附魔术
# 消耗病诅咒
# 消失术
# 邪眼守卫术
# 邪眼术
# 心理暗示术
# 心灵感应术
# 心跳停止术
# 心脏爆炸术
# 心中的勇气
# 星之精束缚术
# 星之精召唤术
# 星之种
# 凶暴疯狂术
# 熊皮法
# 熊爪法
# 修格斯号令术
# 修格斯束缚术
# 修格斯召唤术
# 续命术
# 喧嚣嗉囊术
# 血清附魔术
# 血肉防护术
# 血肉附魔术
# 血肉熔解术
# 血肉蠕行者创建术
# 血肉移植术
# 血舌的号令
# 寻龙法
# 炎之精束缚术
# 炎之精召唤术
# 扬升大师联络术
# 耶德·艾塔德放逐术
# 野兽唤醒法
# 野兽之神请神术
# 业报授予法
# 夜雾唤起术
# 夜魇束缚术
# 夜魇召唤术
# 液化术
# 伊本-加齐之粉
# 伊波恩雾轮术
# 伊波-兹特尔请神术
# 伊伯鬼魂唤起术
# 伊戈罗纳克通神术
# 伊格的尖牙
# 伊格请神术
# 伊格通神术
# 伊格之子嗣摧毁术
# 伊南娜的馈赠
# 伊欧德请神术
# 伊欧德通神术
# 伊斯人联络术
# 伊塔库亚请神术
# 伊希斯的封印
# 依诺拉停止术
# 遗恨术
# 疑心消除术
# 抑光术
# 阴影虚空术
# 引神术
# 荧火术
# 蝇蛆术
# 犹格-索托斯的拥抱
# 犹格-索托斯请神术
# 犹格-索托斯通神术
# 犹格-索托斯之拳
# 友善关系断绝术
# 有角之人请神术
# 幼体驱除术
# 诱鱼术
# 预言术
# 欲望金笼术
# 元素控制术
# 原初之水酿造术
# 远行涡流术
# 月光术
# 月棱镜守护者请神术
# 月兽联络术
# 扎尔与罗伊格尔通神术
# 占卜窗创建术
# 占卜法
# 占据术
# 召唤术
# 召雷术
# 折磨术
# 真实一瞥授予术
# 真言术
# 振荡穹庐术
# 支配术
# 肢体凋萎术
# 治愈法
# 挚爱回归术
# 致病术
# 致盲术/治盲术
# 致死术
# 终结日预示术
# 肿胀折磨术
# 种内之神唤醒/驱散术
# 咒逐术
# 朱玛唤醒术
# 祝福法
# 转生术
# 自保袋创建术
# 诅咒法
# 诅咒之板创建术
# 诅咒之笛创建术
# 祖谢昆请神术
# 钻地魔虫联络术
# 最终宴请
# 尊长创造术
# 佐斯-奥摩格通神术
# 作物枯萎法/作物祝福法`
	lines := strings.Split(text, "\n")
	var spells []string
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
		if line != "" {
			spells = append(spells, line)
		}
	}
	return spells
}()
// AvailableConstantKeys returns names that can be used by agent tool calls.
func AvailableConstantKeys() []string {
	return []string{
		"rulebook_dir",
		"rulebook_detail_dir",
		"aliens",
		"books",
		"great_old_ones_and_gods",
		"monsters",
		"mythos_creatures",
		"spells",
		"skills",
	}
}

var AllSkills = []string{
	"会计", "人类学", "估价", "考古学", "魅惑", "攀爬", "计算机使用", "信用评级",
	"乔装", "驾驶(汽车)", "电气维修", "电子学", "话术", "急救", "历史", "恐吓",
	"跳跃", "母语", "法律", "图书馆使用", "聆听", "锁匠", "机械维修",
	"医学", "博物学", "领航(陆地)", "神秘学", "操作重型机械",
	"说服", "药学", "摄影", "物理学", "精神分析",
	"心理学", "骑术", "科学(地质学)", "潜行", "游泳",
	"投掷", "追踪", "驾驶(船)", "侦查", "斗殴", "闪避", "手枪", "步枪/霰弹枪", "冲锋枪",
}

// ReadConstant returns the requested rulebook constant in plain text.
func ReadConstant(name string) string {
	key := normalizeConstKey(name)

	switch key {
	case "rulebook_dir":
		return strings.TrimSpace(RulebookDir)
	case "rulebook_detail_dir":
		return strings.TrimSpace(RulebookDetailDir)
	case "aliens":
		return formatList("aliens", Aliens)
	case "books":
		return formatList("books", Books)
	case "great_old_ones_and_gods":
		return formatList("great_old_ones_and_gods", GreadOldOnesAndGods)
	case "monsters":
		return formatList("monsters", Monsters)
	case "mythos_creatures":
		return formatList("mythos_creatures", MythosCreatures)
	case "spells":
		return formatList("spells", Spells)
	case "skills":
		return formatList("skills", AllSkills)
	default:
		return "unknown constant: " + name + "\navailable: " + strings.Join(AvailableConstantKeys(), ", ")
	}
}

func normalizeConstKey(name string) string {
	key := strings.ToLower(strings.TrimSpace(name))
	repl := strings.NewReplacer("-", "_", " ", "_", ".", "_", "/", "_")
	key = repl.Replace(key)

	switch key {
	case "rulebookdir":
		return "rulebook_dir"
	case "rulebookdetaildir", "rulebook_detaildir":
		return "rulebook_detail_dir"
	case "greadoldonesandgods", "gread_old_ones_and_gods", "greatoldonesandgods":
		return "great_old_ones_and_gods"
	case "mythoscreatures":
		return "mythos_creatures"
	default:
		return key
	}
}

func formatList(name string, items []string) string {
	if len(items) == 0 {
		return name + "\n(total=0)"
	}
	var sb strings.Builder
	sb.WriteString(name)
	sb.WriteString("\n")
	sb.WriteString("(total=")
	sb.WriteString(strconv.Itoa(len(items)))
	sb.WriteString(")\n")
	for i := 0; i < len(items); i++ {
		sb.WriteString("- ")
		sb.WriteString(items[i])
		sb.WriteString("\n")
	}
	return strings.TrimSpace(sb.String())
}

// Section represents a single top-level chapter/section of the rulebook.
type Section struct {
	Title   string
	Content string
}

// Index is a slice of rulebook sections loaded from a Markdown file.
type Index []Section

// GlobalIndex holds the loaded rulebook sections, populated at startup.
var GlobalIndex Index

// GlobalHash is the SHA-256 hash of the loaded rulebook file.
var GlobalHash string

var (
	ruleBookLines    []string
	spellBookLines   []string
	monsterBookLines []string
)

// FileHash returns the SHA-256 hash of the file at path.
func FileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Load reads a Markdown file at the given path and splits it into sections
// at any Markdown heading level (lines starting with one or more '#').
func Load(path string) (Index, error) {
	lines, err := loadLines(path)
	if err != nil {
		return nil, err
	}
	ruleBookLines = lines

	var sections Index
	var current *Section
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if title, ok := parseHeading(trimmed); ok {
			// Save previous section.
			if current != nil {
				sections = append(sections, *current)
			}
			current = &Section{Title: title}
		} else if current != nil {
			current.Content += line + "\n"
		}
	}
	// Flush last section.
	if current != nil {
		sections = append(sections, *current)
	}

	return sections, nil
}

// LoadSpellBook loads the fixed spell reference document used by Lawyer tools.
func LoadSpellBook(path string) error {
	lines, err := loadLines(path)
	if err != nil {
		return err
	}
	spellBookLines = lines
	return nil
}

// LoadMonsterBook loads the fixed monster reference document used by Lawyer tools.
func LoadMonsterBook(path string) error {
	lines, err := loadLines(path)
	if err != nil {
		return err
	}
	monsterBookLines = lines
	return nil
}

func loadLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// Increase scanner buffer for very long lines in the reference documents.
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

func parseHeading(line string) (string, bool) {
	if line == "" || line[0] != '#' {
		return "", false
	}
	i := 0
	for i < len(line) && line[i] == '#' {
		i++
	}
	if i >= len(line) || line[i] != ' ' {
		return "", false
	}
	title := strings.TrimSpace(line[i:])
	if title == "" {
		return "", false
	}
	return title, true
}

type GrepResult struct {
	LineNum int
	Text    string
}

func GrepRuleBook(keyword string) []GrepResult {
	return grepLines(ruleBookLines, keyword)
}

func GetContentByLineNum(firstLine, endLine int) string {
	return contentByLineNum(ruleBookLines, firstLine, endLine)
}

func GrepSpellBook(keyword string) []GrepResult {
	return grepLines(spellBookLines, keyword)
}

func GetSpellContentByLineNum(firstLine, endLine int) string {
	return contentByLineNum(spellBookLines, firstLine, endLine)
}

func GrepMonsterBook(keyword string) []GrepResult {
	return grepLines(monsterBookLines, keyword)
}

func GetMonsterContentByLineNum(firstLine, endLine int) string {
	return contentByLineNum(monsterBookLines, firstLine, endLine)
}

func grepLines(lines []string, keyword string) []GrepResult {
	var results []GrepResult
	for i, line := range lines {
		if strings.Contains(line, keyword) {
			results = append(results, GrepResult{LineNum: i + 1, Text: line})
		}
	}
	return results
}

func contentByLineNum(lines []string, firstLine, endLine int) string {
	if firstLine < 1 {
		firstLine = 1
	}
	if endLine < firstLine {
		return ""
	}

	sb := strings.Builder{}
	for i := firstLine - 1; i < endLine && i < len(lines); i++ {
		sb.WriteString(lines[i])
		sb.WriteString("\n")
	}
	return sb.String()
}

// Search returns up to maxResults sections ranked by multi-strategy matching:
// exact phrase, keyword hits, title boosts, and Chinese fragment fuzzy overlap.
func Search(idx Index, query string, maxResults int) []Section {
	if maxResults <= 0 {
		maxResults = 3
	}
	qRaw := strings.TrimSpace(strings.ToLower(query))
	if qRaw == "" {
		return nil
	}
	qNorm := normalizeForMatch(qRaw)
	keywords := splitKeywords(qRaw)
	if len(keywords) == 0 {
		return nil
	}

	type scored struct {
		sec   Section
		score int
	}
	var hits []scored

	for _, sec := range idx {
		titleLower := strings.ToLower(sec.Title)
		contentLower := strings.ToLower(sec.Content)
		wholeLower := titleLower + "\n" + contentLower
		titleNorm := normalizeForMatch(titleLower)
		wholeNorm := normalizeForMatch(wholeLower)

		score := scoreSection(titleLower, contentLower, wholeLower, titleNorm, wholeNorm, qRaw, qNorm, keywords)
		if score > 0 {
			hits = append(hits, scored{sec, score})
		}
	}

	sort.SliceStable(hits, func(i, j int) bool {
		return hits[i].score > hits[j].score
	})

	if len(hits) > maxResults {
		hits = hits[:maxResults]
	}

	result := make([]Section, 0, len(hits))
	for _, h := range hits {
		result = append(result, h.sec)
	}
	return result
}

func scoreSection(titleLower, contentLower, wholeLower, titleNorm, wholeNorm, qRaw, qNorm string, keywords []string) int {
	score := 0

	// Exact phrase has highest weight.
	if strings.Contains(wholeLower, qRaw) {
		score += 120
	}
	if qNorm != "" && strings.Contains(wholeNorm, qNorm) {
		score += 120
	}
	if strings.Contains(titleLower, qRaw) {
		score += 180
	}
	if qNorm != "" && strings.Contains(titleNorm, qNorm) {
		score += 180
	}

	for _, kw := range keywords {
		kwNorm := normalizeForMatch(kw)
		if strings.Contains(titleLower, kw) || (kwNorm != "" && strings.Contains(titleNorm, kwNorm)) {
			score += 40
		}
		if strings.Contains(contentLower, kw) || (kwNorm != "" && strings.Contains(wholeNorm, kwNorm)) {
			score += 18
		}

		// Fuzzy fallback for long Chinese phrases: overlap on 2-rune fragments.
		if kwNorm != "" && len([]rune(kwNorm)) >= 4 {
			overlap := bigramOverlap(wholeNorm, kwNorm)
			if overlap >= 0.5 {
				score += int(overlap * 20)
			}
		}
	}

	return score
}

func normalizeForMatch(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func bigramOverlap(haystack, needle string) float64 {
	hRunes := []rune(haystack)
	nRunes := []rune(needle)
	if len(nRunes) < 2 || len(hRunes) < 2 {
		return 0
	}

	total := len(nRunes) - 1
	if total <= 0 {
		return 0
	}

	hSet := make(map[string]struct{}, len(hRunes)-1)
	for i := 0; i < len(hRunes)-1; i++ {
		hSet[string(hRunes[i:i+2])] = struct{}{}
	}

	matched := 0
	for i := 0; i < len(nRunes)-1; i++ {
		if _, ok := hSet[string(nRunes[i:i+2])]; ok {
			matched++
		}
	}

	return float64(matched) / float64(total)
}

// Format converts sections to plain text suitable for an LLM prompt, with a
// total character cap to avoid blowing up the context window.
func Format(sections []Section, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 3000
	}
	var sb strings.Builder
	for _, sec := range sections {
		block := "【" + sec.Title + "】\n" + sec.Content + "\n"
		if sb.Len()+len(block) > maxChars {
			// Truncate last block to fit.
			remaining := maxChars - sb.Len()
			if remaining > 0 {
				sb.WriteString(block[:remaining])
				sb.WriteString("…")
			}
			break
		}
		sb.WriteString(block)
	}
	return strings.TrimSpace(sb.String())
}

// splitKeywords lowercases query and splits on whitespace / punctuation,
// then expands long tokens into smaller Chinese fragments for better recall.
func splitKeywords(query string) []string {
	// Replace common Chinese punctuation with spaces, then split on whitespace.
	replacer := strings.NewReplacer(
		",", " ", "。", " ", "、", " ", ":", " ",
		"；", " ", "(", " ", ")", " ", "【", " ", "】", " ",
		"？", " ", "！", " ", ",", " ", ".", " ", ";", " ",
		":", " ", "(", " ", ")", " ", "[", " ", "]", " ",
	)
	cleaned := replacer.Replace(strings.ToLower(query))
	rawParts := strings.Fields(cleaned)
	out := make([]string, 0, len(rawParts)+8)

	seen := map[string]struct{}{}
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		if _, ok := seen[v]; ok {
			return
		}
		out = append(out, v)
		seen[v] = struct{}{}
	}

	base := append([]string(nil), rawParts...)
	for _, p := range base {
		add(p)
		r := []rune(normalizeForMatch(p))
		if len(r) >= 4 {
			// Add 2~4 rune fragments from long phrases to improve recall for Chinese.
			for i := 0; i < len(r); i++ {
				for w := 2; w <= 4; w++ {
					if i+w <= len(r) {
						add(string(r[i : i+w]))
					}
				}
			}
		}
	}

	// Also add the whole query as one keyword to catch exact phrases.
	if len(base) > 1 {
		add(strings.ToLower(strings.TrimSpace(query)))
		qNorm := normalizeForMatch(query)
		if qNorm != "" {
			add(qNorm)
		}
	}
	return out
}
