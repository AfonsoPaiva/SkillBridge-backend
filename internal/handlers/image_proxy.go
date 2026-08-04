package handlers

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	_ "image/gif"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/disintegration/imaging"
	"github.com/gin-gonic/gin"
)

// ── In-process image cache ────────────────────────────────────────────────────
// Keyed by the upstream URL. Stores the resized/compressed bytes and content-type.
// Simple sync.Map; entries never evicted (logos are static). For a 1000-vacancy
// page the cache will hold at most ~1000 logos, typically a few MB.

type cachedImage struct {
	data        []byte
	contentType string
}

var imageCache sync.Map // map[string]*cachedImage

// ── Constants ─────────────────────────────────────────────────────────────────

const (
	// Target size for company logos in the UI (56 px display, 2× for retina).
	logoMaxPx = 120
	// JPEG quality used when re-encoding non-PNG originals.
	jpegQuality = 82
	// 1 year immutable — content is hashed via ?url=... so stale entries never surface.
	cacheControlValue = "public, max-age=31536000, immutable"
)

// ProxyImage fetches an external image, resizes it to logoMaxPx on its longest
// edge, re-encodes it as JPEG (or PNG for transparency) and streams it back
// with a 1-year immutable cache header. Responses are memoised in-process.
func ProxyImage(c *gin.Context) {
	imageURL := c.Query("url")
	if imageURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing URL parameter"})
		return
	}

	// ── Serve from in-process cache ──────────────────────────────────────────
	if cached, ok := imageCache.Load(imageURL); ok {
		entry := cached.(*cachedImage)
		c.Header("Cache-Control", cacheControlValue)
		c.Header("Content-Type", entry.contentType)
		c.Data(http.StatusOK, entry.contentType, entry.data)
		return
	}

	// ── Fetch upstream ───────────────────────────────────────────────────────
	client := &http.Client{Timeout: 8 * time.Second}
	req, err := http.NewRequest(http.MethodGet, imageURL, nil) //nolint:gosec
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Failed to build request"})
		return
	}
	// Mimic a real browser so CDNs (e.g. LinkedIn) don't block the request.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/*,*/*;q=0.8")
	req.Header.Set("Accept-Language", "pt-PT,pt;q=0.9,en;q=0.8")
	if strings.Contains(imageURL, "linkedin.com") {
		req.Header.Set("Referer", "https://www.linkedin.com/")
	}
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Failed to fetch image"})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.JSON(resp.StatusCode, gin.H{"error": "Upstream error"})
		return
	}

	// Read body (cap at 10 MB to avoid abuse)
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read image"})
		return
	}

	// ── Decode ────────────────────────────────────────────────────────────────
	src, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		// Cannot decode — pass through the original bytes unchanged.
		ct := resp.Header.Get("Content-Type")
		if ct == "" {
			ct = http.DetectContentType(raw)
		}
		store := &cachedImage{data: raw, contentType: ct}
		imageCache.Store(imageURL, store)
		c.Header("Cache-Control", cacheControlValue)
		c.Data(http.StatusOK, ct, raw)
		return
	}

	// ── Resize (only if larger than target) ──────────────────────────────────
	b := src.Bounds()
	maxDim := b.Dx()
	if b.Dy() > maxDim {
		maxDim = b.Dy()
	}
	if maxDim > logoMaxPx {
		src = imaging.Fit(src, logoMaxPx, logoMaxPx, imaging.Lanczos)
	}

	// ── Encode ────────────────────────────────────────────────────────────────
	var buf bytes.Buffer
	var contentType string

	hasPNG := format == "png" || strings.Contains(resp.Header.Get("Content-Type"), "png")
	if hasPNG {
		// Keep PNG to preserve transparency (e.g. logos with alpha channel).
		if err2 := png.Encode(&buf, src); err2 != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Encode error"})
			return
		}
		contentType = "image/png"
	} else {
		// Re-encode everything else as JPEG for maximum compression.
		if err2 := jpeg.Encode(&buf, src, &jpeg.Options{Quality: jpegQuality}); err2 != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Encode error"})
			return
		}
		contentType = "image/jpeg"
	}

	// ── Cache & respond ───────────────────────────────────────────────────────
	data := buf.Bytes()
	store := &cachedImage{data: data, contentType: contentType}
	imageCache.Store(imageURL, store)

	c.Header("Cache-Control", cacheControlValue)
	c.Data(http.StatusOK, contentType, data)
}
