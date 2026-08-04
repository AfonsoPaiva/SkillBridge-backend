package handlers

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/paiva/SkillBridge/Backend/config"
	"github.com/paiva/SkillBridge/Backend/internal/database"
	"github.com/paiva/SkillBridge/Backend/internal/models"
)

// UniversitySearchResult represents a university with its courses.
type UniversitySearchResult struct {
	Estabelecimento string   `json:"estabelecimento"`
	TotalCursos     int      `json:"total_cursos"`
	Cursos          []string `json:"cursos,omitempty"`
}

// ListUniversities returns the unique list of educação establishments.
func ListUniversities(c *gin.Context) {
	c.JSON(http.StatusOK, config.UnivList)
}

// SearchUniversities searches universities by name and optionally returns matching courses.
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

// GetUniversityRankings returns the university ranking list populated with icons and ratings.
func GetUniversityRankings(c *gin.Context) {
	searchQuery := strings.ToLower(strings.TrimSpace(c.Query("q")))
	sortOption := c.Query("sort") // ranking_desc, ranking_asc, reviews_desc, name_asc

	// Fetch aggregated stats per university from DB
	type UnivAggregate struct {
		UniversityName string  `gorm:"column:university_name"`
		AvgScore       float64 `gorm:"column:avg_score"`
		TotalReviews   int64   `gorm:"column:total_reviews"`
		AvgUnivScore   float64 `gorm:"column:avg_univ_score"`
		AvgCourseScore float64 `gorm:"column:avg_course_score"`
	}

	var stats []UnivAggregate
	err := database.DB.Table("university_reviews").
		Select(`
			university_name,
			AVG(overall_score) as avg_score,
			COUNT(id) as total_reviews,
			AVG((campus_quality + location_accessibility + cost_of_living + social_environment + reputation + libraries_quality + food_services) / 7.0) as avg_univ_score,
			AVG((teachers_quality + subject_interest + course_facilities + classmates_environment + workload_balance + practical_opportunities + future_prospects) / 7.0) as avg_course_score
		`).
		Group("university_name").
		Scan(&stats).Error

	statsMap := make(map[string]UnivAggregate)
	if err == nil {
		for _, s := range stats {
			statsMap[s.UniversityName] = s
		}
	}

	var result []models.UniversityRankingSummary
	for _, entry := range config.UnivEntries {
		name := entry.Estabelecimento
		if searchQuery != "" && !strings.Contains(strings.ToLower(name), searchQuery) {
			// Check if any course matches query
			courseMatch := false
			for _, course := range entry.Cursos {
				if strings.Contains(strings.ToLower(course), searchQuery) {
					courseMatch = true
					break
				}
			}
			if !courseMatch {
				continue
			}
		}

		stat, exists := statsMap[name]
		avgRating := 0.0
		totalReviews := int64(0)
		avgUnivScore := 0.0
		avgCourseScore := 0.0

		if exists {
			avgRating = math.Round(stat.AvgScore*10) / 10
			totalReviews = stat.TotalReviews
			avgUnivScore = math.Round(stat.AvgUnivScore*10) / 10
			avgCourseScore = math.Round(stat.AvgCourseScore*10) / 10
		}

		summary := models.UniversityRankingSummary{
			Estabelecimento:  name,
			TotalCursos:      entry.TotalCursos,
			Cursos:           entry.Cursos,
			Icon:             entry.Icon,
			AverageRating:    avgRating,
			TotalReviews:     totalReviews,
			UnivAvgRating:    avgUnivScore,
			CourseAvgRating:  avgCourseScore,
		}
		result = append(result, summary)
	}

	// Sort logic
	switch sortOption {
	case "ranking_asc":
		sort.Slice(result, func(i, j int) bool {
			return result[i].AverageRating < result[j].AverageRating
		})
	case "reviews_desc":
		sort.Slice(result, func(i, j int) bool {
			return result[i].TotalReviews > result[j].TotalReviews
		})
	case "name_asc":
		sort.Slice(result, func(i, j int) bool {
			return result[i].Estabelecimento < result[j].Estabelecimento
		})
	default: // ranking_desc
		sort.Slice(result, func(i, j int) bool {
			if result[i].AverageRating == result[j].AverageRating {
				return result[i].TotalReviews > result[j].TotalReviews
			}
			return result[i].AverageRating > result[j].AverageRating
		})
	}

	c.JSON(http.StatusOK, result)
}

