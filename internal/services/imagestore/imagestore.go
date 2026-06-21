// NOTE: Package imagestore 负责把生成图片落盘为可缓存的静态文件。
package imagestore

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/llmcoc/server/internal/config"
)

var (
	ErrInvalidHash = errors.New("invalid image hash")
	ErrNotFound    = errors.New("image not found")
)

type Ref struct {
	Hash      string
	MIME      string
	Extension string
	Path      string
}

type StoredFile struct {
	Hash string
	MIME string
	Path string
	Info os.FileInfo
}

type Store struct {
	Dir string
}

type CleanupReport struct {
	Deleted int
	Errors  []error
}

type imageFormat struct {
	MIME      string
	Extension string
}

var supportedFormats = []imageFormat{
	{MIME: "image/png", Extension: ".png"},
	{MIME: "image/jpeg", Extension: ".jpg"},
	{MIME: "image/webp", Extension: ".webp"},
}

var (
	defaultDirMu       sync.RWMutex
	defaultDirOverride string
)

func SetDefaultDir(dir string) func() {
	defaultDirMu.Lock()
	prev := defaultDirOverride
	defaultDirOverride = dir
	defaultDirMu.Unlock()
	return func() {
		defaultDirMu.Lock()
		defaultDirOverride = prev
		defaultDirMu.Unlock()
	}
}

func DefaultDir() string {
	defaultDirMu.RLock()
	override := defaultDirOverride
	defaultDirMu.RUnlock()
	if override != "" {
		return override
	}
	if env := strings.TrimSpace(os.Getenv("LLMCOC_IMAGE_DIR")); env != "" {
		return env
	}
	dbPath := strings.TrimSpace(config.Global.Database.Path)
	if dbPath == "" {
		return filepath.Join("data", "images")
	}
	dir := filepath.Dir(dbPath)
	if dir == "." || dir == "" {
		return filepath.Join("data", "images")
	}
	return filepath.Join(dir, "images")
}

func DefaultStore() Store {
	return Store{Dir: DefaultDir()}
}

func New(dir string) Store {
	if strings.TrimSpace(dir) == "" {
		return DefaultStore()
	}
	return Store{Dir: dir}
}

func (s Store) SaveDataURL(dataURL string) (Ref, error) {
	bytes, mime, ext, err := DecodeDataURL(dataURL)
	if err != nil {
		return Ref{}, err
	}
	sum := sha256.Sum256(bytes)
	hash := hex.EncodeToString(sum[:])
	path := s.pathFor(hash, ext)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return Ref{}, err
	}
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		return Ref{Hash: hash, MIME: mime, Extension: ext, Path: path}, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Ref{}, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if errors.Is(err, os.ErrExist) {
		return Ref{Hash: hash, MIME: mime, Extension: ext, Path: path}, nil
	}
	if err != nil {
		return Ref{}, err
	}
	writeErr := error(nil)
	if _, writeErr = file.Write(bytes); writeErr == nil {
		writeErr = file.Close()
	} else {
		_ = file.Close()
	}
	if writeErr != nil {
		_ = os.Remove(path)
		return Ref{}, writeErr
	}
	return Ref{Hash: hash, MIME: mime, Extension: ext, Path: path}, nil
}

func DecodeDataURL(dataURL string) ([]byte, string, string, error) {
	dataURL = strings.TrimSpace(dataURL)
	if !strings.HasPrefix(dataURL, "data:") {
		return nil, "", "", fmt.Errorf("invalid image data URL")
	}
	comma := strings.Index(dataURL, ",")
	if comma < 0 {
		return nil, "", "", fmt.Errorf("invalid image data URL")
	}
	header := dataURL[len("data:"):comma]
	payload := strings.TrimSpace(dataURL[comma+1:])
	parts := strings.Split(header, ";")
	if len(parts) == 0 {
		return nil, "", "", fmt.Errorf("invalid image data URL")
	}
	mime, ext, ok := NormalizeMIME(parts[0])
	if !ok {
		return nil, "", "", fmt.Errorf("unsupported image MIME type")
	}
	hasBase64 := false
	for _, part := range parts[1:] {
		if strings.EqualFold(strings.TrimSpace(part), "base64") {
			hasBase64 = true
			break
		}
	}
	if !hasBase64 {
		return nil, "", "", fmt.Errorf("image data URL must be base64")
	}
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, "", "", fmt.Errorf("decode image data URL: %w", err)
	}
	if len(decoded) == 0 {
		return nil, "", "", fmt.Errorf("empty image data")
	}
	return decoded, mime, ext, nil
}

