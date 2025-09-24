package models

import (
	"database/sql"
	"time"
)

// ErrorResponse represents a generic error structure for API responses.
type ErrorResponse struct {
	Error string `json:"error"`
}

// Enums (moved or adapted from original files)
type AnnouncementPriority string

const (
	PriorityLow    AnnouncementPriority = "low"
	PriorityNormal AnnouncementPriority = "normal"
	PriorityHigh   AnnouncementPriority = "high"
	PriorityUrgent AnnouncementPriority = "urgent"
)

type LocationType string

const (
	LocTypeStage    LocationType = "stage"
	LocTypeDining   LocationType = "dining"
	LocTypeHelpdesk LocationType = "helpdesk"
	LocTypeParking  LocationType = "parking"
	LocTypeWater    LocationType = "water"
	LocTypeToilet   LocationType = "toilet"
	LocTypePoi      LocationType = "poi"
)

type AssignmentRole string

const (
	RoleVolunteer AssignmentRole = "volunteer"
	RoleLead      AssignmentRole = "lead"
	RoleSupport   AssignmentRole = "support"
)

type AssignmentStatus string

const (
	StatusAssigned  AssignmentStatus = "assigned"
	StatusStandby   AssignmentStatus = "standby"
	StatusCancelled AssignmentStatus = "cancelled"
)

// UserRole enum (defined here as the canonical type)
type UserRole string

const (
	UserRoleAdmin     UserRole = "admin"
	UserRoleFaculty   UserRole = "faculty"
	UserRoleVolunteer UserRole = "volunteer"
)

// Main Models
type Event struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	Venue     *string    `json:"venue"`
	TZ        string     `json:"tz"`
	StartsAt  *time.Time `json:"starts_at"`
	EndsAt    *time.Time `json:"ends_at"`
	CreatedAt time.Time  `json:"created_at"`
}

