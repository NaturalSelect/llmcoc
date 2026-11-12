package agent

// NOTE: Writer 的 paint 工具：Writer 写正文时可以随时调用 paint 配图，工具立即返回占位地址(illust://<slot>)，
// 图片在后台画完后由 handlers 补进消息。Writer 把占位写成 markdown 图片放进正文，前端按 meta.illustrations 解析。
// 没有启用 painter、provider 不支持流式工具调用或 ctx 里没有 illustrator 时，Writer 走原来的 tool_choice=none 路径，
// 提示词和请求都与引入 paint 之前一字不差。

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

const (
	writerPaintToolName = "paint"
	// NOTE: 配图数量不设上限，这里只是防止模型无限循环调用工具；每次 paint 之后都要多一轮续写请求。
	writerMaxToolRounds = 32

	illustrationScheme = "illust://"
)

const (
	tokenWriterToolRule     = "{{WRITER_TOOL_RULE}}"
	tokenWriterImageBan     = "{{WRITER_IMAGE_BAN}}"
	tokenWriterPaintSection = "{{WRITER_PAINT_SECTION}}"
)

const (
	writerToolRuleOff = `不要调用任何工具`
	writerToolRuleOn  = `不要调用其中任何一个；你自己唯一可用的工具是 paint(配图)`

	writerImageBanOff = `和图片`
	writerImageBanOn  = `和外部图片(只有 paint 返回的地址可以写成图片，见 <illustration>)`

	writerPaintSectionOff = ``
	writerPaintSectionOn  = `
	<illustration>
		<rule>你可以调用 paint 工具为正文配图，画面要求见工具说明。图片只能使用 paint 返回的地址，以单独成段的 ![一句话说明](地址) 写进正文，这是上面“只允许这几种标记”之外唯一允许的标记；不要编造地址，不要引用外部图片。</rule>
		<rule>配图是正文的一部分：先写完这张图之前的文字，再调用 paint；拿到地址后，下一步先写出图片那一行，再接着往下写，不要重复已写内容。图片与文字互相配合，不能替代叙述。</rule>
		<rule>主动配图，不要等 KP 或玩家要求，也不要吝啬：新地点或场景切换、重要人物首次登场、情绪与氛围的关键转折、发现重要线索或道具、战斗或追逐等高张力瞬间，都应该配一张图；一段有这些时机的正文却一张图都没有，是遗漏。每张图画不同的瞬间，不要为同一个画面重复配图。</rule>
		<rule>图片的位置就是它所描绘的那一刻在正文里的位置：先把这一刻写成至少一整段文字，再调用 paint，图片紧跟在这段文字之后。新场景、新人物登场的配图，放在介绍它的第一段之后。第一次调用 paint 之前必须已经写出正文，不要一开篇就配图；也不要把图片集中堆在开头或结尾。</rule>
		<rule>一次只调用一个 paint；写出这张图的图片行、再接着写下去之后，才可以为下一张图调用 paint。相邻两张图之间至少隔一段正文。</rule>
		<rule>图片说明只用一句话概括画面，不得泄露玩家尚未获知的信息。</rule>
	</illustration>`

	writerInstructionToolClauseOff = `不要调用工具`
	writerInstructionToolClauseOn  = `只有 paint 可以调用(用于配图，遇到新场景、人物登场、关键转折等时机应主动配图；调用前必须先写出这张图所描绘的那段正文，图片跟在对应情节之后，不要一上来就配图)，不要调用其它工具`
)

// renderWriterPaint 按 paintOK 渲染 Writer 提示词里的配图相关片段；关闭时与引入 paint 之前的提示词逐字节一致。
func renderWriterPaint(tpl string, paintOK bool) string {
	pick := func(on, off string) string {
		if paintOK {
			return on
		}
		return off
	}
	return strings.NewReplacer(
		tokenWriterToolRule, pick(writerToolRuleOn, writerToolRuleOff),
		tokenWriterImageBan, pick(writerImageBanOn, writerImageBanOff),
		tokenWriterPaintSection, pick(writerPaintSectionOn, writerPaintSectionOff),
	).Replace(tpl)
}

// IllustrationStarter 为一次 paint 调用登记并启动后台出图，返回正文里要使用的占位地址。
type IllustrationStarter func(req ImagePromptRequest) string

type writerIllustratorKey struct{}

