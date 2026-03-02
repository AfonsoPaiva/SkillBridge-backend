package handlers

import (
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/disintegration/imaging"
	"github.com/gin-gonic/gin"
	"github.com/paiva/SkillBridge/Backend/config"
)

// imageType defines resize behaviour per use case.
type imageType struct {
	subDir  string // subdirectory inside UploadsDir
	width   int
	height  int
	fill    bool // true = center-crop to exact size, false = fit inside bounds
	quality int  // JPEG quality (1-100)
}

var imageTypes = map[string]imageType{
	// Square avatar, 400 × 400, high quality
	"avatar": {subDir: "avatars", width: 400, height: 400, fill: true, quality: 85},
	// Project banner, 1280 × 720 (16:9), fit inside — no crop
	"project": {subDir: "projects", width: 1280, height: 720, fill: false, quality: 82},
}

// UploadImage - Faz upload e redimensiona uma imagem, guardando-a localmente.
//
// @Summary      Upload de imagem
// @Description  Faz upload e redimensiona uma imagem (avatar 400x400 ou projeto 1280x720)
// @Tags         upload
// @Accept       multipart/form-data
// @Produce      json
// @Security     BearerAuth
// @Param        type   query     string  false  "Tipo de imagem: avatar (default) ou project"
// @Param        image  formData  file    true   "Ficheiro de imagem (JPEG, PNG, GIF, WebP — máx 10MB)"
// @Success      201  {object}  map[string]string
// @Failure      400  {object}  map[string]string
// @Router       /upload/image [post]
func UploadImage(c *gin.Context) {
	// --- 1. Determinar tipo de imagem ---
	typeName := strings.ToLower(c.DefaultQuery("type", "avatar"))
	imgType, ok := imageTypes[typeName]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Parâmetro 'type' inválido. Use 'avatar' ou 'project'."})
		return
	}

	// --- 2. Ler ficheiro ---
	file, header, err := c.Request.FormFile("image")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Campo 'image' obrigatório."})
		return
	}
	defer file.Close()

	// Validar tamanho (máx. 10 MB)
	const maxSize = 10 << 20
	if header.Size > maxSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Ficheiro demasiado grande. Máximo: 10 MB."})
		return
	}

	// Validar extensão
	ext := strings.ToLower(filepath.Ext(header.Filename))
	allowed := map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true}
	if !allowed[ext] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Formato não suportado. Use JPEG, PNG, GIF ou WebP."})
		return
	}

	// --- 3. Descodificar imagem ---
	src, _, err := image.Decode(file)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Não foi possível processar a imagem."})
		return
	}

	// --- 4. Redimensionar ---
	var resized *image.NRGBA
	if imgType.fill {
		// Center-crop para tamanho exato (avatares quadrados)
		resized = imaging.Fill(src, imgType.width, imgType.height, imaging.Center, imaging.Lanczos)
	} else {
		// Fit dentro dos limites, mantendo proporção (imagens de projeto)
		resized = imaging.Fit(src, imgType.width, imgType.height, imaging.Lanczos)
	}

	// --- 5. Criar diretoria de destino ---
	destDir := filepath.Join(config.AppConfig.UploadsDir, imgType.subDir)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao criar diretoria de uploads."})
		return
	}

	// --- 6. Guardar sempre como JPEG (melhor compressão) ---
	firebaseUID := c.GetString("firebase_uid")
	filename := fmt.Sprintf("%d_%s.jpg", time.Now().UnixMilli(), firebaseUID)
	destPath := filepath.Join(destDir, filename)

	if err := imaging.Save(resized, destPath, imaging.JPEGQuality(imgType.quality)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao guardar imagem."})
		return
	}

	// URL pública: /uploads/avatars/<filename>.jpg  ou  /uploads/projects/<filename>.jpg
	relativePath := fmt.Sprintf("/uploads/%s/%s", imgType.subDir, filename)
	// In production, prefix with the backend base URL so the frontend (on a different domain) can resolve images
	publicURL := relativePath
	if config.AppConfig.Env == "production" && config.AppConfig.BackendURL != "" {
		publicURL = config.AppConfig.BackendURL + relativePath
	}
	c.JSON(http.StatusCreated, gin.H{
		"url":      publicURL,
		"type":     typeName,
		"filename": filename,
	})
}

// DeleteImage - Remove uma imagem local
//
// @Summary      Remover imagem
// @Description  Remove uma imagem local do servidor
// @Tags         upload
// @Produce      json
// @Security     BearerAuth
// @Param        filename  query  string  true  "Caminho relativo da imagem (ex: avatars/xxx.jpg)"
// @Success      200  {object}  map[string]string
// @Failure      400  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /upload/image [delete]
func DeleteImage(c *gin.Context) {
	filename := c.Query("filename")
	if filename == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Parâmetro 'filename' obrigatório."})
		return
	}

	// Segurança: rejeitar traversal de diretoria
	if strings.Contains(filename, "..") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Caminho inválido."})
		return
	}

	fullPath := filepath.Join(config.AppConfig.UploadsDir, filename)
	if err := os.Remove(fullPath); err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Imagem não encontrada."})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao remover imagem."})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Imagem removida com sucesso."})
}
