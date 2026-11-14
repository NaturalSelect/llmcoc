// NOTE: live_test.go 是联网冒烟测试，默认跳过；只有设置 LLMCOC_LIVE_ONLINEMOD=1 才会真的访问蓝奏云。
// 用法：LLMCOC_LIVE_ONLINEMOD=1 go test -run TestLiveLanzouDownload ./internal/services/onlinemod/ -v
// CI 不设这个变量，所以不会联网。
package onlinemod

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"
)

func TestLiveLanzouDownload(t *testing.T) {
	if os.Getenv("LLMCOC_LIVE_ONLINEMOD") != "1" {
		t.Skip("设置 LLMCOC_LIVE_ONLINEMOD=1 才运行联网测试")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	c := NewClient()

	direct, filename, err := c.ResolveLanzou(ctx, "https://wwbth.lanzov.com/iNWGo4fuqqli")
	if err != nil {
		t.Fatalf("ResolveLanzou: %v", err)
	}
	t.Logf("direct=%s filename=%q", direct, filename)

	data, err := c.Download(ctx, direct, 50<<20)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	t.Logf("downloaded %d bytes", len(data))
	if !bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		t.Fatalf("不是 zip，开头 = %q", data[:min(16, len(data))])
	}

	docs, err := Extract(data, filename)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	candidates := 0
	for _, d := range docs {
		t.Logf("doc %s kind=%s runes=%d candidate=%v", d.Path, d.Kind, d.Runes, d.Candidate)
		if d.Candidate {
			candidates++
		}
	}
	if candidates == 0 {
		t.Fatalf("没有任何候选正文（共 %d 篇）", len(docs))
	}
}