// WithWriterIllustrator 挂上配图启动器；ctx 里没有它时 Writer 不提供 paint 工具。
func WithWriterIllustrator(ctx context.Context, fn IllustrationStarter) context.Context {
	return context.WithValue(ctx, writerIllustratorKey{}, fn)
}

// WriterIllustratorFrom 取出 ctx 里的配图启动器，没有时返回 nil。
func WriterIllustratorFrom(ctx context.Context) IllustrationStarter {
	fn, _ := ctx.Value(writerIllustratorKey{}).(IllustrationStarter)
	return fn
}

// IllustrationPlaceholder 返回第 slot 张配图在正文里的占位地址。
func IllustrationPlaceholder(slot int) string {
	return fmt.Sprintf("%s%d", illustrationScheme, slot)
}

// writerPaintHandle 选择本次配图使用的 Painter：nsfw 只走 painter_nsfw，不回落；provider 必须能出图。
func writerPaintHandle(handles map[models.AgentRole]agentHandle, nsfw bool) (agentHandle, bool) {
	h, ok := pickPainterHandle(handles, nsfw)
	if !ok {
		return agentHandle{}, false
	}
	if _, ok := h.provider.(llm.ImageGenerator); !ok {
		return agentHandle{}, false
	}
	return h, true
}

// writerPaintAvailable 判断本次 Writer 是否应该提供 paint：普通 painter 可用，或房间开了 NSFW 且 painter_nsfw 可用。
func writerPaintAvailable(handles map[models.AgentRole]agentHandle, roomNSFW bool) bool {
	if _, ok := writerPaintHandle(handles, false); ok {
		return true
	}
	if roomNSFW {
		_, ok := writerPaintHandle(handles, true)
		return ok
	}
	return false
}

type writerPaintArgs struct {
	Prompt string `json:"prompt"`
	Aspect string `json:"aspect"`
	NSFW   bool   `json:"nsfw"`
}

// execWriterPaint 执行 Writer 的一次工具调用，返回回传给模型的结果；成功启动配图时第二个返回值是占位地址。
func execWriterPaint(call llm.ToolCall, gctx GameContext, handles map[models.AgentRole]agentHandle, start IllustrationStarter) (string, string) {
	if call.Name != writerPaintToolName {
		return "SYSTEM REJECT: 你只能调用 paint；记录里的其他工具属于 KP，已经执行完毕。继续写正文。", ""
	}
	var args writerPaintArgs
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		return "SYSTEM REJECT: paint 的参数不是合法 JSON。继续写正文，不要插入图片。", ""
	}
	prompt := strings.TrimSpace(args.Prompt)
	if prompt == "" {
		return "SYSTEM REJECT: paint 的 prompt 不能为空。继续写正文，不要插入图片。", ""
	}
	nsfw := gctx.Session.EnableNSFW && args.NSFW
	if _, ok := writerPaintHandle(handles, nsfw); !ok {
		reason := "配图不可用"
		if nsfw {
			reason = "配图不可用(NSFW 配图未启用)"
		}
		debugf("Writer", "session=%d paint rejected nsfw=%v prompt_len=%d", gctx.Session.ID, nsfw, len([]rune(prompt)))
		return reason + "。继续写正文，不要插入图片。", ""
	}
	url := start(ImagePromptRequest{Prompt: prompt, Aspect: normalizeImageAspect(args.Aspect), NSFW: nsfw})
	debugf("Writer", "session=%d paint started url=%s aspect=%s nsfw=%v prompt_len=%d", gctx.Session.ID, url, normalizeImageAspect(args.Aspect), nsfw, len([]rune(prompt)))
	return fmt.Sprintf("已开始绘制，无需等待。图片地址：%s。下一步先把 ![一句话说明](%s) 单独成段写出来，再接着上文继续写，不要重复已写内容。", url, url), url
}

var (
	writerImagePattern    = regexp.MustCompile(`!\[[^\]]*\]\(\s*([^)\s]*)[^)]*\)`)
	writerBlankRunPattern = regexp.MustCompile(`\n{3,}`)
)

