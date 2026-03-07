package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/paiva/SkillBridge/Backend/config"
)

// UniversitySearchResult represents a university with its courses.
type UniversitySearchResult struct {
	Estabelecimento string   `json:"estabelecimento"`
	TotalCursos     int      `json:"total_cursos"`
	Cursos          []string `json:"cursos,omitempty"`
}

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

// SearchUniversities searches universities by name and optionally returns matching courses.
//
// @Summary      Pesquisar estabelecimentos de ensino
// @Description  Pesquisa universidades/institutos por query string (case-insensitive)
// @Tags         universities
// @Produce      json
// @Param        q              query  string  false  "Query de pesquisa"
// @Param        limit          query  int     false  "Limite de resultados (default: 20)"
// @Param        include_courses query  bool    false  "Incluir lista de cursos (default: false)"
// @Success      200  {array}   UniversitySearchResult
// @Router       /universities/search [get]
func SearchUniversities(c *gin.Context) {
	query := strings.ToLower(strings.TrimSpace(c.Query("q")))
	limit := 20
	if l := c.Query("limit"); l != "" {
		if parsed, err := parseLimit(l); err == nil {
			limit = parsed
		}
	}
	includeCourses := c.Query("include_courses") == "true"

	results := []UniversitySearchResult{}

	// If no query, return first N universities
	if query == "" {
		for i, name := range config.UnivList {
			if i >= limit {
				break
			}
			result := UniversitySearchResult{
				Estabelecimento: name,
				TotalCursos:     len(config.CoursesByUniv[name]),
			}
			if includeCourses {
				result.Cursos = config.CoursesByUniv[name]
			}
			results = append(results, result)
		}
		c.JSON(http.StatusOK, results)
		return
	}

	// Search universities that match the query
	for _, name := range config.UnivList {
		if strings.Contains(strings.ToLower(name), query) {
			result := UniversitySearchResult{
				Estabelecimento: name,
				TotalCursos:     len(config.CoursesByUniv[name]),
			}
			if includeCourses {
				result.Cursos = config.CoursesByUniv[name]
			}
			results = append(results, result)

			if len(results) >= limit {
				break
			}
		}
	}

	c.JSON(http.StatusOK, results)
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

// parseLimit safely parses a limit string to int.
func parseLimit(s string) (int, error) {
	var l int
	if _, err := fmt.Sscanf(s, "%d", &l); err != nil {
		return 0, err
	}
	if l < 1 {
		l = 1
	}
	if l > 100 {
		l = 100
	}
	return l, nil
}