type Committee struct {
	ID          int64     `json:"id"`
	EventID     int64     `json:"event_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	EventName   string    `json:"event_name,omitempty"`
}

type Faculty struct {
	ID           int64    `json:"id"`
	Name         string   `json:"name"`
	Email        *string  `json:"email"`
	Phone        *string  `json:"phone"`
	Department   *string  `json:"department"`
	Role         UserRole `json:"role"` // Uses models.UserRole
	PasswordHash *string  `json:"-"`    // Don't expose password hash
}

type Volunteer struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Email        *string   `json:"email"`
	Phone        *string   `json:"phone"`
	Dept         *string   `json:"dept"`
	CollegeID    *string   `json:"college_id"`
	PasswordHash *string   `json:"-"`    // For volunteer login
	Role         UserRole  `json:"role"` // Uses models.UserRole
	CreatedAt    time.Time `json:"created_at"`
}

type VolunteerAssignment struct {
	ID            int64            `json:"id"`
	EventID       int64            `json:"event_id"`
	CommitteeID   int64            `json:"committee_id"`
	VolunteerID   int64            `json:"volunteer_id"`
	Role          AssignmentRole   `json:"role"`
	Status        AssignmentStatus `json:"status"`
	ReportingTime *time.Time       `json:"reporting_time"`
	Shift         *string          `json:"shift"`      // New field
	StartTime     *time.Time       `json:"start_time"` // New field
	EndTime       *time.Time       `json:"end_time"`   // New field
	Notes         *string          `json:"notes"`
	CreatedAt     time.Time        `json:"created_at"`

	// Enriched fields for responses
	VolunteerName      string  `json:"volunteer_name,omitempty"`
	VolunteerEmail     *string `json:"volunteer_email,omitempty"`
	VolunteerCollegeID *string `json:"volunteer_college_id,omitempty"` // NEW: Added VolunteerCollegeID
	CommitteeName      string  `json:"committee_name,omitempty"`
	EventName          string  `json:"event_name,omitempty"`
}

// Updated Attendance struct (no approval fields, added Shift field)
type Attendance struct {
	ID           int64      `json:"id"`
	AssignmentID int64      `json:"assignment_id"`
	CheckInTime  time.Time  `json:"check_in_time"`
	CheckOutTime *time.Time `json:"check_out_time"`  // Ptr for nullable
	Lat          *float64   `json:"lat"`             // Ptr for nullable
	Lng          *float64   `json:"lng"`             // Ptr for nullable
	Shift        *string    `json:"shift,omitempty"` // NEW: Added Shift field for context

	// Enriched fields for responses (assuming these are populated by joins)
	VolunteerID        int64   `json:"volunteer_id,omitempty"`
	CommitteeID        int64   `json:"committee_id,omitempty"`
	EventID            int64   `json:"event_id,omitempty"`
	VolunteerName      string  `json:"volunteer_name,omitempty"`
	VolunteerCollegeID *string `json:"volunteer_college_id,omitempty"` // NEW: Added VolunteerCollegeID
	CommitteeName      string  `json:"committee_name,omitempty"`
	EventName          string  `json:"event_name,omitempty"`
}

type Announcement struct {
	ID          int64                `json:"id"`
	EventID     int64                `json:"event_id"`
	CommitteeID *int64               `json:"committee_id"`
	Title       string               `json:"title"`
	Body        string               `json:"body"`
	Priority    AnnouncementPriority `json:"priority"`
	CreatedBy   *int64               `json:"created_by"`
	CreatedAt   time.Time            `json:"created_at"`
	ExpiresAt   *time.Time           `json:"expires_at"`

	// Enriched fields for responses
	CreatedByName *string `json:"created_by_name,omitempty"`
	CommitteeName *string `json:"committee_name,omitempty"`
}

type Location struct {
	ID          int64        `json:"id"`
	EventID     int64        `json:"event_id"`
	Name        string       `json:"name"`
	Type        LocationType `json:"type"`
	Description string       `json:"description"`
	Lat         float64      `json:"lat"`
	Lng         float64      `json:"lng"`
}

type CarbonFootprint struct {
	ID              int64     `json:"id"`
	EventID         int64     `json:"event_id"`
	CommitteeID     *int64    `json:"committee_id"`
	MetricDate      time.Time `json:"metric_date"`
	WasteBags       int       `json:"waste_bags"`
	PlasticKg       float64   `json:"plastic_kg"`
	VolunteersCount int       `json:"volunteers_count"`
	Notes           *string   `json:"notes"`
	CreatedAt       time.Time `json:"created_at"`
}

type ApiKey struct {
	ID             int64      `json:"id"`
	Label          string     `json:"label"`
	Role           UserRole   `json:"role"` // Uses models.UserRole
	KeyHash        []byte     `json:"-"`
	OwnerFacultyID *int64     `json:"owner_faculty_id"`
	CreatedAt      time.Time  `json:"created_at"`
	RevokedAt      *time.Time `json:"revoked_at"`
}

type AuditLog struct {
	ID          int64     `json:"id"`
	ActorType   string    `json:"actor_type"`
	ActorID     *string   `json:"actor_id"`
	EventID     *int64    `json:"event_id"`
	EntityTable string    `json:"entity_table"`
	EntityID    string    `json:"entity_id"`
	Action      string    `json:"action"`
	Diff        []byte    `json:"diff"`
	CreatedAt   time.Time `json:"created_at"`
}

// NEW: Question model for "May I Help You"
type Question struct {
	ID             int64      `json:"id"`
	VolunteerID    *int64     `json:"volunteer_id"` // Nullable if anonymous is allowed
	VolunteerName  *string    `json:"volunteer_name,omitempty"`
	QuestionText   string     `json:"question_text"`
	AskedAt        time.Time  `json:"asked_at"`
	EventID        *int64     `json:"event_id"`     // Optional: event context for the question
	CommitteeID    *int64     `json:"committee_id"` // Optional: committee context for the question
	AnsweredBy     *int64     `json:"answered_by"`
	AnsweredByName *string    `json:"answered_by_name,omitempty"`
	AnswerText     *string    `json:"answer_text"` // Null if not answered
	AnsweredAt     *time.Time `json:"answered_at"` // Null if not answered
}

// Request DTOs (Data Transfer Objects)

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type LoginResponse struct {
	AccessToken  string   `json:"access_token"`
	ExpiresIn    int      `json:"expires_in"`
	RefreshToken *string  `json:"refresh_token,omitempty"` // Refresh token might be optional depending on implementation
	Role         UserRole `json:"role"`                    // Uses models.UserRole
	UserID       int64    `json:"user_id"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type RegisterFacultyRequest struct { // Admin registers faculty
	Name     string    `json:"name"`
	Email    string    `json:"email"`
	Password string    `json:"password"`
	Role     *UserRole `json:"role"` // Uses models.UserRole
}

type RegisterVolunteerRequest struct { // Student self-registers
	Name      string  `json:"name"`
	Email     string  `json:"email"`
	Password  string  `json:"password"`
	Phone     *string `json:"phone,omitempty"`
	Dept      *string `json:"dept,omitempty"`
	CollegeID *string `json:"college_id,omitempty"`
}

type SetVolunteerPasswordRequest struct {
	OldPassword *string `json:"old_password,omitempty"` // Optional if setting for the first time or if admin allows reset
	NewPassword string  `json:"new_password"`
}

type CreateVolunteerRequest struct { // Admin creates volunteer (can omit password, or set initial)
	Name      string  `json:"name"`
	Email     *string `json:"email"`
	Phone     *string `json:"phone"`
	Dept      *string `json:"dept"`
	CollegeID *string `json:"college_id"`
	Password  *string `json:"password,omitempty"` // Admin can set an initial password
}

type UpdateVolunteerRequest struct {
	Name      *string   `json:"name"`
	Email     *string   `json:"email"`
	Phone     *string   `json:"phone"`
	Dept      *string   `json:"dept"`
	CollegeID *string   `json:"college_id"`
	Password  *string   `json:"password"` // Admin can update password
	Role      *UserRole `json:"role"`     // Uses models.UserRole
}

type CreateVolunteerAssignmentRequest struct {
	EventID       int64            `json:"event_id"`
	CommitteeID   int64            `json:"committee_id"`
	VolunteerID   int64            `json:"volunteer_id"`
	Role          AssignmentRole   `json:"role"`
	Status        AssignmentStatus `json:"status"`
	ReportingTime *time.Time       `json:"reporting_time"`
	Shift         *string          `json:"shift"`
	StartTime     *time.Time       `json:"start_time"`
	EndTime       *time.Time       `json:"end_time"`
	Notes         *string          `json:"notes"`
}

type UpdateVolunteerAssignmentRequest struct {
	Role          *AssignmentRole   `json:"role"`
	Status        *AssignmentStatus `json:"status"`
	ReportingTime *time.Time        `json:"reporting_time"`
	Shift         *string           `json:"shift"`
	StartTime     *time.Time        `json:"start_time"`
	EndTime       *time.Time        `json:"end_time"`
	Notes         *string           `json:"notes"`
}

type CheckInRequest struct {
	AssignmentID int64    `json:"assignment_id"`
	Lat          *float64 `json:"lat"`
	Lng          *float64 `json:"lng"`
	TimeISO      *string  `json:"time,omitempty"` // RFC3339, defaults to now
}

type CheckOutRequest struct {
	AttendanceID int64   `json:"attendance_id"`
	TimeISO      *string `json:"time,omitempty"` // RFC3339, defaults to now
}

type CreateAnnouncementRequest struct {
	EventID     int64                `json:"event_id"`
	CommitteeID *int64               `json:"committee_id"`
	Title       string               `json:"title"`
	Body        string               `json:"body"`
	Priority    AnnouncementPriority `json:"priority"`
	ExpiresAt   *time.Time           `json:"expires_at"`
}

type UpdateAnnouncementRequest struct {
	CommitteeID *int64                `json:"committee_id"`
	Title       *string               `json:"title"`
	Body        *string               `json:"body"`
	Priority    *AnnouncementPriority `json:"priority"`
	ExpiresAt   *time.Time            `json:"expires_at"`
}

type CreateLocationRequest struct {
	EventID     int64        `json:"event_id"`
	Name        string       `json:"name"`
	Type        LocationType `json:"type"`
	Description *string      `json:"description"`
	Lat         float64      `json:"lat"`
	Lng         float64      `json:"lng"`
}

type UpdateLocationRequest struct {
	Name        *string       `json:"name"`
	Type        *LocationType `json:"type"`
	Description *string       `json:"description"`
	Lat         *float64      `json:"lat"`
	Lng         *float64      `json:"lng"`
}

type SubmitCarbonRequest struct {
	EventID         int64      `json:"event_id"`
	CommitteeID     *int64     `json:"committee_id"`
	MetricDate      *time.Time `json:"metric_date"`
	WasteBags       int        `json:"waste_bags"`
	PlasticKg       float64    `json:"plastic_kg"`
	VolunteersCount int        `json:"volunteers_count"`
	Notes           *string    `json:"notes"`
}

type UpdateCarbonFootprintRequest struct {
	WasteBags       *int     `json:"waste_bags"`
	PlasticKg       *float64 `json:"plastic_kg"`
	VolunteersCount *int     `json:"volunteers_count"`
	Notes           *string  `json:"notes"`
}

// NEW: Question DTOs
type CreateQuestionRequest struct {
	QuestionText string `json:"question_text"`
	EventID      *int64 `json:"event_id,omitempty"`
	CommitteeID  *int64 `json:"committee_id,omitempty"`
}

type AnswerQuestionRequest struct {
	AnswerText string `json:"answer_text"`
}

type CreateCommitteeRequest struct {
	EventID     int64   `json:"event_id"`    // Required: The event this committee belongs to
	Name        string  `json:"name"`        // Required: Name of the committee
	Description *string `json:"description"` // Optional: Description of the committee
}

// UpdateCommitteeRequest represents the request body for updating an existing committee.
type UpdateCommitteeRequest struct {
	Name        *string `json:"name"`        // Optional: New name for the committee
	Description *string `json:"description"` // Optional: New description for the committee
}

// NEW: Struct for the revised Pending endpoint (now list assignments that *could* have attendance)
type PendingShiftRow struct {
	AssignmentID       int64            `json:"assignment_id"`
	EventID            int64            `json:"event_id"`
	EventName          string           `json:"event_name"`
	CommitteeID        int64            `json:"committee_id"`
	CommitteeName      string           `json:"committee_name"`
	VolunteerID        int64            `json:"volunteer_id"`
	VolunteerName      string           `json:"volunteer_name"`
	VolunteerDept      *string          `json:"volunteer_dept,omitempty"`
	VolunteerCollegeID *string          `json:"volunteer_college_id,omitempty"` // NEW: Added College ID
	AssignmentRole     AssignmentRole   `json:"assignment_role"`
	AssignmentStatus   AssignmentStatus `json:"assignment_status"`
	ReportingTime      *time.Time       `json:"reporting_time,omitempty"`
	StartTime          *time.Time       `json:"start_time,omitempty"`
	EndTime            *time.Time       `json:"end_time,omitempty"`
	Shift              *string          `json:"shift,omitempty"`
	Notes              *string          `json:"notes,omitempty"`
}
type AssignmentWithCheckinStatus struct {
	// Embed VolunteerAssignment to inherit all its fields
	VolunteerAssignment

	// Additional fields for attendance status
	ActiveAttendanceID sql.NullInt64 `json:"active_attendance_id,omitempty"` // The ID of the active attendance record, if any
	IsCheckedIn        bool          `json:"is_checked_in"`                  // True if the volunteer is checked in for the specific queried day and assignment
}