// normalizeIllustrationRefs 规整 Writer 正文里的图片引用：删掉不是本次分配的地址(编造的、外链)和重复出现的占位，
// 让每张图单独成段，并把已分配却漏写的占位补到正文末尾。
func normalizeIllustrationRefs(text string, allocated []string) string {
	if !strings.Contains(text, "![") && len(allocated) == 0 {
		return text
	}
	valid := make(map[string]bool, len(allocated))
	for _, u := range allocated {
		valid[u] = true
	}
	seen := map[string]bool{}
	text = writerImagePattern.ReplaceAllStringFunc(text, func(m string) string {
		url := writerImagePattern.FindStringSubmatch(m)[1]
		if !valid[url] || seen[url] {
			return ""
		}
		seen[url] = true
		return "\n\n" + m + "\n\n"
	})
	for _, u := range allocated {
		if !seen[u] {
			seen[u] = true
			text += "\n\n![配图](" + u + ")"
		}
	}
	return strings.TrimSpace(writerBlankRunPattern.ReplaceAllString(text, "\n\n"))
}

// joinWriterRounds 把各续写轮的正文按段落拼起来。
func joinWriterRounds(text, next string) string {
	switch {
	case next == "":
		return text
	case text == "":
		return next
	}
	return text + "\n\n" + next
}

// writerEmitter 记录已经交给上层的正文字节数和末尾，续写轮中途断流时据此只撤回当前轮的正文。
type writerEmitter struct {
	onToken func(string)
	written int
	tail    string
}

func (e *writerEmitter) emit(s string) {
	if s == "" {
		return
	}
	e.written += len(s)
	e.tail += s
	if len(e.tail) > 2 {
		e.tail = e.tail[len(e.tail)-2:]
	}
	if e.onToken != nil {
		e.onToken(s)
	}
}

func (e *writerEmitter) restore(written int, tail string) {
	e.written, e.tail = written, tail
}

// appendWriterStreamTools 是 Writer 提供 paint 时的续写循环：每轮 tool_choice=auto 流式请求，
// 模型调用 paint 就立即回传占位地址并开下一轮，直到某一轮不再调用工具。
// 拒绝判定只作用于第一轮；流中途 EOF 时只丢弃并重发当前轮，已经写出的正文和已启动的配图保持不变。
func appendWriterStreamTools(ctx context.Context, h agentHandle, streamer llm.ToolStreamer, state *WriterState,
	msgs []llm.ChatMessage, tools []llm.ToolDefinition, gctx GameContext, handles map[models.AgentRole]agentHandle,
	start IllustrationStarter, nsfwMode bool, onToken func(string)) error {

	if !h.isEnabled() {
		return fmt.Errorf("writer agent 未配置或未启用")
	}
	debugf("Writer", "msgs=%d tools=%d nsfw=%v paint=on", len(msgs), len(tools), nsfwMode)
	cacheKey := h.cacheKey(fmt.Sprintf("%v", gctx.Session.ID))
	msgs = append([]llm.ChatMessage(nil), msgs...)

	var (
		text      string
		allocated []string
		failures  int
	)
	em := &writerEmitter{onToken: onToken}
	finish := func(err error) error {
		appendWriterBuffer(state, normalizeIllustrationRefs(text, allocated))
		return err
	}

	for round := 1; round <= writerMaxToolRounds; round++ {
		var res llm.ToolChatResult
		var content string
		for {
			w0, t0 := em.written, em.tail
			pendingSep := round > 1 && w0 > 0 && !strings.HasSuffix(t0, "\n\n")
			forward := func(s string) {
				if s == "" {
					return
				}
				if pendingSep {
					pendingSep = false
					em.emit("\n\n")
				}
				em.emit(s)
			}
			var gate writerRefusalGate
			onText := forward
			if round == 1 {
				onText = func(chunk string) { forward(gate.feed(chunk)) }
			}

			var err error
			res, err = streamer.ChatStreamTools(ctx, cacheKey, msgs, tools, onText)
			if round == 1 {
				forward(gate.eof())
			}
			content = stripWriterControlTags(res.Content)

			if err != nil {
				// NOTE: 流在响应完成前被关闭(EOF)时，当前轮的半截正文整段丢弃重新请求，不能和重试的正文拼在一起。
				if llm.IsUnexpectedEOF(err) && ctx.Err() == nil && failures+1 < writerMaxGenerateAttempts {
					failures++
					alog.Warn("writer stream interrupted, retrying", "session", gctx.Session.ID, "round", round,
						"attempt", failures, "max_attempts", writerMaxGenerateAttempts, "partial_len", len([]rune(content)), "err", err)
					em.restore(w0, t0)
					notifyWriterRestart(ctx, w0)
					select {
					case <-ctx.Done():
						return finish(ctx.Err())
					case <-time.After(writerStreamRetryDelay):
					}
					continue
				}
				text = joinWriterRounds(text, content)
				return finish(err)
			}
			if round == 1 && (hasWriterRefusalPrefix(content) || (content == "" && len(res.ToolCalls) == 0)) {
				failures++
				debugf("Writer", "stream response rejected attempt=%d/%d preview=%s", failures, writerMaxGenerateAttempts, truncateRunes(content, 100))
				if failures >= writerMaxGenerateAttempts {
					return fmt.Errorf("writer stream response rejected after %d attempts", writerMaxGenerateAttempts)
				}
				em.restore(w0, t0)
				continue
			}
			break
		}

		text = joinWriterRounds(text, content)
		if len(res.ToolCalls) == 0 {
			return finish(nil)
		}
		if round == writerMaxToolRounds {
			alog.Warn("writer paint round limit reached", "session", gctx.Session.ID, "rounds", round)
			return finish(nil)
		}

		msgs = append(msgs, llm.ChatMessage{
			Role: "assistant", Content: res.Content, ToolCalls: res.ToolCalls,
			Reasoning: res.Reasoning, ReasoningBlocks: res.ReasoningBlocks,
		})
		for _, call := range res.ToolCalls {
			result, url := execWriterPaint(call, gctx, handles, start)
			if url != "" {
				allocated = append(allocated, url)
			}
			msgs = append(msgs, llm.ChatMessage{Role: "tool", ToolCallID: call.ID, Content: result})
		}
	}
	return finish(nil)
}

