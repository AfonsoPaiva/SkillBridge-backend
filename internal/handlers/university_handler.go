package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/paiva/SkillBridge/Backend/config"
)

// ListUniversities returns the unique list of educação establishments.
//
// @Summary      Listar estabelecimentos de ensino superior
// @Description  Devolve todas as universidades/institutos presentes no ficheiro UniversidadesECursos.json
// @Tags         universities
// @Produce      json
// @Success      200  {array}   string
// @Router       /universities [get]
func ListUniversities(c *gin.Context) {
	c.JSON(http.StatusOK, config.UnivList)
}

// ListCoursesByUniversity returns all cursos for a given estabelecimento.
//
// The client must provide the `estabelecimento` query parameter.  Matching is
// case-sensitive and must exactly equal the string stored in the JSON file.
//
// @Summary      Listar cursos de um estabelecimento
// @Tags         universities
// @Produce      json
// @Param        estabelecimento  query  string  true  "Nome do estabelecimento"
// @Success      200  {array}   string
// @Failure      400  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /universities/courses [get]
func ListCoursesByUniversity(c *gin.Context) {
	est := c.Query("estabelecimento")
	if est == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Parâmetro 'estabelecimento' obrigatório."})
		return
	}

	courses, ok := config.CoursesByUniv[est]
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "Estabelecimento não encontrado."})
		return
	}

	c.JSON(http.StatusOK, courses)
}
