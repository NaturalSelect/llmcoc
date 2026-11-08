// NOTE: 前端静态资源的 NoRoute 处理：Vue 构建产物里的文件按路径返回，其余路径回落到 index.html，由前端路由接管。
package main

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
)

const frontendNotBuilt = "前端未构建：cd frontend && npm ci && npm run build\n"

// frontendHandler 返回挂在 NoRoute 上的处理函数；fsys 的根目录就是前端构建产物目录（含 index.html）。
func frontendHandler(fsys fs.FS) gin.HandlerFunc {
	serveIndex := func(c *gin.Context) {
		data, err := fs.ReadFile(fsys, "index.html")
		if err != nil {
			c.Data(http.StatusServiceUnavailable, "text/plain; charset=utf-8", []byte(frontendNotBuilt))
			return
		}
		// NOTE: index.html 是 SPA 入口，禁止长缓存，避免用户长期停留在旧版本
		c.Header("Cache-Control", "no-cache")
		c.Data(http.StatusOK, "text/html; charset=utf-8", data)
	}

	return func(c *gin.Context) {
		name := strings.TrimPrefix(path.Clean("/"+c.Request.URL.Path), "/")
		// NOTE: http.FileServer 会把 /index.html 重定向到 ./，所以入口文件不走它
		if name == "" || name == "index.html" {
			serveIndex(c)
			return
		}
		// NOTE: 目录和不存在的路径都回落到 index.html（前端路由地址如 /sessions/7 不对应任何文件）
		info, err := fs.Stat(fsys, name)
		if err != nil || info.IsDir() {
			serveIndex(c)
			return
		}
		// NOTE: Vite 生成的 assets 文件名带内容哈希，内容变化文件名就变，可以永久缓存；其他静态文件短期缓存
		if strings.HasPrefix(name, "assets/") {
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			c.Header("Cache-Control", "public, max-age=3600")
		}
		c.FileFromFS(name, http.FS(fsys))
	}
}
