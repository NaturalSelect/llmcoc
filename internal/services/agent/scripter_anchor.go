// scripter_anchor.go — 神话锚点（mythos_anchor）的代码侧校验。
//
// 锚点不再交给独立的 Translator 子代理翻译：architect 自己用 ask_lawyer 向规则书专家
// 核验候选元素，再把最终元素全称交给 anchorTracker 登记。登记前由代码强制两条纪律：
// 必须先查过规则书，且不得命中最近已用过的锚点禁用列表。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/llmcoc/server/internal/services/llm"
)

// anchorTracker 记录一次工具循环内的规则书核验进度与已确认的神话锚点。
// 不变量：confirmed 非空时，要么来自调用方传入的初始锚点，要么已经过 ask_lawyer
// 核验且未命中禁用列表。
type anchorTracker struct {
	room        *scripterRoom
	askedLawyer bool
	confirmed   string
}

func newAnchorTracker(room *scripterRoom, initialAnchor string) *anchorTracker {
	return &anchorTracker{room: room, confirmed: strings.TrimSpace(initialAnchor)}
}

// askLawyer 执行 ask_lawyer 工具调用并记下"已核验过规则书"。
func (t *anchorTracker) askLawyer(ctx context.Context, call llm.ToolCall) toolOutcome {
	var args askLawyerArgs
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		return toolOutcome{reject: "SYSTEM REJECT: ask_lawyer参数不是合法JSON，请重新调用。"}
	}
	t.askedLawyer = true
	return toolOutcome{result: scripterAskLawyer(ctx, t.room, args.Question)}
}

// validate 校验候选锚点：非空、已向规则书专家核验过、未命中禁用列表。
// 通过时返回去除首尾空白的锚点；不通过时返回 SYSTEM REJECT 提示文本。
func (t *anchorTracker) validate(anchor string) (valid string, reject string) {
	anchor = strings.TrimSpace(anchor)
	if anchor == "" || anchor == "无" {
		return "", "SYSTEM REJECT: 锚点不能为空；规则书里没有可靠匹配时，请换一个方向继续用ask_lawyer查询，不要乱编凑数。"
	}
	if !t.askedLawyer {
		return "", "SYSTEM REJECT: 登记锚点前必须至少调用一次ask_lawyer，向规则书专家核验该元素的出处与设定，不得凭常识或记忆直接登记。"
	}
	if hit := oneshotFindForbiddenAnchor(anchor, t.room.mythosBlacklist); hit != "" {
		return "", fmt.Sprintf("SYSTEM REJECT: 「%s」命中最近已用过的元素「%s」，未登记。请换一个规则书元素，或换掉概念后重新用ask_lawyer核验。", anchor, hit)
	}
	return anchor, ""
}

// confirm 执行 confirm_anchor 工具调用：校验通过则登记为当前锚点。
func (t *anchorTracker) confirm(call llm.ToolCall) toolOutcome {
	var args confirmAnchorArgs
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		return toolOutcome{reject: "SYSTEM REJECT: confirm_anchor参数不是合法JSON，请重新调用。"}
	}
	anchor, reject := t.validate(args.Anchor)
	if reject != "" {
		return toolOutcome{reject: reject}
	}
	t.confirmed = anchor
	return toolOutcome{result: fmt.Sprintf("已登记神话元素「%s」。继续按规则书裁定写作，登记的元素不得与裁定矛盾。", anchor)}
}
