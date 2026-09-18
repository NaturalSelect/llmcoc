// NOTE: 覆盖 anthropic_provider.go 里 buildParams 对 thinkingLevel 的处理：合法档位应
// 开启 adaptive thinking 并设置 output_config.effort，同时让 temperature 跟随 API 默认值
// （不能显式传自定义值，Anthropic 扩展思考与自定义 temperature 互斥）；none/空/未识别档位
// 保持原有行为，仍按 disableTemperature/temperature 走。纯内存构造，不涉及网络。
package llm

import "testing"

func TestBuildParams_ThinkingLevelEnablesAdaptiveThinking(t *testing.T) {
	p := newAnthropicProvider("", "", "claude-x", 0, 0.5, false, "high")
	params, err := p.buildParams([]ChatMessage{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params.Thinking.OfAdaptive == nil {
		t.Fatalf("expected adaptive thinking to be enabled, got: %+v", params.Thinking)
	}
	if params.OutputConfig.Effort != "high" {
		t.Fatalf("expected output_config.effort=high, got %q", params.OutputConfig.Effort)
	}
	if params.Temperature.Valid() {
		t.Fatalf("temperature must not be set when thinking is enabled, got %v", params.Temperature.Value)
	}
}

func TestBuildParams_NoneOrEmptyThinkingLevelKeepsTemperature(t *testing.T) {
	for _, level := range []string{"none", "", "unrecognized"} {
		p := newAnthropicProvider("", "", "claude-x", 0, 0.5, false, level)
		params, err := p.buildParams([]ChatMessage{{Role: "user", Content: "hi"}}, nil)
		if err != nil {
			t.Fatalf("thinkingLevel=%q: unexpected error: %v", level, err)
		}
		if params.Thinking.OfAdaptive != nil {
			t.Fatalf("thinkingLevel=%q: expected thinking to stay disabled, got: %+v", level, params.Thinking)
		}
		if !params.Temperature.Valid() || params.Temperature.Value != 0.5 {
			t.Fatalf("thinkingLevel=%q: expected temperature=0.5 to be set, got valid=%v value=%v",
				level, params.Temperature.Valid(), params.Temperature.Value)
		}
	}
}

func TestBuildParams_DisableTemperatureStillHonoredWithoutThinking(t *testing.T) {
	p := newAnthropicProvider("", "", "claude-x", 0, 0.5, true, "none")
	params, err := p.buildParams([]ChatMessage{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params.Temperature.Valid() {
		t.Fatalf("disableTemperature=true should keep temperature unset, got %v", params.Temperature.Value)
	}
}
