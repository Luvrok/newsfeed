package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	_ "image/gif"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const maxImageBytes = 16 << 20

// handleImage is a small caching proxy for feed images. Browsers in networks
// where the publisher's CDN is unreachable still see the picture, because the
// server downloads it with its own proxy settings.
func (s *Server) handleImage(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.URL.Query().Get("u"))
	target, ok := safeImageTarget(raw)
	if !ok {
		http.Error(w, "invalid image url", http.StatusBadRequest)
		return
	}
	if s.imageDir == "" {
		http.Error(w, "image cache is not configured", http.StatusServiceUnavailable)
		return
	}

	width := 1600
	if rawWidth := r.URL.Query().Get("w"); rawWidth != "" {
		if n, err := strconv.Atoi(rawWidth); err == nil && n >= 200 && n <= 2000 {
			width = n
		}
	}

	sum := sha256.Sum256([]byte(target + "|" + strconv.Itoa(width)))
	key := hex.EncodeToString(sum[:])
	path := filepath.Join(s.imageDir, key)
	typePath := path + ".type"

	lockValue, _ := s.imageLocks.LoadOrStore(key, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	if contentType, err := os.ReadFile(typePath); err == nil {
		if file, err := os.Open(path); err == nil {
			defer file.Close()
			info, err := file.Stat()
			if err == nil {
				writeImageHeaders(w, string(contentType), key)
				http.ServeContent(w, r, "", info.ModTime(), file)
				return
			}
		}
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
	if err != nil {
		http.Error(w, "invalid image url", http.StatusBadRequest)
		return
	}
	req.Header.Set("Accept", "image/avif,image/webp,image/png,image/jpeg,image/gif,image/*;q=0.8")
	resp, err := s.imageClient.Do(req)
	if err != nil {
		s.log.Warn("image fetch", "url", target, "err", err)
		http.Error(w, "image unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		http.Error(w, "image unavailable", http.StatusBadGateway)
		return
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil || len(data) == 0 {
		http.Error(w, "image unavailable", http.StatusBadGateway)
		return
	}
	if len(data) > maxImageBytes {
		http.Error(w, "image too large", http.StatusRequestEntityTooLarge)
		return
	}
	contentType := imageContentType(resp.Header.Get("Content-Type"), data)
	if contentType == "" {
		http.Error(w, "not an image", http.StatusUnsupportedMediaType)
		return
	}
	data, contentType = resizeImage(data, contentType, width)

	if err := os.WriteFile(path+".tmp", data, 0o644); err == nil {
		_ = os.Rename(path+".tmp", path)
		_ = os.WriteFile(typePath, []byte(contentType), 0o644)
	}

	writeImageHeaders(w, contentType, key)
	_, _ = w.Write(data)
}

func writeImageHeaders(w http.ResponseWriter, contentType, key string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
	w.Header().Set("ETag", `"`+key+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

// imageContentType trusts the header only for raster images. SVG is rejected
// because it can carry script, and the sniffed type is used otherwise.
func imageContentType(header string, data []byte) string {
	ctype := strings.ToLower(strings.TrimSpace(strings.Split(header, ";")[0]))
	if strings.Contains(ctype, "svg") {
		return ""
	}
	if strings.HasPrefix(ctype, "image/") {
		return ctype
	}
	detected := http.DetectContentType(data)
	if strings.HasPrefix(detected, "image/") && !strings.Contains(detected, "svg") {
		return detected
	}
	return ""
}

func safeImageTarget(raw string) (string, bool) {
	if raw == "" || len(raw) > 4096 {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return "", false
	}
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return "", false
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
			ip.IsUnspecified() || ip.IsMulticast() {
			return "", false
		}
	}
	return u.String(), true
}

// resizeImage keeps feed images sane: publishers often ship 8000px photos,
// while the reader needs at most 1600px. The original bytes are returned when
// the image is already small, animated, or in a format Go cannot decode.
func resizeImage(data []byte, contentType string, width int) ([]byte, string) {
	if width <= 0 || contentType == "image/gif" || contentType == "image/avif" {
		return data, contentType
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= width || cfg.Width <= 0 || cfg.Height <= 0 {
		return data, contentType
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data, contentType
	}
	height := cfg.Height * width / cfg.Width
	if height < 1 {
		height = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)

	var buf bytes.Buffer
	if contentType == "image/png" {
		if err := png.Encode(&buf, dst); err == nil {
			return buf.Bytes(), "image/png"
		}
	} else if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85}); err == nil {
		return buf.Bytes(), "image/jpeg"
	}
	return data, contentType
}
