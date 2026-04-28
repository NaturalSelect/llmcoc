// NOTE: Implements sanity and madness mechanics for Call of Cthulhu.
package game

import "math/rand"

// MadnessSymptom describes a madness effect from the COC 7th edition symptom tables.
type MadnessSymptom struct {
	// Duration is a human-readable description of how long the symptom lasts.
	Duration int
	// Description is a Chinese description of the symptom, ready to pass to the Writer agent.
	Description string
	// IsInstantaneous is true for the "instantaneous / bystander" table.
	IsInstantaneous bool
}

// instantaneousSymptoms are the 10 immediate madness symptoms (COC 7th, Chapter 8).
// Triggered when other characters are present; lasts 1D10 rounds.
var instantaneousSymptoms = [10]string{
	"失忆：调查员完全忘记了刚才发生的事情，茫然地站在原地，神情空洞，对周围的人毫无反应。",
	"假性残疾：调查员的身体突然停止了某项功能——失明、失聪或无法开口说话，尽管肉体上毫发无损。",
	"无差别暴力：调查员陷入狂乱，开始对身边最近的人(无论是敌是友)发动攻击，直到被制服或力竭。",
	"严重偏执：调查员坚信队伍中有人是间谍或刺客，开始对同伴怒吼指责，拒绝任何人接近。",
	"误认他人：调查员将某位同伴误认为某个对自己重要的人——已故的爱人、挚友或死敌，并以此态度对待对方。",
	"昏厥：调查员当场失去意识，瘫倒在地，无论周围发生什么都无法将其唤醒，直到发作结束。",
	"逃避行为：调查员被恐惧压垮，拼命向反方向奔逃，不顾一切地试图逃离这个地方，不管路上有何危险。",
	"歇斯底里：调查员陷入歇斯底里的崩溃——嚎啕大哭、狂笑不止或两者交替，完全无法自控。",
	"恐惧症发作：调查员触发了一种新的或已有的恐惧症，在接下来数轮内对恐惧的来源无法正视或接近。",
	"躁狂症发作：调查员触发了一种新的或已有的躁狂症，以一种强迫性且往往危险的方式行事。",
}

// summarySymptoms are the 10 summary madness symptoms (COC 7th, Chapter 8).
// Triggered when the character is alone; time skips 1D10 hours.
var summarySymptoms = [10]string{
	"失忆于陌生之地：调查员在一个陌生的地方苏醒，完全不记得自己是怎么来到这里的，口袋里可能有一些莫名的物品。",
	"财物被盗：调查员发现自己的重要物品(武器、线索或金钱)已不翼而飞，不知是被人偷走还是自己在疯狂中丢弃。",
	"遍体鳞伤：调查员身上带着无法解释的伤痕苏醒，当前生命值减半，对受伤经过毫无记忆。",
	"暴力破坏：调查员在疯狂中做出了某些暴力或破坏性的举动，周围留有明显的证据，但其本人全然不知。",
	"极端信念：调查员在发作期间接受了某种极端的信念或世界观，这一观念将持续影响其此后的行为方式。",
	"寻找重要之人：调查员被一股强烈的冲动驱使，拼命去寻找某个对自己重要的人，并且已经开始行动了一段时间。",
	"被收容或被拘押：调查员被警察、医疗人员或其他机构收押，他们认为调查员的行为危及自身或他人安全。",
	"逃往远处：调查员逃离了现场，最终出现在距离事发地相当远的地方，对这段时间发生的事毫无印象。",
	"新恐惧症：调查员获得了一种新的永久性恐惧症，尽管他可能并不记得是什么让自己变得如此害怕。",
	"新躁狂症：调查员获得了一种新的永久性躁狂症，表现为某种强迫性的、无法压制的行为冲动。",
}

// RollMadnessSymptom randomly selects a madness symptom from the appropriate table.
// instantaneous=true → table for bystander situations; lasts exactly 10 combat rounds (COC 7th rule).
// instantaneous=false → summary table for lone situations; time skips 1D10 hours.
func RollMadnessSymptom(instantaneous bool) MadnessSymptom {
	idx := rand.Intn(10)
	if instantaneous {
		// COC 7th: "疯狂发作只会持续10个战斗轮(应用即时症状时)"——固定10轮，不是随机
		return MadnessSymptom{
			IsInstantaneous: true,
			Duration:        10,
			Description:     instantaneousSymptoms[idx],
		}
	}
	rolls, _ := Roll(1, 10)
	return MadnessSymptom{
		IsInstantaneous: false,
		Duration:        rolls * 2,
		Description:     summarySymptoms[idx],
	}
}

func itoa(n int) string {
	switch n {
	case 1:
		return "1"
	case 2:
		return "2"
	case 3:
		return "3"
	case 4:
		return "4"
	case 5:
		return "5"
	case 6:
		return "6"
	case 7:
		return "7"
	case 8:
		return "8"
	case 9:
		return "9"
	default:
		return "10"
	}
}

// MadnessKind categorises the severity of a sanity loss event.
type MadnessKind int

const (
	MadnessNone       MadnessKind = iota
	MadnessTemporary              // single loss ≥5
	MadnessIndefinite             // daily cumulative loss ≥ maxSAN/5
	MadnessPermanent              // SAN drops to 0
)

// EvalMadness determines what kind of madness (if any) a sanity loss event triggers.
// loss      – the SAN points lost in this single event (positive integer)
// newSAN    – the character's SAN after applying the loss
// dailyLoss – total SAN lost so far today (including this event)
// maxSAN    – the character's current maximum SAN
func EvalMadness(loss, newSAN, dailyLoss, maxSAN int) MadnessKind {
	if newSAN <= 0 {
		return MadnessPermanent
	}
	// Check for indefinite first, as it's more severe than temporary
	if maxSAN > 0 && dailyLoss >= maxSAN/5 {
		return MadnessIndefinite
	}
	if loss >= 5 {
		return MadnessTemporary
	}
	return MadnessNone
}
