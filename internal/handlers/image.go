package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/services/imagestore"
)

func ServeImage(c *gin.Context) {
	hash := strings.TrimSpace(c.Param("hash"))
	file, stored, err := imagestore.DefaultStore().Open(hash)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, imagestore.ErrInvalidHash) || errors.Is(err, imagestore.ErrNotFound) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": "图片不存在"})
		return
	}
	defer file.Close()

	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Header("Content-Type", stored.MIME)
	http.ServeContent(c.Writer, c.Request, stored.Hash, stored.Info.ModTime(), file)
}