func writerPaintTool() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name: writerPaintToolName,
		Description: `为正文配一张图，用来增强沉浸感。应主动使用，不要等玩家要求，数量不限：新地点/新场景切换、重要NPC首次登场、氛围与情绪的关键转折、发现重要线索或道具、战斗/追逐等高张力瞬间，都是配图的好时机，倾向于配图而不是省略；但每张图都要画不同的瞬间，不要为同一个画面重复配图。每次调用只画一格。可选参数 aspect 控制画面方向：场景全景、建筑外观、开阔环境、群像用 landscape(横图)；单角色立绘、近景特写用 portrait(竖图)；不确定时省略或用 square(方图)。
【调用方式】调用 paint 会立即返回一个图片地址，图片在后台绘制，不需要等待，你也看不到画出来的样子。先把这张图所描绘的那一刻写成正文(至少一整段)，再调用 paint，图片会出现在调用之后、紧跟这段文字的位置；不要在写出任何正文之前调用，也不要在同一次回复里连续调用多个 paint。拿到地址后的下一步，先把 ![一句话说明](图片地址) 单独成段写出来，再接着往下写正文，不要重复已写内容。图片地址必须原样使用，不要自己编地址，也不要引用任何外部图片。
【格式规则】prompt用中文写成一段连贯的自然语言描述，不加标题、不用列表、不用markdown，但长度不限：把这一格的所有细节写全，越详细越好，不要省略，不要用"等""之类""若干"概括。绘图模型只会画你写到的东西，没写的就是空白或随机发挥。至少逐项交代：①景别、镜头角度与构图重心；②每个出场人物的发型发色、瞳色、脸型与年龄感、体型、服饰逐件(颜色、材质、款式、磨损或污渍)、配饰与手持物品、表情、视线方向、姿势与手部动作、在画面中的位置和彼此的相对位置；③场景的地点类型、室内或室外、空间大小与布局、背景与前景的每一件显眼物件(材质、颜色、状态)、地面与墙面、天气、时间、季节；④光源种类与方向、明暗对比、阴影落点、整体色调、烟雾灰尘水汽等空气质感，以及这一刻的情绪氛围。人物外貌只能来自最后一条消息里 character 块的 app(外貌)和 eq(当前穿戴或手持的装备)，以及记录里已经写明的NPC外貌；细节只能来自已交代的剧情，不得为了凑细节编造剧情事实、人物设定或未鉴定的神话信息；纯环境空镜没有人物就不写人物。不要写"避免xxx"之类的负面提示。
【风格规则】每张配图是一部连续日式ACG漫画里的一格，不是孤立的插画。统一日式ACG漫画画风(粗细有变化的墨线、赛璐璐上色、网点式阴影)，该画风由绘图代理统一附加，prompt里不必重复描述画风，只写这一格的内容。纯环境/痕迹类空镜同样按日式ACG漫画格来画。
【分镜规则】一张图只画一个瞬间、一个单格：不要要求多格拼版、分栏或画面边框，不要要求对话气泡、拟声词或任何可读文字(画面里的文字会变成乱码，台词留给正文)。用分镜的镜头语言选景别，并随剧情节奏变化：相邻两格不要重复同一景别和构图，每一格都应推进剧情(新的动作、新的反应、新的发现)，而不是换个角度重画上一格。
【连续性规则】读者会把相邻的配图连起来当日式ACG漫画读。同一角色在每一格里必须保持同一套外貌：发型发色、瞳色、服装和显眼配饰逐项沿用 character 块和记录里的描述，不要擅自换衣服或改发色(除非剧情里确实发生了改变)；绘图模型不记得前几格，所以每一格都要把人物外貌和场景陈设完整重写一遍，不能写"同上""与前几格一致"来省略；同一地点沿用已经出现过的标志性陈设和布局；人物的左右位置与视线方向在连续的几格里保持一致。
【NSFW规则】必填参数nsfw(布尔)：本次画面若包含裸露、性暗示或露骨性描写等NSFW内容，必须如实设为true；不含这类内容或不确定时明确填false，不可省略。nsfw=true只在房间已开启NSFW开关时生效，房间未开启NSFW时该参数被忽略，按普通全年龄配图处理；房间已开启但未配置并启用NSFW配图代理时，nsfw=true的调用会被直接拒绝且不会生成图片，此时不要在正文里插入图片。
【未知感规则】配图是玩家可见通道，同样受[UNKNOWN]约束。尚未被合法鉴定的神话实体、怪物与异常现象，禁止画出可辨认的正面全貌——正面肖像等于提前把答案交给玩家。这类对象只画它留下的痕迹与效果、事后现场、遮挡与局部(背光剪影、水下轮廓、门缝里的一部分)、它经过后的空环境，或目击者的反应；prompt里也不要写出它的正式名称、种族名与规则术语，用画面本身描述。已被完全鉴定或已正面遭遇过的对象不受此限制。
调用示例：{"prompt":"低角度中景，镜头从书桌斜前方仰拍，画面重心落在人物上半身和他身后的房门。一位年迈的管家僵立在堆满散乱纸张的红木书桌旁，约六十岁，瘦高，背微驼，脸颊凹陷，皱纹很深，灰色侧分短发，眼珠是浅褐色，嘴唇紧抿；他身穿熨烫平整但袖口磨白的黑色燕尾服、白衬衫和黑色领结，胸前口袋露出一截怀表银链，左手端着一座插着半截白蜡烛的黄铜烛台，烛火向房门方向弯去，右手扶着桌沿，指节发白，肩膀绷紧，脖子前伸，视线死死盯着画面右侧的房门。背景是午夜昏暗的维多利亚式书房，两侧是顶到天花板的深色橡木书架，书脊褪色，书架前有一架带滑轨的木梯，书桌上有翻开的账簿、墨水瓶、羽毛笔和一只倒下的玻璃杯，深红色波斯地毯边缘卷起，右侧的房门半开，门缝里透出一线黑暗。光源只有烛火和墙上泛着病态绿光的煤气灯，暖黄的烛光在管家脸侧形成强烈明暗对比，绿光从左上方斜照在书架上，地毯和书桌下拖出长而深的阴影，空气中浮着细小的灰尘，整体色调阴冷压抑，气氛紧张。","aspect":"landscape","nsfw":false}`,
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"prompt": {"type": "string", "description": "中文画面描述,一段连贯的自然语言,不加标题、不用列表,长度不限,把人物外貌服饰表情姿势、场景布局物件、光线色调氛围等所有细节写全,越详细越好,不要省略,具体要求见工具说明"},
				"aspect": {
					"type": "string",
					"enum": ["landscape", "portrait", "square"],
					"description": "可选,画面方向。landscape=横图,适合场景全景、建筑外观、开阔环境、群像、战斗追逐的空间关系;portrait=竖图,适合单个角色立绘、近景特写、高耸或纵深的空间;square=方图,通用场景。省略时按square处理"
				},
				"nsfw": {
					"type": "boolean",
					"description": "必填。本次画面是否包含裸露、性暗示或露骨性描写等NSFW内容;如实标记"
				}
			},
			"required": ["prompt", "nsfw"]
		}`),
	}
}
