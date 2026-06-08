package handlers

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

// ProxyImage handler acts as a proxy for external images (e.g., LinkedIn logos)
// to bypass adblockers on the client side.
func ProxyImage(c *gin.Context) {
	imageURL := c.Query("url")
	if imageURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing URL parameter"})
		return
	}

	// Fetch the image
	resp, err := http.Get(imageURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch image"})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.JSON(resp.StatusCode, gin.H{"error": "Failed to fetch image"})
		return
	}

	// Forward headers
	contentType := resp.Header.Get("Content-Type")
	if contentType != "" {
		c.Header("Content-Type", contentType)
	}
	// Cache for a long time since logos rarely change
	c.Header("Cache-Control", "public, max-age=86400")

	// Stream the body
	_, err = io.Copy(c.Writer, resp.Body)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
	}
}
