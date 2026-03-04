package handlers

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/disintegration/imaging"
	"github.com/gin-gonic/gin"
	"github.com/paiva/SkillBridge/Backend/config"
	"github.com/paiva/SkillBridge/Backend/internal/storage"
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

// UploadImage - Faz upload e redimensiona uma imagem, guardando-a no Google Cloud Storage.
//
// @Summary      Upload de imagem
// @Description  Faz upload e redimensiona uma imagem (avatar 400x400 ou projeto 1280x720), guardando no GCS
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

	// --- 5. Encode image to JPEG in memory ---
	firebaseUID := c.GetString("firebase_uid")
	filename := fmt.Sprintf("%d_%s.jpg", time.Now().UnixMilli(), firebaseUID)
	objectName := fmt.Sprintf("%s/%s", imgType.subDir, filename)

	// Encode to JPEG in a buffer
	buf := new(bytes.Buffer)
	if err := imaging.Encode(buf, resized, imaging.JPEG, imaging.JPEGQuality(imgType.quality)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao processar imagem."})
		return
	}

	// --- 6. Upload to Google Cloud Storage ---
	publicURL, err := storage.UploadFile(objectName, buf, "image/jpeg")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Erro ao fazer upload da imagem.",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"url":      publicURL,
		"type":     typeName,
		"filename": filename,
	})
}

// DeleteImage - Remove uma imagem local
//
// @Summary      Remover imagemdo Google Cloud Storage
//
// @Summary      Remover imagem
// @Description  Remove uma imagem do GCS
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

	// Delete from GCS
	if err := storage.DeleteFile(filename); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Erro ao remover imagem.",
			"details": err.Error(),
		

	c.JSON(http.StatusOK, gin.H{"message": "Imagem removida com sucesso."})
}