func NormalizeMIME(mime string) (string, string, bool) {
	mime = strings.ToLower(strings.TrimSpace(mime))
	if mime == "image/jpg" {
		mime = "image/jpeg"
	}
	for _, format := range supportedFormats {
		if mime == format.MIME {
			return format.MIME, format.Extension, true
		}
	}
	return "", "", false
}

func ValidHash(hash string) bool {
	if len(hash) != sha256.Size*2 {
		return false
	}
	for _, r := range hash {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') {
			continue
		}
		return false
	}
	return true
}

func URL(hash string) string {
	return "/api/images/" + strings.ToLower(hash)
}

func (s Store) Resolve(hash string) (StoredFile, error) {
	hash = strings.ToLower(strings.TrimSpace(hash))
	if !ValidHash(hash) {
		return StoredFile{}, ErrInvalidHash
	}
	for _, format := range supportedFormats {
		path := s.pathFor(hash, format.Extension)
		info, err := os.Stat(path)
		if err == nil && info.Mode().IsRegular() {
			return StoredFile{Hash: hash, MIME: format.MIME, Path: path, Info: info}, nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return StoredFile{}, err
		}
	}
	return StoredFile{}, ErrNotFound
}

func (s Store) Open(hash string) (*os.File, StoredFile, error) {
	stored, err := s.Resolve(hash)
	if err != nil {
		return nil, StoredFile{}, err
	}
	file, err := os.Open(stored.Path)
	if err != nil {
		return nil, StoredFile{}, err
	}
	return file, stored, nil
}

func (s Store) CleanupOlderThan(cutoff time.Time) (int, error) {
	report := s.CleanupOlderThanReport(cutoff)
	if len(report.Errors) > 0 {
		return report.Deleted, report.Errors[0]
	}
	return report.Deleted, nil
}

func (s Store) CleanupOlderThanReport(cutoff time.Time) CleanupReport {
	base := s.baseDir()
	if _, err := os.Stat(base); errors.Is(err, os.ErrNotExist) {
		return CleanupReport{}
	} else if err != nil {
		return CleanupReport{Errors: []error{err}}
	}
	report := CleanupReport{}
	_ = filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			report.Errors = append(report.Errors, walkErr)
			return nil
		}
		if entry.IsDir() || !isManagedImageFile(path) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			report.Errors = append(report.Errors, err)
			return nil
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				report.Errors = append(report.Errors, err)
				return nil
			}
			report.Deleted++
		}
		return nil
	})
	return report
}

func StartCleanup(ctx context.Context, store Store, maxAge, interval time.Duration) {
	if maxAge <= 0 {
		maxAge = 14 * 24 * time.Hour
	}
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	go func() {
		run := func() {
			report := store.CleanupOlderThanReport(time.Now().Add(-maxAge))
			for _, err := range report.Errors {
				log.Printf("[images] cleanup failed: %v", err)
			}
			if report.Deleted > 0 {
				log.Printf("[images] cleanup deleted %d old files", report.Deleted)
			}
		}
		run()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()
}

func (s Store) baseDir() string {
	if strings.TrimSpace(s.Dir) == "" {
		return DefaultDir()
	}
	return s.Dir
}

func (s Store) pathFor(hash, ext string) string {
	hash = strings.ToLower(hash)
	return filepath.Join(s.baseDir(), hash[:2], hash+ext)
}

func isManagedImageFile(path string) bool {
	name := filepath.Base(path)
	ext := filepath.Ext(name)
	if ext == "" {
		return false
	}
	hash := strings.TrimSuffix(name, ext)
	if !ValidHash(hash) {
		return false
	}
	for _, format := range supportedFormats {
		if ext == format.Extension {
			return true
		}
	}
	return false
}
