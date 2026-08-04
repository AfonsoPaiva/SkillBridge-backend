package models

import (
	"time"
)

// UniversityReview represents an evaluation submitted by a student for a university and specific course.
type UniversityReview struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	UserID         uint      `gorm:"not null;index" json:"user_id"`
	User           *User     `gorm:"foreignKey:UserID" json:"user,omitempty"`
	UniversityName string    `gorm:"type:varchar(255);not null;index" json:"university_name"`
	CourseName     string    `gorm:"type:varchar(255);not null;index" json:"course_name"`
	IsAnonymous    bool      `gorm:"default:false" json:"is_anonymous"`
	// Comment is optional; max 2000 characters enforced at both handler and DB level.
	Comment        string    `gorm:"type:varchar(2000)" json:"comment"`

	// University Criteria (0 to 10)
	CampusQuality         float64 `gorm:"type:decimal(4,2);default:0" json:"campus_quality"`
	LocationAccessibility float64 `gorm:"type:decimal(4,2);default:0" json:"location_accessibility"`
	CostOfLiving          float64 `gorm:"type:decimal(4,2);default:0" json:"cost_of_living"`
	SocialEnvironment     float64 `gorm:"type:decimal(4,2);default:0" json:"social_environment"`
	Reputation            float64 `gorm:"type:decimal(4,2);default:0" json:"reputation"`
	LibrariesQuality      float64 `gorm:"type:decimal(4,2);default:0" json:"libraries_quality"`
	FoodServices          float64 `gorm:"type:decimal(4,2);default:0" json:"food_services"`

	// Course Criteria (0 to 10)
	TeachersQuality        float64 `gorm:"type:decimal(4,2);default:0" json:"teachers_quality"`
	SubjectInterest        float64 `gorm:"type:decimal(4,2);default:0" json:"subject_interest"`
	CourseFacilities       float64 `gorm:"type:decimal(4,2);default:0" json:"course_facilities"`
	ClassmatesEnvironment  float64 `gorm:"type:decimal(4,2);default:0" json:"classmates_environment"`
	WorkloadBalance        float64 `gorm:"type:decimal(4,2);default:0" json:"workload_balance"`
	PracticalOpportunities float64 `gorm:"type:decimal(4,2);default:0" json:"practical_opportunities"`
	FutureProspects        float64 `gorm:"type:decimal(4,2);default:0" json:"future_prospects"`

	OverallScore float64   `gorm:"type:decimal(4,2);default:0;index" json:"overall_score"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// UniversityRankingSummary represents a university with aggregated ranking stats.
type UniversityRankingSummary struct {
	Estabelecimento  string   `json:"estabelecimento"`
	TotalCursos      int      `json:"total_cursos"`
	Cursos           []string `json:"cursos,omitempty"`
	Icon             string   `json:"icon"`
	AverageRating    float64  `json:"average_rating"`
	TotalReviews     int64    `json:"total_reviews"`
	UnivAvgRating    float64  `json:"univ_avg_rating"`
	CourseAvgRating  float64  `json:"course_avg_rating"`
}