// GetUniversityReviews retrieves student evaluations for a university.
func GetUniversityReviews(c *gin.Context) {
	universityName := c.Query("university")
	courseName := c.Query("course")

	if universityName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Parâmetro 'university' é obrigatório."})
		return
	}

	query := database.DB.Model(&models.UniversityReview{}).Preload("User").Where("university_name = ?", universityName)
	if courseName != "" {
		query = query.Where("course_name = ?", courseName)
	}

	var reviews []models.UniversityReview
	if err := query.Order("created_at desc").Find(&reviews).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao carregar avaliações."})
		return
	}

	// Mask anonymous reviewers
	type MaskedReviewResponse struct {
		models.UniversityReview
		AuthorName   string `json:"author_name"`
		AuthorAvatar string `json:"author_avatar"`
		AuthorRole   string `json:"author_role"`
	}

	out := make([]MaskedReviewResponse, len(reviews))
	for i, r := range reviews {
		resp := MaskedReviewResponse{
			UniversityReview: r,
		}
		if r.IsAnonymous || r.User == nil {
			resp.AuthorName = "Estudante Anónimo"
			resp.AuthorAvatar = ""
			resp.AuthorRole = "Estudante"
			resp.User = nil
		} else {
			resp.AuthorName = r.User.Name
			resp.AuthorAvatar = r.User.AvatarURL
			resp.AuthorRole = r.User.Role
			if resp.AuthorRole == "" {
				resp.AuthorRole = "Estudante"
			}
		}
		out[i] = resp
	}

	c.JSON(http.StatusOK, out)
}

// CreateUniversityReviewInput payload structure.
type CreateUniversityReviewInput struct {
	UniversityName string  `json:"university_name" binding:"required"`
	CourseName     string  `json:"course_name" binding:"required"`
	IsAnonymous    bool    `json:"is_anonymous"`
	Comment        string  `json:"comment"`

	// University Criteria
	CampusQuality         float64 `json:"campus_quality"`
	LocationAccessibility float64 `json:"location_accessibility"`
	CostOfLiving          float64 `json:"cost_of_living"`
	SocialEnvironment     float64 `json:"social_environment"`
	Reputation            float64 `json:"reputation"`
	LibrariesQuality      float64 `json:"libraries_quality"`
	FoodServices          float64 `json:"food_services"`

	// Course Criteria
	TeachersQuality        float64 `json:"teachers_quality"`
	SubjectInterest        float64 `json:"subject_interest"`
	CourseFacilities       float64 `json:"course_facilities"`
	ClassmatesEnvironment  float64 `json:"classmates_environment"`
	WorkloadBalance        float64 `json:"workload_balance"`
	PracticalOpportunities float64 `json:"practical_opportunities"`
	FutureProspects        float64 `json:"future_prospects"`
}

// CreateUniversityReview allows an authenticated user to rate a university & course.
func CreateUniversityReview(c *gin.Context) {
	currentUserVal, exists := c.Get("currentUser")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Utilizador não autenticado."})
		return
	}
	currentUser := currentUserVal.(*models.User)

	var input CreateUniversityReviewInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dados inválidos: " + err.Error()})
		return
	}

	// Validate university existence
	courses, univExists := config.CoursesByUniv[input.UniversityName]
	if !univExists {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Universidade não encontrada no sistema."})
		return
	}

	// Validate course existence in university
	courseValid := false
	for _, cName := range courses {
		if strings.EqualFold(cName, input.CourseName) {
			courseValid = true
			input.CourseName = cName
			break
		}
	}
	if !courseValid {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Curso não encontrado nesta universidade."})
		return
	}

	// Helper function to clamp rating 0-10
	clamp := func(val float64) float64 {
		if val < 0 {
			return 0
		}
		if val > 10 {
			return 10
		}
		return val
	}

	u1 := clamp(input.CampusQuality)
	u2 := clamp(input.LocationAccessibility)
	u3 := clamp(input.CostOfLiving)
	u4 := clamp(input.SocialEnvironment)
	u5 := clamp(input.Reputation)
	u6 := clamp(input.LibrariesQuality)
	u7 := clamp(input.FoodServices)

	c1 := clamp(input.TeachersQuality)
	c2 := clamp(input.SubjectInterest)
	c3 := clamp(input.CourseFacilities)
	c4 := clamp(input.ClassmatesEnvironment)
	c5 := clamp(input.WorkloadBalance)
	c6 := clamp(input.PracticalOpportunities)
	c7 := clamp(input.FutureProspects)

	totalSum := u1 + u2 + u3 + u4 + u5 + u6 + u7 + c1 + c2 + c3 + c4 + c5 + c6 + c7
	overall := math.Round((totalSum/14.0)*10) / 10

	review := models.UniversityReview{
		UserID:                 currentUser.ID,
		UniversityName:         input.UniversityName,
		CourseName:             input.CourseName,
		IsAnonymous:            input.IsAnonymous,
		Comment:                strings.TrimSpace(input.Comment),
		CampusQuality:          u1,
		LocationAccessibility: u2,
		CostOfLiving:          u3,
		SocialEnvironment:     u4,
		Reputation:            u5,
		LibrariesQuality:      u6,
		FoodServices:          u7,
		TeachersQuality:        c1,
		SubjectInterest:        c2,
		CourseFacilities:       c3,
		ClassmatesEnvironment:  c4,
		WorkloadBalance:        c5,
		PracticalOpportunities: c6,
		FutureProspects:        c7,
		OverallScore:           overall,
	}

	if err := database.DB.Create(&review).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao guardar a avaliação na base de dados."})
		return
	}

	c.JSON(http.StatusCreated, review)
}

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
